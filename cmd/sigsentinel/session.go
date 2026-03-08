package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/jentfoo/SignalSentinel/internal/sds200"
)

type SDS200Client interface {
	Resync() (sds200.RuntimeStatus, error)
	StartPushScannerInfo(intervalMS int) error
	Hold(tkw, x1, x2 string) error
	JumpMode(mode, index string) error
	OnTelemetry(handler func(sds200.RuntimeStatus))
	TelemetrySnapshot() sds200.RuntimeStatus
	Close() error
}

type SDS200Factory func(sds200.ClientConfig) (SDS200Client, error)

type SessionConfig struct {
	Address             string
	ControlPort         int
	ResponseTimeout     time.Duration
	Retries             int
	ReadTimeout         time.Duration
	WriteTimeout        time.Duration
	PushIntervalMS      int
	HealthCheckInterval time.Duration
	ReconnectDelay      time.Duration
	MaxReconnectFails   int
	Logger              *log.Logger
	Factory             SDS200Factory
}

type ScannerSession struct {
	cfg SessionConfig

	ctx    context.Context
	cancel context.CancelFunc

	mu     sync.Mutex
	client SDS200Client

	fatalErr chan error
	wg       sync.WaitGroup
	closeMu  sync.Once
	stateHub *stateHub

	controlMu sync.Mutex
	controlCh chan ControlIntent
}

type ControlIntent string

const (
	IntentResumeScan ControlIntent = "resume_scan"
	IntentHold       ControlIntent = "hold"
)

func NewScannerSession(parent context.Context, cfg SessionConfig, hub *stateHub) (*ScannerSession, error) {
	if cfg.Address == "" {
		return nil, errors.New("scanner address is required")
	}
	if cfg.ControlPort == 0 {
		cfg.ControlPort = sds200.DefaultControlPort
	}
	if cfg.ResponseTimeout == 0 {
		cfg.ResponseTimeout = 2 * time.Second
	}
	if cfg.Retries <= 0 {
		cfg.Retries = 3
	}
	if cfg.ReadTimeout == 0 {
		cfg.ReadTimeout = cfg.ResponseTimeout
	}
	if cfg.WriteTimeout == 0 {
		cfg.WriteTimeout = cfg.ResponseTimeout
	}
	if cfg.HealthCheckInterval <= 0 {
		cfg.HealthCheckInterval = 20 * time.Second
	}
	if cfg.ReconnectDelay <= 0 {
		cfg.ReconnectDelay = 3 * time.Second
	}
	if cfg.MaxReconnectFails <= 0 {
		cfg.MaxReconnectFails = 5
	}
	if cfg.Factory == nil {
		return nil, errors.New("scanner factory is required")
	}

	ctx, cancel := context.WithCancel(parent)
	s := &ScannerSession{
		cfg:       cfg,
		ctx:       ctx,
		cancel:    cancel,
		fatalErr:  make(chan error, 1),
		stateHub:  hub,
		controlCh: make(chan ControlIntent, 1),
	}
	if err := s.connectAndSync(); err != nil {
		cancel()
		return nil, err
	}
	s.wg.Add(2)
	go s.supervise()
	go s.controlLoop()
	return s, nil
}

func (s *ScannerSession) Fatal() <-chan error {
	return s.fatalErr
}

func (s *ScannerSession) Close() error {
	s.closeMu.Do(func() {
		s.cancel()
		s.wg.Wait()
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.client != nil {
			_ = s.client.Close()
			s.client = nil
		}
	})
	return nil
}

func (s *ScannerSession) EnqueueControl(intent ControlIntent) {
	if s == nil {
		return
	}
	s.controlMu.Lock()
	select {
	case <-s.controlCh:
	default:
	}
	select {
	case s.controlCh <- intent:
	case <-s.ctx.Done():
	}
	s.controlMu.Unlock()
}

