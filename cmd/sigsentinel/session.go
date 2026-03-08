package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/jentfoo/SignalSentinel/internal/sds200"
	"github.com/jentfoo/SignalSentinel/internal/store"
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
	Scanner             store.ScannerConfig
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

func defaultClientFactory(cfg sds200.ClientConfig) (SDS200Client, error) {
	return sds200.NewClient(cfg)
}

func (c SessionConfig) withDefaults() SessionConfig {
	if c.Scanner.ControlPort == 0 {
		c.Scanner.ControlPort = sds200.DefaultControlPort
	}
	if c.ResponseTimeout == 0 {
		c.ResponseTimeout = 2 * time.Second
	}
	if c.Retries <= 0 {
		c.Retries = 3
	}
	if c.ReadTimeout == 0 {
		c.ReadTimeout = c.ResponseTimeout
	}
	if c.WriteTimeout == 0 {
		c.WriteTimeout = c.ResponseTimeout
	}
	if c.PushIntervalMS <= 0 {
		c.PushIntervalMS = 1000
	}
	if c.HealthCheckInterval <= 0 {
		c.HealthCheckInterval = 20 * time.Second
	}
	if c.ReconnectDelay <= 0 {
		c.ReconnectDelay = 3 * time.Second
	}
	if c.MaxReconnectFails <= 0 {
		c.MaxReconnectFails = 5
	}
	if c.Logger == nil {
		c.Logger = log.New(os.Stderr, "", log.LstdFlags|log.LUTC)
	}
	if c.Factory == nil {
		c.Factory = defaultClientFactory
	}
	return c
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
	IntentNext       ControlIntent = "next"
	IntentPrevious   ControlIntent = "previous"

	IntentJumpNumberTag   ControlIntent = "jump_number_tag"
	IntentQuickSearchHold ControlIntent = "quick_search_hold"
	IntentJumpMode        ControlIntent = "jump_mode"

	IntentSetFavoritesQuickKeys  ControlIntent = "set_favorites_quick_keys"
	IntentSetSystemQuickKeys     ControlIntent = "set_system_quick_keys"
	IntentSetDepartmentQuickKeys ControlIntent = "set_department_quick_keys"
	IntentSetServiceTypes        ControlIntent = "set_service_types"

	IntentSetRecordOn  ControlIntent = "set_record_on"
	IntentSetRecordOff ControlIntent = "set_record_off"

	IntentAvoid      ControlIntent = "avoid"
	IntentUnavoid    ControlIntent = "unavoid"
	IntentSetVolume  ControlIntent = "set_volume"
	IntentSetSquelch ControlIntent = "set_squelch"

	IntentMenuEnter    ControlIntent = "menu_enter"
	IntentMenuSetValue ControlIntent = "menu_set_value"
	IntentMenuBack     ControlIntent = "menu_back"

	IntentAnalyzeStart       ControlIntent = "analyze_start"
	IntentAnalyzePauseResume ControlIntent = "analyze_pause_resume"
	IntentPushWaterfall      ControlIntent = "push_waterfall"
	IntentGetWaterfall       ControlIntent = "get_waterfall"

	IntentSetDateTime      ControlIntent = "set_date_time"
	IntentSetLocationRange ControlIntent = "set_location_range"
	IntentKeepAlive        ControlIntent = "keep_alive"
	IntentPowerOff         ControlIntent = "power_off"
)

func NewScannerSession(parent context.Context, cfg SessionConfig, hub *stateHub) (*ScannerSession, error) {
	cfg = cfg.withDefaults()
	if cfg.Scanner.IP == "" {
		return nil, errors.New("scanner address is required")
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
		Address:         s.cfg.Scanner.IP,
		ControlPort:     s.cfg.Scanner.ControlPort,
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
