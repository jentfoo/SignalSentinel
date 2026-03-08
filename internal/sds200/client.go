package sds200

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"
)

type ClientConfig struct {
	Address         string
	ControlPort     int
	ResponseTimeout time.Duration
	Retries         int
	QueueSize       int
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
}

func (c ClientConfig) withDefaults() ClientConfig {
	if c.ControlPort == 0 {
		c.ControlPort = DefaultControlPort
	}
	if c.ResponseTimeout == 0 {
		c.ResponseTimeout = 2 * time.Second
	}
	if c.Retries <= 0 {
		c.Retries = 3
	}
	if c.QueueSize <= 0 {
		c.QueueSize = 128
	}
	if c.ReadTimeout == 0 {
		c.ReadTimeout = c.ResponseTimeout
	}
	if c.WriteTimeout == 0 {
		c.WriteTimeout = c.ResponseTimeout
	}
	return c
}

type commandJob struct {
	spec CommandSpec
	args []string
	resp chan commandResult
}

type commandResult struct {
	resp CommandResponse
	err  error
}

type Client struct {
	cfg       ClientConfig
	transport *UDPTransport
	telemetry *TelemetryStore

	ctx    context.Context
	cancel context.CancelFunc
	queue  chan commandJob
	wg     sync.WaitGroup

	subMu             sync.RWMutex
	telemetryHandlers []func(RuntimeStatus)
	rawHandlers       []func(CommandResponse)
	pushXML           map[string]*xmlReassembler

	closeOnce sync.Once
	closeErr  error
}

func NewClient(cfg ClientConfig) (*Client, error) {
	cfg = cfg.withDefaults()
	if cfg.Address == "" {
		return nil, errors.New("address is required")
	}

	transport, err := NewUDPTransport(TransportConfig{
		Address:      cfg.Address,
		Port:         cfg.ControlPort,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
	})
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	c := &Client{
		cfg:       cfg,
		transport: transport,
		telemetry: NewTelemetryStore(),
		ctx:       ctx,
		cancel:    cancel,
		queue:     make(chan commandJob, cfg.QueueSize),
		pushXML:   map[string]*xmlReassembler{},
	}
	c.wg.Add(1)
	go c.commandLoop()
	return c, nil
}

func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		c.cancel()
		c.wg.Wait()
		c.closeErr = c.transport.Close()
	})
	return c.closeErr
}

func (c *Client) TelemetrySnapshot() RuntimeStatus {
	return c.telemetry.Snapshot()
}

func (c *Client) OnTelemetry(handler func(RuntimeStatus)) {
	if handler == nil {
		return
	}
	c.subMu.Lock()
	defer c.subMu.Unlock()
	c.telemetryHandlers = append(c.telemetryHandlers, handler)
}

func (c *Client) OnRawResponse(handler func(CommandResponse)) {
	if handler == nil {
		return
	}
	c.subMu.Lock()
	defer c.subMu.Unlock()
	c.rawHandlers = append(c.rawHandlers, handler)
}

func (c *Client) commandLoop() {
	defer c.wg.Done()
	pollTicker := time.NewTicker(20 * time.Millisecond)
	defer pollTicker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			return
		case job, ok := <-c.queue:
			if !ok {
				return
			}
			resp, err := c.runWithRetry(job.spec, job.args)
			job.resp <- commandResult{resp: resp, err: err}
		case <-pollTicker.C:
			c.processUnsolicitedOnce()
		}
	}
}

func (c *Client) processUnsolicitedOnce() {
	ctx, cancel := context.WithTimeout(c.ctx, 20*time.Millisecond)
	defer cancel()

	msg, err := c.transport.Receive(ctx)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return
		}
		log.Printf("sds200: unsolicited receive error: %v", err)
		return
	}
	cmd, fields := parseDelimitedFields(msg)
	if cmd == "" {
		return
	}
	c.handleUnsolicited(cmd, fields, msg)
}

func (c *Client) runWithRetry(spec CommandSpec, args []string) (CommandResponse, error) {
	var lastErr error
	for attempt := 1; attempt <= c.cfg.Retries; attempt++ {
		resp, err := c.runOnce(spec, args)
		if err == nil {
			return resp, nil
		}
		lastErr = err
	}
	return CommandResponse{}, fmt.Errorf("%s failed after %d attempts: %w", spec.Command, c.cfg.Retries, lastErr)
}

