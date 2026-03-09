package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jentfoo/SignalSentinel/internal/gui"
	"github.com/jentfoo/SignalSentinel/internal/sds200"
	"github.com/jentfoo/SignalSentinel/internal/store"
)

type SDS200Client interface {
	Resync() (sds200.RuntimeStatus, error)
	StartPushScannerInfo(intervalMS int) error
	Hold(tkw, x1, x2 string) error
	Next(tkw, x1, x2 string, count int) error
	Previous(tkw, x1, x2 string, count int) error
	Avoid(tkw, x1, x2 string, status int) error
	JumpNumberTag(flTag, sysTag, chanTag int) error
	QuickSearchHold(freqHz int) error
	JumpMode(mode, index string) error
	GetFavoritesQuickKeys() (sds200.QuickKeyState, error)
	SetFavoritesQuickKeys(state sds200.QuickKeyState) error
	GetSystemQuickKeys(favQK int) (sds200.QuickKeyState, error)
	SetSystemQuickKeys(favQK int, state sds200.QuickKeyState) error
	GetDepartmentQuickKeys(favQK, sysQK int) (sds200.QuickKeyState, error)
	SetDepartmentQuickKeys(favQK, sysQK int, state sds200.QuickKeyState) error
	GetServiceTypes() ([]int, error)
	SetServiceTypes(values []int) error
	SetVolume(level int) error
	SetSquelch(level int) error
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
	if c.Factory == nil {
		c.Factory = defaultClientFactory
	}
	return c
}

type ScannerSession struct {
	cfg SessionConfig

	ctx    context.Context
	cancel context.CancelFunc

	mu     sync.RWMutex
	client SDS200Client

	fatalErr chan error
	wg       sync.WaitGroup
	closeMu  sync.Once
	stateHub *stateHub

	controlMu sync.Mutex
	controlCh chan controlRequest
}

type ControlIntent string

type ControlParams struct {
	FavoritesTag      int
	SystemTag         int
	ChannelTag        int
	FrequencyHz       int
	JumpMode          string
	JumpIndex         string
	ScopeFavoritesTag int
	ScopeSystemTag    int
	QuickKeyValues    []int
	ServiceTypes      []int
	Volume            int
	Squelch           int
}

type controlRequest struct {
	intent ControlIntent
	params ControlParams
	result chan error
}

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
		controlCh: make(chan controlRequest, 1),
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
	if s.controlCh == nil {
		return
	}
	req := controlRequest{intent: intent}
	s.controlMu.Lock()
	select {
	case stale := <-s.controlCh:
		if stale.result != nil {
			stale.result <- errors.New("control request superseded")
		}
	default:
	}
	select {
	case s.controlCh <- req:
	case <-s.ctx.Done():
	}
	s.controlMu.Unlock()
}

func (s *ScannerSession) ExecuteControl(intent ControlIntent, params ControlParams) error {
	if s == nil {
		return errors.New("scanner session unavailable")
	}
	if s.controlCh == nil {
		return errors.New("scanner session unavailable")
	}
	req := controlRequest{
		intent: intent,
		params: params,
		result: make(chan error, 1),
	}
	select {
	case s.controlCh <- req:
	case <-s.ctx.Done():
		return errors.New("scanner session unavailable")
	}
	select {
	case err := <-req.result:
		return err
	case <-s.ctx.Done():
		return errors.New("scanner session unavailable")
	}
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
				log.Printf("session: scanner health check failed (%d/%d): %v", consecutiveFails, s.cfg.MaxReconnectFails, err)
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
	s.mu.RLock()
	c := s.client
	s.mu.RUnlock()
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
	log.Printf("session: scanner session connected and synchronized")
	return nil
}

func (s *ScannerSession) controlLoop() {
	defer s.wg.Done()
	for {
		select {
		case <-s.ctx.Done():
			return
		case req := <-s.controlCh:
			err := s.executeIntent(req.intent, req.params)
			if err != nil {
				log.Printf("session: control intent %q failed: %v", req.intent, err)
			}
			if req.result != nil {
				req.result <- err
			}
		}
	}
}

