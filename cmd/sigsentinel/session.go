package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jentfoo/SignalSentinel/internal/gui"
	"github.com/jentfoo/SignalSentinel/internal/sds200"
	"github.com/jentfoo/SignalSentinel/internal/store"
)

type SDS200Client interface {
	Model() (string, error)
	FirmwareVersion() (string, error)
	Resync() (sds200.RuntimeStatus, error)
	StartPushScannerInfo(intervalMS int) error
	Hold(tkw, x1, x2 string) error
	KeyPress(code string, mode sds200.KeyMode) error
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
	EnterMenu(menuID, index string) error
	MenuStatus() (sds200.XMLNode, error)
	MenuSetValue(value string) error
	MenuBack(retLevel string) error
	AnalyzeStart(mode string, params ...string) (sds200.CommandResponse, error)
	AnalyzePauseResume(mode string) error
	PushWaterfallFFT(fftType int, on bool) (sds200.CommandResponse, error)
	GetWaterfallFFT(fftType int, on bool) ([]int, error)
	GetDateTime() (sds200.DateTimeStatus, error)
	SetDateTime(daylightSaving int, t time.Time) error
	GetLocationRange() (sds200.LocationRange, error)
	SetLocationRange(lat, lon, rng string) error
	GetChargeStatus() (sds200.ChargeStatus, error)
	KeepAlive() error
	PowerOff() error
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
		c.PushIntervalMS = 200
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
	MenuID            string
	MenuIndex         string
	MenuValue         string
	MenuBackLevel     string
	AnalyzeMode       string
	AnalyzeParams     []string
	FFTType           int
	FFTEnabled        bool
	DaylightSaving    int
	DateTime          time.Time
	Latitude          string
	Longitude         string
	Range             string
	Confirmed         bool
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
	IntentMenuStatus   ControlIntent = "menu_status"
	IntentMenuSetValue ControlIntent = "menu_set_value"
	IntentMenuBack     ControlIntent = "menu_back"

	IntentAnalyzeStart       ControlIntent = "analyze_start"
	IntentAnalyzePauseResume ControlIntent = "analyze_pause_resume"
	IntentPushWaterfall      ControlIntent = "push_waterfall"
	IntentGetWaterfall       ControlIntent = "get_waterfall"

	IntentSetDateTime      ControlIntent = "set_date_time"
	IntentGetDateTime      ControlIntent = "get_date_time"
	IntentSetLocationRange ControlIntent = "set_location_range"
	IntentGetLocationRange ControlIntent = "get_location_range"
	IntentGetDeviceInfo    ControlIntent = "get_device_info"
	IntentGetModel         ControlIntent = "get_model"
	IntentGetFirmware      ControlIntent = "get_firmware"
	IntentGetChargeStatus  ControlIntent = "get_charge_status"
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
				log.Printf("session: scanner health check failed (%d): %v", consecutiveFails, err)
				delay := s.reconnectBackoff(consecutiveFails)
				select {
				case <-s.ctx.Done():
					return
				case <-time.After(delay):
				}
				continue
			}
			if consecutiveFails > 0 {
				log.Printf("session: scanner reconnected after %d failed attempts", consecutiveFails)
			}
			consecutiveFails = 0
		}
	}
}