func (c *Client) runOnce(spec CommandSpec, args []string) (CommandResponse, error) {
	frame := buildCommand(spec.Command, args...)
	ctx, cancel := context.WithTimeout(c.ctx, c.cfg.ResponseTimeout)
	defer cancel()
	if err := c.transport.Send(ctx, frame); err != nil {
		return CommandResponse{}, err
	}
	if spec.Mode == ModeNoResponse {
		return CommandResponse{Command: spec.Command}, nil
	}

	xmlAssembler := newXMLReassembler()
	for {
		msg, err := c.transport.Receive(ctx)
		if err != nil {
			return CommandResponse{}, err
		}
		cmd, fields := parseDelimitedFields(msg)
		if cmd == "" {
			continue
		}

		if !commandMatchesResponse(spec.Command, cmd) {
			c.handleUnsolicited(cmd, fields, msg)
			continue
		}

		resp := CommandResponse{Command: cmd, Fields: fields, Raw: msg}
		switch spec.Mode {
		case ModeNormal, ModeBinary:
			c.dispatchRaw(resp)
			return resp, nil
		case ModeXML:
			fragment, fragErr := parseXMLFragment(msg)
			if fragErr != nil {
				return CommandResponse{}, fragErr
			}
			xmlAssembler.Add(fragment)
			if xmlAssembler.Complete() {
				resp.XML = xmlAssembler.Bytes()
				c.dispatchRaw(resp)
				return resp, nil
			}
			if fragment.EOT {
				if missing := xmlAssembler.MissingSequences(); len(missing) > 0 {
					return CommandResponse{}, fmt.Errorf("xml fragment loss: missing sequence(s) %v", missing)
				}
			}
		}
	}
}

func commandMatchesResponse(expected, got string) bool {
	if got == expected {
		return true
	}
	// GWF binary variant: newer firmware may label the response as GW2
	return expected == cmdGWF && got == cmdGW2
}

func (c *Client) handleUnsolicited(cmd string, fields []string, raw []byte) {
	resp := CommandResponse{Command: cmd, Fields: fields, Raw: raw}
	switch cmd {
	case cmdPSI, cmdGSI:
		// PSI acknowledgments (e.g. PSI,OK) are not XML; skip them.
		if !bytes.Contains(raw, []byte(",<XML>,")) {
			break
		}
		if fragment, err := parseXMLFragment(raw); err == nil {
			asm := c.pushXML[cmd]
			if asm == nil {
				asm = newXMLReassembler()
				c.pushXML[cmd] = asm
			}
			asm.Add(fragment)
			if asm.Complete() {
				resp.XML = asm.Bytes()
				delete(c.pushXML, cmd)
				if info, parseErr := ParseScannerInfoXML(resp.XML); parseErr == nil {
					status := c.telemetry.UpdateFromScannerInfo(info)
					c.dispatchTelemetry(status)
				}
			} else if fragment.EOT {
				// Drop incomplete unsolicited frame sets and wait for next push cycle
				delete(c.pushXML, cmd)
			}
		} else {
			log.Printf("sds200: failed to parse %s xml fragment: %v", cmd, err)
		}
	case cmdGST:
		if gst, err := ParseGST(fields); err == nil {
			status := c.telemetry.UpdateFromGST(gst)
			c.dispatchTelemetry(status)
		} else {
			log.Printf("sds200: failed to parse GST: %v", err)
		}
	case cmdSTS:
		if sts, err := ParseSTS(fields); err == nil {
			status := c.telemetry.UpdateFromSTS(sts)
			c.dispatchTelemetry(status)
		} else {
			log.Printf("sds200: failed to parse STS: %v", err)
		}
	}
	c.dispatchRaw(resp)
}

func (c *Client) dispatchTelemetry(status RuntimeStatus) {
	c.subMu.RLock()
	handlers := append([]func(RuntimeStatus){}, c.telemetryHandlers...)
	c.subMu.RUnlock()
	for _, h := range handlers {
		h(status)
	}
}

func (c *Client) dispatchRaw(resp CommandResponse) {
	c.subMu.RLock()
	handlers := append([]func(CommandResponse){}, c.rawHandlers...)
	c.subMu.RUnlock()
	for _, h := range handlers {
		h(resp)
	}
}

func (c *Client) execute(spec CommandSpec, args ...string) (CommandResponse, error) {
	if c.ctx.Err() != nil {
		return CommandResponse{}, errors.New("client closed")
	}
	job := commandJob{spec: spec, args: args, resp: make(chan commandResult, 1)}
	select {
	case c.queue <- job:
	case <-c.ctx.Done():
		return CommandResponse{}, c.ctx.Err()
	}
	select {
	case result := <-job.resp:
		return result.resp, result.err
	case <-c.ctx.Done():
		return CommandResponse{}, c.ctx.Err()
	}
}

func (c *Client) Resync() (RuntimeStatus, error) {
	if _, err := c.Model(); err != nil {
		return RuntimeStatus{}, err
	} else if _, err := c.FirmwareVersion(); err != nil {
		return RuntimeStatus{}, err
	}
	info, err := c.GetScannerInfo()
	if err != nil {
		return RuntimeStatus{}, err
	}
	return c.telemetry.UpdateFromScannerInfo(info), nil
}