func (s *ScannerSession) executeIntent(intent ControlIntent, params ControlParams) error {
	s.mu.RLock()
	client := s.client
	s.mu.RUnlock()
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
	case IntentNext:
		target, err := navigationTarget(client.TelemetrySnapshot())
		if err != nil {
			return err
		}
		return client.Next(target.Keyword, target.Arg1, target.Arg2, 1)
	case IntentPrevious:
		target, err := navigationTarget(client.TelemetrySnapshot())
		if err != nil {
			return err
		}
		return client.Previous(target.Keyword, target.Arg1, target.Arg2, 1)
	case IntentJumpNumberTag:
		if params.FavoritesTag < 0 || params.FavoritesTag > 99 {
			return fmt.Errorf("favorites tag must be in range 0-99 (got %d)", params.FavoritesTag)
		}
		if params.SystemTag < 0 || params.SystemTag > 99 {
			return fmt.Errorf("system tag must be in range 0-99 (got %d)", params.SystemTag)
		}
		if params.ChannelTag < 0 || params.ChannelTag > 999 {
			return fmt.Errorf("channel tag must be in range 0-999 (got %d)", params.ChannelTag)
		}
		return client.JumpNumberTag(params.FavoritesTag, params.SystemTag, params.ChannelTag)
	case IntentQuickSearchHold:
		if params.FrequencyHz <= 0 {
			return errors.New("quick search frequency must be > 0")
		}
		return client.QuickSearchHold(params.FrequencyHz)
	case IntentJumpMode:
		mode := strings.TrimSpace(params.JumpMode)
		if mode == "" {
			mode = "SCN_MODE"
		}
		index := strings.TrimSpace(params.JumpIndex)
		if index == "" {
			index = "0"
		}
		return client.JumpMode(mode, index)
	case IntentAvoid:
		status := client.TelemetrySnapshot()
		target := status.HoldTarget
		if target.Keyword == "" || target.Arg1 == "" {
			return errors.New("avoid target unavailable for current scanner state")
		}
		avoidTarget := toAvoidTarget(target)
		return client.Avoid(avoidTarget.Keyword, avoidTarget.Arg1, avoidTarget.Arg2, 2)
	case IntentUnavoid:
		status := client.TelemetrySnapshot()
		target := status.HoldTarget
		if target.Keyword == "" || target.Arg1 == "" {
			return errors.New("unavoid target unavailable for current scanner state")
		}
		avoidTarget := toAvoidTarget(target)
		return client.Avoid(avoidTarget.Keyword, avoidTarget.Arg1, avoidTarget.Arg2, 3)
	case IntentSetFavoritesQuickKeys:
		values, err := validateQuickKeyValues(params.QuickKeyValues, 100, "favorites quick keys")
		if err != nil {
			return err
		}
		state := quickKeyValuesToState(values)
		if err := client.SetFavoritesQuickKeys(state); err != nil {
			return err
		}
		return s.resyncAfterScopeChange(client)
	case IntentSetSystemQuickKeys:
		if err := validateQuickKeyTag("favorites quick key", params.ScopeFavoritesTag); err != nil {
			return err
		}
		values, err := validateQuickKeyValues(params.QuickKeyValues, 100, "system quick keys")
		if err != nil {
			return err
		}
		state := quickKeyValuesToState(values)
		if err := client.SetSystemQuickKeys(params.ScopeFavoritesTag, state); err != nil {
			return err
		}
		return s.resyncAfterScopeChange(client)
	case IntentSetDepartmentQuickKeys:
		if err := validateQuickKeyTag("favorites quick key", params.ScopeFavoritesTag); err != nil {
			return err
		}
		if err := validateQuickKeyTag("system quick key", params.ScopeSystemTag); err != nil {
			return err
		}
		values, err := validateQuickKeyValues(params.QuickKeyValues, 100, "department quick keys")
		if err != nil {
			return err
		}
		state := quickKeyValuesToState(values)
		if err := client.SetDepartmentQuickKeys(params.ScopeFavoritesTag, params.ScopeSystemTag, state); err != nil {
			return err
		}
		return s.resyncAfterScopeChange(client)
	case IntentSetServiceTypes:
		values, err := validateBinaryValues(params.ServiceTypes, 47, "service types")
		if err != nil {
			return err
		}
		if err := client.SetServiceTypes(values); err != nil {
			return err
		}
		return s.resyncAfterScopeChange(client)
	case IntentSetVolume:
		return client.SetVolume(params.Volume)
	case IntentSetSquelch:
		return client.SetSquelch(params.Squelch)
	default:
		return fmt.Errorf("unsupported control intent: %s", intent)
	}
}

