package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/jentfoo/SignalSentinel/internal/sds200"
	"github.com/jentfoo/SignalSentinel/internal/store"
)

type Options struct {
	ConfigPath          string
	Logger              *log.Logger
	ResponseTimeout     time.Duration
	Retries             int
	ReadTimeout         time.Duration
	WriteTimeout        time.Duration
	PushIntervalMS      int
	HealthCheckInterval time.Duration
	ReconnectDelay      time.Duration
	MaxReconnectFails   int
	ClientFactory       SDS200Factory
}

func (o Options) withDefaults() Options {
	if o.Logger == nil {
		o.Logger = log.New(os.Stderr, "", log.LstdFlags|log.LUTC)
	}
	if o.ResponseTimeout == 0 {
		o.ResponseTimeout = 2 * time.Second
	}
	if o.Retries <= 0 {
		o.Retries = 3
	}
	if o.ReadTimeout == 0 {
		o.ReadTimeout = o.ResponseTimeout
	}
	if o.WriteTimeout == 0 {
		o.WriteTimeout = o.ResponseTimeout
	}
	if o.PushIntervalMS <= 0 {
		o.PushIntervalMS = 1000
	}
	if o.HealthCheckInterval == 0 {
		o.HealthCheckInterval = 20 * time.Second
	}
	if o.ReconnectDelay == 0 {
		o.ReconnectDelay = 3 * time.Second
	}
	if o.MaxReconnectFails <= 0 {
		o.MaxReconnectFails = 5
	}
	if o.ClientFactory == nil {
		o.ClientFactory = defaultClientFactory
	}
	return o
}

type Runtime struct {
	doc            *store.Document
	store          *store.Store
	session        *ScannerSession
	ctx            context.Context
	state          *stateHub
	recordingsPath string
}

func (r *Runtime) Config() *store.Document {
	return r.doc
}

func (r *Runtime) Store() *store.Store { return r.store }

func (r *Runtime) RecordingsPath() string {
	if r == nil {
		return ""
	}
	return r.recordingsPath
}

func (r *Runtime) StateSnapshot() RuntimeState {
	if r == nil || r.state == nil {
		return RuntimeState{}
	}
	return r.state.snapshot()
}

func (r *Runtime) SubscribeState(ctx context.Context) <-chan RuntimeState {
	if r == nil || r.state == nil {
		ch := make(chan RuntimeState, 1)
		ch <- RuntimeState{}
		close(ch)
		return ch
	}
	return r.state.subscribe(ctx)
}

func (r *Runtime) Wait() error {
	select {
	case <-r.ctx.Done():
		select {
		case err := <-r.session.Fatal():
			if err != nil && !errors.Is(err, context.Canceled) {
				return err
			}
		default:
		}
		return nil
	case err := <-r.session.Fatal():
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	}
}

func (r *Runtime) Close() error {
	if r == nil || r.session == nil {
		return nil
	}
	return r.session.Close()
}

func StartRuntime(ctx context.Context, opts Options) (*Runtime, error) {
	opts = opts.withDefaults()

	s := store.New(opts.ConfigPath)
	doc, err := s.Load()
	if err != nil {
		return nil, fmt.Errorf("load config store: %w", err)
	}
	if err := doc.Validate(); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}
	recordingsPath, err := resolveRecordingsPath(doc, s)
	if err != nil {
		return nil, err
	}
	if err := ensureDirectoryWritable(recordingsPath); err != nil {
		return nil, fmt.Errorf("validate recordings path: %w", err)
	}

	scannerCfg := SessionConfig{
		Address:             doc.Config.Scanner.IP,
		ControlPort:         doc.Config.Scanner.ControlPort,
		ResponseTimeout:     opts.ResponseTimeout,
		Retries:             opts.Retries,
		ReadTimeout:         opts.ReadTimeout,
		WriteTimeout:        opts.WriteTimeout,
		PushIntervalMS:      opts.PushIntervalMS,
		HealthCheckInterval: opts.HealthCheckInterval,
		ReconnectDelay:      opts.ReconnectDelay,
		MaxReconnectFails:   opts.MaxReconnectFails,
		Logger:              opts.Logger,
		Factory:             opts.ClientFactory,
	}

	hub := newStateHub()
	session, err := NewScannerSession(ctx, scannerCfg, hub)
	if err != nil {
		return nil, fmt.Errorf("start scanner session: %w", err)
	}

	return &Runtime{
		doc:            doc,
		store:          s,
		session:        session,
		ctx:            ctx,
		state:          hub,
		recordingsPath: recordingsPath,
	}, nil
}

func defaultClientFactory(cfg sds200.ClientConfig) (SDS200Client, error) {
	return sds200.NewClient(cfg)
}

func resolveRecordingsPath(doc *store.Document, s *store.Store) (string, error) {
	if doc == nil {
		return "", errors.New("runtime config missing")
	}
	recordingsPath := doc.Config.Storage.RecordingsPath
	if filepath.IsAbs(recordingsPath) {
		return recordingsPath, nil
	}
	storeDir, err := s.Dir()
	if err != nil {
		return "", fmt.Errorf("resolve store directory: %w", err)
	}
	return filepath.Join(storeDir, recordingsPath), nil
}

func ensureDirectoryWritable(path string) error {
	if err := os.MkdirAll(path, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(path, ".write-test-*")
	if err != nil {
		return err
	}
	name := f.Name()
	if err := f.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	return os.Remove(name)
}