func (s *ScannerSession) supervise() {
	defer s.wg.Done()
	ticker := time.NewTicker(s.cfg.HealthCheckInterval)
	defer ticker.Stop()

	consecutiveFails := 0
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			if err := s.healthCheck(); err != nil {
				consecutiveFails++
				s.logf("scanner health check failed (%d/%d): %v", consecutiveFails, s.cfg.MaxReconnectFails, err)
				if consecutiveFails >= s.cfg.MaxReconnectFails {
					s.signalFatal(fmt.Errorf("scanner reconnect budget exceeded: %w", err))
					return
				}
				select {
				case <-s.ctx.Done():
					return
				case <-time.After(s.cfg.ReconnectDelay):
				}
				continue
			}
			consecutiveFails = 0
		}
	}
}

func (s *ScannerSession) healthCheck() error {
	s.mu.Lock()
	c := s.client
	s.mu.Unlock()
	if c == nil {
		return s.connectAndSync()
	}
	status, err := c.Resync()
	if err != nil {
		s.publishScannerState(sds200.RuntimeStatus{Connected: false})
		s.mu.Lock()
		if s.client != nil {
			_ = s.client.Close()
			s.client = nil
		}
		s.mu.Unlock()
		if recErr := s.connectAndSync(); recErr != nil {
			return recErr
		}
	} else {
		s.publishScannerState(status)
	}
	return nil
}

func (s *ScannerSession) connectAndSync() error {
	client, err := s.cfg.Factory(sds200.ClientConfig{
		Address:         s.cfg.Address,
		ControlPort:     s.cfg.ControlPort,
		ResponseTimeout: s.cfg.ResponseTimeout,
		Retries:         s.cfg.Retries,
		ReadTimeout:     s.cfg.ReadTimeout,
		WriteTimeout:    s.cfg.WriteTimeout,
	})
	if err != nil {
		return fmt.Errorf("create scanner client: %w", err)
	}
	status, err := client.Resync()
	if err != nil {
		_ = client.Close()
		return fmt.Errorf("initial scanner resync: %w", err)
	}
	client.OnTelemetry(func(status sds200.RuntimeStatus) {
		s.publishScannerState(status)
	})
	if err := client.StartPushScannerInfo(s.cfg.PushIntervalMS); err != nil {
		_ = client.Close()
		return fmt.Errorf("enable scanner push telemetry: %w", err)
	}

	s.mu.Lock()
	old := s.client
	s.client = client
	s.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	s.publishScannerState(status)
	s.logf("scanner session connected and synchronized")
	return nil
}

func (s *ScannerSession) controlLoop() {
	defer s.wg.Done()
	for {
		select {
		case <-s.ctx.Done():
			return
		case intent := <-s.controlCh:
			if err := s.executeIntent(intent); err != nil {
				s.logf("control intent %q failed: %v", intent, err)
			}
		}
	}
}

func (s *ScannerSession) executeIntent(intent ControlIntent) error {
	s.mu.Lock()
	client := s.client
	s.mu.Unlock()
	if client == nil {
		return errors.New("scanner client unavailable")
	}

	switch intent {
	case IntentResumeScan:
		return client.JumpMode("SCN_MODE", "0")
	case IntentHold:
		status := client.TelemetrySnapshot()
		target := status.HoldTarget
		if target.Keyword == "" || target.Arg1 == "" {
			return errors.New("hold target unavailable for current scanner state")
		}
		return client.Hold(target.Keyword, target.Arg1, target.Arg2)
	default:
		return fmt.Errorf("unsupported control intent: %s", intent)
	}
}

func (s *ScannerSession) publishScannerState(status sds200.RuntimeStatus) {
	if s.stateHub == nil {
		return
	}
	s.stateHub.publish(RuntimeState{Scanner: status})
}

func (s *ScannerSession) signalFatal(err error) {
	s.logf("hard fault: %v", err)
	select {
	case s.fatalErr <- err:
	default:
	}
	s.cancel()
}

func (s *ScannerSession) logf(format string, args ...any) {
	if s.cfg.Logger != nil {
		s.cfg.Logger.Printf("session: "+format, args...)
	}
}