func (s *ScannerSession) ReadScanScope(favoritesTag, systemTag int) (gui.ScanScopeSnapshot, error) {
	if s == nil {
		return gui.ScanScopeSnapshot{}, errors.New("scanner session unavailable")
	}
	if err := validateQuickKeyTag("favorites quick key", favoritesTag); err != nil {
		return gui.ScanScopeSnapshot{}, err
	}
	if err := validateQuickKeyTag("system quick key", systemTag); err != nil {
		return gui.ScanScopeSnapshot{}, err
	}

	s.mu.RLock()
	client := s.client
	s.mu.RUnlock()
	if client == nil {
		return gui.ScanScopeSnapshot{}, errors.New("scanner client unavailable")
	}

	favoritesState, err := client.GetFavoritesQuickKeys()
	if err != nil {
		return gui.ScanScopeSnapshot{}, err
	}
	systemState, err := client.GetSystemQuickKeys(favoritesTag)
	if err != nil {
		return gui.ScanScopeSnapshot{}, err
	}
	departmentState, err := client.GetDepartmentQuickKeys(favoritesTag, systemTag)
	if err != nil {
		return gui.ScanScopeSnapshot{}, err
	}
	serviceTypes, err := client.GetServiceTypes()
	if err != nil {
		return gui.ScanScopeSnapshot{}, err
	}
	serviceTypesCopy := append([]int(nil), serviceTypes...)

	return gui.ScanScopeSnapshot{
		FavoritesTag:        favoritesTag,
		SystemTag:           systemTag,
		FavoritesQuickKeys:  quickKeyStateToValues(favoritesState),
		SystemQuickKeys:     quickKeyStateToValues(systemState),
		DepartmentQuickKeys: quickKeyStateToValues(departmentState),
		ServiceTypes:        serviceTypesCopy,
	}, nil
}

func navigationTarget(status sds200.RuntimeStatus) (sds200.HoldTarget, error) {
	target := status.HoldTarget
	if target.Keyword == "" || target.Arg1 == "" {
		return sds200.HoldTarget{}, errors.New("navigation target unavailable for current scanner state")
	}
	return target, nil
}

func toAvoidTarget(target sds200.HoldTarget) sds200.HoldTarget {
	switch target.Keyword {
	case "SWS_FREQ", "CS_FREQ", "QS_FREQ":
		target.Keyword = "AFREQ"
		target.Arg2 = ""
	case "TGID":
		target.Keyword = "ATGID"
		target.Arg2 = target.SystemIndex
	case "DEPT":
		target.Arg2 = ""
	}
	return target
}

func (s *ScannerSession) resyncAfterScopeChange(client SDS200Client) error {
	status, err := client.Resync()
	if err != nil {
		return fmt.Errorf("scope change applied but telemetry resync failed: %w", err)
	}
	s.publishScannerState(status)
	return nil
}

func validateQuickKeyTag(name string, value int) error {
	if value < 0 || value > 99 {
		return fmt.Errorf("%s must be in range 0-99 (got %d)", name, value)
	}
	return nil
}

func validateQuickKeyValues(values []int, expected int, name string) ([]int, error) {
	if len(values) != expected {
		return nil, fmt.Errorf("%s must contain %d values (got %d)", name, expected, len(values))
	}
	out := make([]int, expected)
	for i, value := range values {
		if value != 0 && value != 1 && value != 2 {
			return nil, fmt.Errorf("%s[%d] must be 0, 1, or 2 (got %d)", name, i, value)
		}
		out[i] = value
	}
	return out, nil
}

func validateBinaryValues(values []int, expected int, name string) ([]int, error) {
	if len(values) != expected {
		return nil, fmt.Errorf("%s must contain %d values (got %d)", name, expected, len(values))
	}
	out := make([]int, expected)
	for i, value := range values {
		if value != 0 && value != 1 {
			return nil, fmt.Errorf("%s[%d] must be 0 or 1 (got %d)", name, i, value)
		}
		out[i] = value
	}
	return out, nil
}

func quickKeyValuesToState(values []int) sds200.QuickKeyState {
	var state sds200.QuickKeyState
	copy(state[:], values)
	return state
}

func quickKeyStateToValues(state sds200.QuickKeyState) []int {
	return slices.Clone(state[:])
}

func (s *ScannerSession) publishScannerState(status sds200.RuntimeStatus) {
	if s.stateHub == nil {
		return
	}
	s.stateHub.publish(RuntimeState{Scanner: status})
}

func (s *ScannerSession) signalFatal(err error) {
	log.Printf("session: hard fault: %v", err)
	select {
	case s.fatalErr <- err:
	default:
	}
	s.cancel()
}