func (s *ScannerSession) reconnectBackoff(consecutiveFails int) time.Duration {
	base := s.cfg.ReconnectDelay
	multiplier := consecutiveFails
	if multiplier > s.cfg.MaxReconnectFails {
		multiplier = s.cfg.MaxReconnectFails
	}
	return base * time.Duration(multiplier)
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
	s.refreshExpertTimeLocation(client)
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
		if client.TelemetrySnapshot().Hold {
			return nil // already held, don't toggle
		}
		return client.KeyPress("C", sds200.KeyModePress)
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
		avoidTarget, err := resolveAvoidTarget(client.TelemetrySnapshot().HoldTarget)
		if err != nil {
			return err
		}
		return client.Avoid(avoidTarget.Keyword, avoidTarget.Arg1, avoidTarget.Arg2, 2)
	case IntentUnavoid:
		avoidTarget, err := resolveAvoidTarget(client.TelemetrySnapshot().HoldTarget)
		if err != nil {
			return err
		}
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
	case IntentMenuEnter:
		menuID := strings.TrimSpace(params.MenuID)
		if menuID == "" {
			return errors.New("menu id is required")
		}
		return client.EnterMenu(menuID, strings.TrimSpace(params.MenuIndex))
	case IntentMenuStatus:
		node, err := client.MenuStatus()
		if err != nil {
			return err
		}
		s.updateExpertState(func(expert *ExpertRuntimeState) {
			expert.MenuStatusSummary = summarizeMenuStatus(node)
		})
		return nil
	case IntentMenuSetValue:
		value := strings.TrimSpace(params.MenuValue)
		if value == "" {
			return errors.New("menu value is required")
		}
		return client.MenuSetValue(value)
	case IntentMenuBack:
		level := strings.TrimSpace(params.MenuBackLevel)
		if level == "" {
			level = "RETURN_PREVOUS_MODE"
		}
		return client.MenuBack(level)
	case IntentAnalyzeStart:
		mode := strings.TrimSpace(params.AnalyzeMode)
		if mode == "" {
			return errors.New("analyze mode is required")
		}
		resp, err := client.AnalyzeStart(mode, params.AnalyzeParams...)
		if err != nil {
			return err
		}
		s.updateExpertState(func(expert *ExpertRuntimeState) {
			expert.AnalyzeSummary = summarizeCommandResponse(resp)
		})
		return nil
	case IntentAnalyzePauseResume:
		mode := strings.TrimSpace(params.AnalyzeMode)
		if mode == "" {
			return errors.New("analyze mode is required")
		}
		return client.AnalyzePauseResume(mode)
	case IntentPushWaterfall:
		if err := validateFFTType(params.FFTType); err != nil {
			return err
		}
		resp, err := client.PushWaterfallFFT(params.FFTType, params.FFTEnabled)
		if err != nil {
			return err
		}
		s.updateExpertState(func(expert *ExpertRuntimeState) {
			expert.WaterfallSummary = summarizeCommandResponse(resp)
		})
		return nil
	case IntentGetWaterfall:
		if err := validateFFTType(params.FFTType); err != nil {
			return err
		}
		values, err := client.GetWaterfallFFT(params.FFTType, params.FFTEnabled)
		if err != nil {
			return err
		}
		s.updateExpertState(func(expert *ExpertRuntimeState) {
			expert.WaterfallSummary = summarizeFFT(values)
		})
		return nil
	case IntentSetDateTime:
		if params.DaylightSaving != 0 && params.DaylightSaving != 1 {
			return fmt.Errorf("daylight saving must be 0 or 1 (got %d)", params.DaylightSaving)
		}
		if params.DateTime.IsZero() {
			return errors.New("date/time value is required")
		}
		if err := client.SetDateTime(params.DaylightSaving, params.DateTime); err != nil {
			return err
		}
		s.updateExpertState(func(expert *ExpertRuntimeState) {
			expert.DateTimeValue = params.DateTime.UTC()
			expert.DaylightSaving = params.DaylightSaving
			expert.HasDateTime = true
			expert.DateTimeSummary = fmt.Sprintf("set to %s (dst=%d)", expert.DateTimeValue.Format(time.RFC3339), params.DaylightSaving)
		})
		return nil
	case IntentGetDateTime:
		status, err := client.GetDateTime()
		if err != nil {
			return err
		}
		if status.Time.IsZero() {
			return errors.New("date/time response missing time")
		}
		s.updateExpertState(func(expert *ExpertRuntimeState) {
			applyDateTimeToExpert(expert, status)
		})
		return nil
	case IntentSetLocationRange:
		lat := strings.TrimSpace(params.Latitude)
		lon := strings.TrimSpace(params.Longitude)
		rng := strings.TrimSpace(params.Range)
		if err := validateLocationRange(lat, lon, rng); err != nil {
			return err
		}
		if err := client.SetLocationRange(lat, lon, rng); err != nil {
			return err
		}
		s.updateExpertState(func(expert *ExpertRuntimeState) {
			expert.Latitude = lat
			expert.Longitude = lon
			expert.Range = rng
			expert.LocationSummary = fmt.Sprintf("lat=%s lon=%s range=%s", lat, lon, rng)
		})
		return nil
	case IntentGetLocationRange:
		status, err := client.GetLocationRange()
		if err != nil {
			return err
		}
		if strings.TrimSpace(status.Latitude) == "" && strings.TrimSpace(status.Longitude) == "" && strings.TrimSpace(status.Range) == "" {
			return errors.New("location response is empty")
		}
		s.updateExpertState(func(expert *ExpertRuntimeState) {
			applyLocationToExpert(expert, status)
		})
		return nil
	case IntentGetDeviceInfo:
		model, err := client.Model()
		if err != nil {
			return err
		}
		version, err := client.FirmwareVersion()
		if err != nil {
			return err
		}
		s.updateExpertState(func(expert *ExpertRuntimeState) {
			expert.DeviceModel = strings.TrimSpace(model)
			expert.FirmwareVersion = strings.TrimSpace(version)
		})
		return nil
	case IntentGetModel:
		model, err := client.Model()
		if err != nil {
			return err
		}
		s.updateExpertState(func(expert *ExpertRuntimeState) {
			expert.DeviceModel = strings.TrimSpace(model)
		})
		return nil
	case IntentGetFirmware:
		version, err := client.FirmwareVersion()
		if err != nil {
			return err
		}
		s.updateExpertState(func(expert *ExpertRuntimeState) {
			expert.FirmwareVersion = strings.TrimSpace(version)
		})
		return nil
	case IntentGetChargeStatus:
		charge, err := client.GetChargeStatus()
		if err != nil {
			return err
		}
		s.updateExpertState(func(expert *ExpertRuntimeState) {
			expert.ChargeStatusSummary = summarizeChargeStatus(charge)
		})
		return nil
	case IntentKeepAlive:
		err := client.KeepAlive()
		s.updateExpertState(func(expert *ExpertRuntimeState) {
			if err != nil {
				expert.KeepAliveStatus = "failed: " + err.Error()
				return
			}
			expert.KeepAliveStatus = "ok at " + time.Now().UTC().Format(time.RFC3339)
		})
		return err
	case IntentPowerOff:
		if !params.Confirmed {
			return errors.New("power off requires explicit confirmation")
		}
		return client.PowerOff()
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

func (s *ScannerSession) resyncAfterScopeChange(client SDS200Client) error {
	status, err := client.Resync()
	if err != nil {
		return fmt.Errorf("scope change applied but telemetry resync failed: %w", err)
	}
	s.publishScannerState(status)
	return nil
}

func (s *ScannerSession) refreshExpertTimeLocation(client SDS200Client) {
	if client == nil {
		return
	}
	dt, dtErr := client.GetDateTime()
	if dtErr == nil && !dt.Time.IsZero() {
		s.updateExpertState(func(expert *ExpertRuntimeState) {
			applyDateTimeToExpert(expert, dt)
		})
	}
	loc, locErr := client.GetLocationRange()
	if locErr == nil &&
		(strings.TrimSpace(loc.Latitude) != "" || strings.TrimSpace(loc.Longitude) != "" || strings.TrimSpace(loc.Range) != "") {
		s.updateExpertState(func(expert *ExpertRuntimeState) {
			applyLocationToExpert(expert, loc)
		})
	}
}

func applyDateTimeToExpert(expert *ExpertRuntimeState, dt sds200.DateTimeStatus) {
	expert.DateTimeValue = dt.Time.UTC()
	expert.DaylightSaving = dt.DaylightSaving
	expert.HasDateTime = true
	expert.DateTimeSummary = fmt.Sprintf(
		"%s (dst=%d rtc=%d)",
		expert.DateTimeValue.Format(time.RFC3339),
		dt.DaylightSaving,
		dt.RTCStatus,
	)
}

func applyLocationToExpert(expert *ExpertRuntimeState, loc sds200.LocationRange) {
	expert.Latitude = strings.TrimSpace(loc.Latitude)
	expert.Longitude = strings.TrimSpace(loc.Longitude)
	expert.Range = strings.TrimSpace(loc.Range)
	expert.LocationSummary = fmt.Sprintf(
		"lat=%s lon=%s range=%s",
		expert.Latitude, expert.Longitude, expert.Range,
	)
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

func validateFFTType(value int) error {
	if value < 0 || value > 9 {
		return fmt.Errorf("fft type must be in range 0-9 (got %d)", value)
	}
	return nil
}

func validateLocationRange(lat, lon, rng string) error {
	latV, err := strconv.ParseFloat(lat, 64)
	if err != nil {
		return errors.New("latitude must be numeric")
	}
	lonV, err := strconv.ParseFloat(lon, 64)
	if err != nil {
		return errors.New("longitude must be numeric")
	}
	rngV, err := strconv.ParseFloat(rng, 64)
	if err != nil {
		return errors.New("range must be numeric")
	}
	if math.IsNaN(latV) || latV < -90 || latV > 90 {
		return errors.New("latitude must be in range -90 to 90")
	}
	if math.IsNaN(lonV) || lonV < -180 || lonV > 180 {
		return errors.New("longitude must be in range -180 to 180")
	}
	if math.IsNaN(rngV) || rngV <= 0 {
		return errors.New("range must be > 0")
	}
	return nil
}

func summarizeMenuStatus(node sds200.XMLNode) string {
	parts := make([]string, 0, 3)
	if name := strings.TrimSpace(node.Attrs["Name"]); name != "" {
		parts = append(parts, "name="+name)
	}
	itemNames := make([]string, 0, 3)
	for _, child := range node.Children {
		if !strings.EqualFold(child.XMLName.Local, "MenuItem") {
			continue
		}
		value := strings.TrimSpace(child.Attrs["Name"])
		if value == "" {
			value = strings.TrimSpace(child.Content)
		}
		if value == "" {
			continue
		}
		itemNames = append(itemNames, value)
		if len(itemNames) >= 3 {
			break
		}
	}
	if len(itemNames) > 0 {
		parts = append(parts, "items="+strings.Join(itemNames, " | "))
	}
	if len(parts) == 0 {
		return "menu status received"
	}
	return strings.Join(parts, " ; ")
}

func summarizeCommandResponse(resp sds200.CommandResponse) string {
	if len(resp.Fields) == 0 {
		return "ok"
	}
	return strings.Join(resp.Fields, ",")
}

func summarizeFFT(values []int) string {
	if len(values) == 0 {
		return "no fft data"
	}
	limit := len(values)
	if limit > 8 {
		limit = 8
	}
	head := make([]string, 0, limit)
	for i := 0; i < limit; i++ {
		head = append(head, strconv.Itoa(values[i]))
	}
	return fmt.Sprintf("%d bins: [%s]", len(values), strings.Join(head, ", "))
}

func summarizeChargeStatus(status sds200.ChargeStatus) string {
	return fmt.Sprintf(
		"status=%d cap=%d%% volt=%dmV curr=%dmA temp=%.2fC",
		status.Status,
		status.CapacityPct,
		status.VoltageMV,
		status.CurrentMA,
		status.TempC,
	)
}

func (s *ScannerSession) updateExpertState(update func(*ExpertRuntimeState)) {
	if s == nil || s.stateHub == nil || update == nil {
		return
	}
	state := s.stateHub.snapshot()
	update(&state.Expert)
	state.Expert.UpdatedAt = time.Now().UTC()
	s.stateHub.publish(state)
}

func (s *ScannerSession) publishScannerState(status sds200.RuntimeStatus) {
	if s.stateHub == nil {
		return
	}
	state := s.stateHub.snapshot()
	state.Scanner = status
	s.stateHub.publish(state)
}
