package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jentfoo/SignalSentinel/internal/sds200"
	"github.com/jentfoo/SignalSentinel/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeSDS200Client struct {
	mu sync.Mutex

	resyncStatus  sds200.RuntimeStatus
	resyncErr     error
	startPushErr  error
	holdErr       error
	nextErr       error
	previousErr   error
	avoidErr      error
	jumpNumErr    error
	quickSHoldErr error
	jumpErr       error
	getFQKErr     error
	setFQKErr     error
	getSQKErr     error
	setSQKErr     error
	getDQKErr     error
	setDQKErr     error
	getSVCErr     error
	setSVCErr     error
	setVolumeErr  error
	setSquelchErr error
	closeErr      error

	telemetrySnapshot   sds200.RuntimeStatus
	onTelemetry         func(sds200.RuntimeStatus)
	favoritesQuickKeys  sds200.QuickKeyState
	systemQuickKeys     sds200.QuickKeyState
	departmentQuickKeys sds200.QuickKeyState
	serviceTypes        []int

	resyncCalls     int
	startPushCalls  []int
	holdCalls       []holdCall
	nextCalls       []navCall
	previousCalls   []navCall
	avoidCalls      []avoidCall
	jumpNumCalls    []jumpNumberCall
	quickSHold      []int
	jumpCalls       []jumpCall
	getSQKCalls     []int
	getDQKCalls     []quickKeyTarget
	setFQKCalls     []sds200.QuickKeyState
	setSQKCalls     []quickKeyWrite
	setDQKCalls     []quickKeyWrite
	setSVCCalls     [][]int
	setVolumeCalls  []int
	setSquelchCalls []int
	closeCalls      int

	holdSignal  chan struct{}
	avoidSignal chan struct{}
	jumpSignal  chan struct{}
}

type holdCall struct {
	keyword string
	arg1    string
	arg2    string
}

type jumpCall struct {
	mode  string
	index string
}

type navCall struct {
	keyword string
	arg1    string
	arg2    string
	count   int
}

type avoidCall struct {
	keyword string
	arg1    string
	arg2    string
	status  int
}

type jumpNumberCall struct {
	fl    int
	sys   int
	chanN int
}

type quickKeyTarget struct {
	fav int
	sys int
}

type quickKeyWrite struct {
	target quickKeyTarget
	state  sds200.QuickKeyState
}

type blockingFavoritesClient struct {
	*fakeSDS200Client
	getFavoritesStarted chan struct{}
	releaseFavorites    chan struct{}
}

func (b *blockingFavoritesClient) GetFavoritesQuickKeys() (sds200.QuickKeyState, error) {
	if b.getFavoritesStarted != nil {
		select {
		case b.getFavoritesStarted <- struct{}{}:
		default:
		}
	}
	if b.releaseFavorites != nil {
		<-b.releaseFavorites
	}
	return b.fakeSDS200Client.GetFavoritesQuickKeys()
}

func (f *fakeSDS200Client) Resync() (sds200.RuntimeStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resyncCalls++
	return f.resyncStatus, f.resyncErr
}

func (f *fakeSDS200Client) StartPushScannerInfo(intervalMS int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.startPushCalls = append(f.startPushCalls, intervalMS)
	return f.startPushErr
}

func (f *fakeSDS200Client) Hold(tkw, x1, x2 string) error {
	f.mu.Lock()
	f.holdCalls = append(f.holdCalls, holdCall{keyword: tkw, arg1: x1, arg2: x2})
	signal := f.holdSignal
	err := f.holdErr
	f.mu.Unlock()

	if signal != nil {
		select {
		case signal <- struct{}{}:
		default:
		}
	}
	return err
}

func (f *fakeSDS200Client) Next(tkw, x1, x2 string, count int) error {
	f.mu.Lock()
	f.nextCalls = append(f.nextCalls, navCall{keyword: tkw, arg1: x1, arg2: x2, count: count})
	err := f.nextErr
	f.mu.Unlock()
	return err
}

func (f *fakeSDS200Client) Previous(tkw, x1, x2 string, count int) error {
	f.mu.Lock()
	f.previousCalls = append(f.previousCalls, navCall{keyword: tkw, arg1: x1, arg2: x2, count: count})
	err := f.previousErr
	f.mu.Unlock()
	return err
}

func (f *fakeSDS200Client) Avoid(tkw, x1, x2 string, status int) error {
	f.mu.Lock()
	f.avoidCalls = append(f.avoidCalls, avoidCall{keyword: tkw, arg1: x1, arg2: x2, status: status})
	signal := f.avoidSignal
	err := f.avoidErr
	f.mu.Unlock()

	if signal != nil {
		select {
		case signal <- struct{}{}:
		default:
		}
	}
	return err
}

func (f *fakeSDS200Client) JumpNumberTag(flTag, sysTag, chanTag int) error {
	f.mu.Lock()
	f.jumpNumCalls = append(f.jumpNumCalls, jumpNumberCall{fl: flTag, sys: sysTag, chanN: chanTag})
	err := f.jumpNumErr
	f.mu.Unlock()
	return err
}

func (f *fakeSDS200Client) QuickSearchHold(freqHz int) error {
	f.mu.Lock()
	f.quickSHold = append(f.quickSHold, freqHz)
	err := f.quickSHoldErr
	f.mu.Unlock()
	return err
}

func (f *fakeSDS200Client) JumpMode(mode, index string) error {
	f.mu.Lock()
	f.jumpCalls = append(f.jumpCalls, jumpCall{mode: mode, index: index})
	signal := f.jumpSignal
	err := f.jumpErr
	f.mu.Unlock()

	if signal != nil {
		select {
		case signal <- struct{}{}:
		default:
		}
	}
	return err
}

func (f *fakeSDS200Client) GetFavoritesQuickKeys() (sds200.QuickKeyState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.favoritesQuickKeys, f.getFQKErr
}

func (f *fakeSDS200Client) SetFavoritesQuickKeys(state sds200.QuickKeyState) error {
	f.mu.Lock()
	f.setFQKCalls = append(f.setFQKCalls, cloneQuickKeyState(state))
	err := f.setFQKErr
	f.mu.Unlock()
	return err
}

func (f *fakeSDS200Client) GetSystemQuickKeys(favQK int) (sds200.QuickKeyState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getSQKCalls = append(f.getSQKCalls, favQK)
	return f.systemQuickKeys, f.getSQKErr
}

func (f *fakeSDS200Client) SetSystemQuickKeys(favQK int, state sds200.QuickKeyState) error {
	f.mu.Lock()
	f.setSQKCalls = append(f.setSQKCalls, quickKeyWrite{
		target: quickKeyTarget{fav: favQK},
		state:  cloneQuickKeyState(state),
	})
	err := f.setSQKErr
	f.mu.Unlock()
	return err
}

func (f *fakeSDS200Client) GetDepartmentQuickKeys(favQK, sysQK int) (sds200.QuickKeyState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getDQKCalls = append(f.getDQKCalls, quickKeyTarget{fav: favQK, sys: sysQK})
	return f.departmentQuickKeys, f.getDQKErr
}

func (f *fakeSDS200Client) SetDepartmentQuickKeys(favQK, sysQK int, state sds200.QuickKeyState) error {
	f.mu.Lock()
	f.setDQKCalls = append(f.setDQKCalls, quickKeyWrite{
		target: quickKeyTarget{fav: favQK, sys: sysQK},
		state:  cloneQuickKeyState(state),
	})
	err := f.setDQKErr
	f.mu.Unlock()
	return err
}

func (f *fakeSDS200Client) GetServiceTypes() ([]int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int(nil), f.serviceTypes...), f.getSVCErr
}

func (f *fakeSDS200Client) SetServiceTypes(values []int) error {
	f.mu.Lock()
	f.setSVCCalls = append(f.setSVCCalls, append([]int(nil), values...))
	err := f.setSVCErr
	f.mu.Unlock()
	return err
}

func (f *fakeSDS200Client) SetVolume(level int) error {
	f.mu.Lock()
	f.setVolumeCalls = append(f.setVolumeCalls, level)
	err := f.setVolumeErr
	f.mu.Unlock()
	return err
}

func (f *fakeSDS200Client) SetSquelch(level int) error {
	f.mu.Lock()
	f.setSquelchCalls = append(f.setSquelchCalls, level)
	err := f.setSquelchErr
	f.mu.Unlock()
	return err
}

func (f *fakeSDS200Client) OnTelemetry(handler func(sds200.RuntimeStatus)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.onTelemetry = handler
}

func (f *fakeSDS200Client) TelemetrySnapshot() sds200.RuntimeStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.telemetrySnapshot
}

func (f *fakeSDS200Client) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closeCalls++
	return f.closeErr
}

func (f *fakeSDS200Client) snapshot() fakeClientSnapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	return fakeClientSnapshot{
		resyncCalls:     f.resyncCalls,
		startPushCalls:  append([]int(nil), f.startPushCalls...),
		holdCalls:       append([]holdCall(nil), f.holdCalls...),
		nextCalls:       append([]navCall(nil), f.nextCalls...),
		previousCalls:   append([]navCall(nil), f.previousCalls...),
		avoidCalls:      append([]avoidCall(nil), f.avoidCalls...),
		jumpNumCalls:    append([]jumpNumberCall(nil), f.jumpNumCalls...),
		quickSHold:      append([]int(nil), f.quickSHold...),
		jumpCalls:       append([]jumpCall(nil), f.jumpCalls...),
		getSQKCalls:     append([]int(nil), f.getSQKCalls...),
		getDQKCalls:     append([]quickKeyTarget(nil), f.getDQKCalls...),
		setFQKCalls:     append([]sds200.QuickKeyState(nil), f.setFQKCalls...),
		setSQKCalls:     append([]quickKeyWrite(nil), f.setSQKCalls...),
		setDQKCalls:     append([]quickKeyWrite(nil), f.setDQKCalls...),
		setSVCCalls:     cloneNestedInts(f.setSVCCalls),
		setVolumeCalls:  append([]int(nil), f.setVolumeCalls...),
		setSquelchCalls: append([]int(nil), f.setSquelchCalls...),
		closeCalls:      f.closeCalls,
		hasTelemetry:    f.onTelemetry != nil,
	}
}

type fakeClientSnapshot struct {
	resyncCalls     int
	startPushCalls  []int
	holdCalls       []holdCall
	nextCalls       []navCall
	previousCalls   []navCall
	avoidCalls      []avoidCall
	jumpNumCalls    []jumpNumberCall
	quickSHold      []int
	jumpCalls       []jumpCall
	getSQKCalls     []int
	getDQKCalls     []quickKeyTarget
	setFQKCalls     []sds200.QuickKeyState
	setSQKCalls     []quickKeyWrite
	setDQKCalls     []quickKeyWrite
	setSVCCalls     [][]int
	setVolumeCalls  []int
	setSquelchCalls []int
	closeCalls      int
	hasTelemetry    bool
}

func cloneQuickKeyState(in sds200.QuickKeyState) sds200.QuickKeyState {
	var out sds200.QuickKeyState
	copy(out[:], in[:])
	return out
}

func cloneNestedInts(values [][]int) [][]int {
	out := make([][]int, len(values))
	for i := range values {
		out[i] = append([]int(nil), values[i]...)
	}
	return out
}

func TestSessionConfigWithDefaults(t *testing.T) {
	t.Parallel()

	t.Run("applies_defaults", func(t *testing.T) {
		cfg := SessionConfig{}.withDefaults()
		assert.Equal(t, sds200.DefaultControlPort, cfg.Scanner.ControlPort)
		assert.Equal(t, 2*time.Second, cfg.ResponseTimeout)
		assert.Equal(t, 3, cfg.Retries)
		assert.Equal(t, 2*time.Second, cfg.ReadTimeout)
		assert.Equal(t, 2*time.Second, cfg.WriteTimeout)
		assert.Equal(t, 1000, cfg.PushIntervalMS)
		assert.Equal(t, 20*time.Second, cfg.HealthCheckInterval)
		assert.Equal(t, 3*time.Second, cfg.ReconnectDelay)
		assert.Equal(t, 5, cfg.MaxReconnectFails)
		require.NotNil(t, cfg.Factory)
	})
}

func TestNewScannerSession(t *testing.T) {
	t.Parallel()

	t.Run("requires_scanner_address", func(t *testing.T) {
		session, err := NewScannerSession(t.Context(), SessionConfig{Factory: func(cfg sds200.ClientConfig) (SDS200Client, error) {
			return &fakeSDS200Client{}, nil
		}}, newStateHub())
		require.Error(t, err)
		assert.Nil(t, session)
		assert.Equal(t, "scanner address is required", err.Error())
	})

	t.Run("connects_and_starts_push", func(t *testing.T) {
		client := &fakeSDS200Client{resyncStatus: sds200.RuntimeStatus{Connected: true, Channel: "ops"}}
		hub := newStateHub()
		session, err := NewScannerSession(t.Context(), SessionConfig{
			Scanner:        store.ScannerConfig{IP: "127.0.0.1", ControlPort: sds200.DefaultControlPort},
			PushIntervalMS: 1200,
			Factory: func(cfg sds200.ClientConfig) (SDS200Client, error) {
				return client, nil
			},
		}, hub)
		require.NoError(t, err)
		defer func() { _ = session.Close() }()

		snap := client.snapshot()
		assert.Equal(t, 1, snap.resyncCalls)
		assert.Equal(t, []int{1200}, snap.startPushCalls)
		assert.True(t, snap.hasTelemetry)

		state := hub.snapshot()
		assert.True(t, state.Scanner.Connected)
		assert.Equal(t, "ops", state.Scanner.Channel)
	})
}

func TestScannerSessionExecuteIntent(t *testing.T) {
	t.Parallel()

	t.Run("requires_client_connection", func(t *testing.T) {
		session := &ScannerSession{}
		err := session.executeIntent(IntentHold, ControlParams{})
		require.Error(t, err)
		assert.Equal(t, "scanner client unavailable", err.Error())
	})

	t.Run("resume_calls_jump_mode", func(t *testing.T) {
		client := &fakeSDS200Client{}
		session := &ScannerSession{client: client}

		require.NoError(t, session.executeIntent(IntentResumeScan, ControlParams{}))

		snap := client.snapshot()
		require.Len(t, snap.jumpCalls, 1)
		assert.Equal(t, "SCN_MODE", snap.jumpCalls[0].mode)
		assert.Equal(t, "0", snap.jumpCalls[0].index)
	})

	t.Run("hold_calls_hold_target", func(t *testing.T) {
		client := &fakeSDS200Client{
			telemetrySnapshot: sds200.RuntimeStatus{
				HoldTarget: sds200.HoldTarget{Keyword: "TGID", Arg1: "100", Arg2: "2"},
			},
		}
		session := &ScannerSession{client: client}

		require.NoError(t, session.executeIntent(IntentHold, ControlParams{}))

		snap := client.snapshot()
		require.Len(t, snap.holdCalls, 1)
		assert.Equal(t, "TGID", snap.holdCalls[0].keyword)
		assert.Equal(t, "100", snap.holdCalls[0].arg1)
		assert.Equal(t, "2", snap.holdCalls[0].arg2)
	})

	t.Run("hold_requires_hold_target", func(t *testing.T) {
		client := &fakeSDS200Client{}
		session := &ScannerSession{client: client}

		err := session.executeIntent(IntentHold, ControlParams{})
		require.Error(t, err)
		assert.Equal(t, "hold target unavailable for current scanner state", err.Error())
	})

	t.Run("avoid_calls_avoid_target", func(t *testing.T) {
		client := &fakeSDS200Client{
			telemetrySnapshot: sds200.RuntimeStatus{
				HoldTarget: sds200.HoldTarget{Keyword: "TGID", Arg1: "100", Arg2: "2", SystemIndex: "5"},
			},
		}
		session := &ScannerSession{client: client}

		require.NoError(t, session.executeIntent(IntentAvoid, ControlParams{}))

		snap := client.snapshot()
		require.Len(t, snap.avoidCalls, 1)
		assert.Equal(t, "ATGID", snap.avoidCalls[0].keyword)
		assert.Equal(t, "100", snap.avoidCalls[0].arg1)
		assert.Equal(t, "5", snap.avoidCalls[0].arg2)
		assert.Equal(t, 2, snap.avoidCalls[0].status)
	})

	t.Run("avoid_maps_search_freq", func(t *testing.T) {
		client := &fakeSDS200Client{
			telemetrySnapshot: sds200.RuntimeStatus{
				HoldTarget: sds200.HoldTarget{Keyword: "SWS_FREQ", Arg1: "4060000", Arg2: "15"},
			},
		}
		session := &ScannerSession{client: client}

		require.NoError(t, session.executeIntent(IntentAvoid, ControlParams{}))

		snap := client.snapshot()
		require.Len(t, snap.avoidCalls, 1)
		assert.Equal(t, "AFREQ", snap.avoidCalls[0].keyword)
		assert.Equal(t, "4060000", snap.avoidCalls[0].arg1)
		assert.Empty(t, snap.avoidCalls[0].arg2)
		assert.Equal(t, 2, snap.avoidCalls[0].status)
	})

	t.Run("avoid_requires_target", func(t *testing.T) {
		client := &fakeSDS200Client{}
		session := &ScannerSession{client: client}

		err := session.executeIntent(IntentAvoid, ControlParams{})
		require.Error(t, err)
		assert.Equal(t, "avoid target unavailable for current scanner state", err.Error())
	})

	t.Run("avoid_requires_tgid_parent_system_index", func(t *testing.T) {
		client := &fakeSDS200Client{
			telemetrySnapshot: sds200.RuntimeStatus{
				HoldTarget: sds200.HoldTarget{Keyword: "TGID", Arg1: "100", Arg2: "2"},
			},
		}
		session := &ScannerSession{client: client}

		err := session.executeIntent(IntentAvoid, ControlParams{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing parent system index")
		assert.Empty(t, client.snapshot().avoidCalls)
	})

	t.Run("avoid_rejects_unsupported_hold_target", func(t *testing.T) {
		client := &fakeSDS200Client{
			telemetrySnapshot: sds200.RuntimeStatus{
				HoldTarget: sds200.HoldTarget{Keyword: "WX", Arg1: "2"},
			},
		}
		session := &ScannerSession{client: client}

		err := session.executeIntent(IntentAvoid, ControlParams{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported")
		assert.Empty(t, client.snapshot().avoidCalls)
	})

	t.Run("avoid_rejects_unknown_hold_target_keyword", func(t *testing.T) {
		client := &fakeSDS200Client{
			telemetrySnapshot: sds200.RuntimeStatus{
				HoldTarget: sds200.HoldTarget{Keyword: "mystery_mode", Arg1: "2"},
			},
		}
		session := &ScannerSession{client: client}

		err := session.executeIntent(IntentAvoid, ControlParams{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported")
		assert.Empty(t, client.snapshot().avoidCalls)
	})

	t.Run("unavoid_calls_target", func(t *testing.T) {
		client := &fakeSDS200Client{
			telemetrySnapshot: sds200.RuntimeStatus{
				HoldTarget: sds200.HoldTarget{Keyword: "TGID", Arg1: "100", Arg2: "2", SystemIndex: "5"},
			},
		}
		session := &ScannerSession{client: client}

		require.NoError(t, session.executeIntent(IntentUnavoid, ControlParams{}))

		snap := client.snapshot()
		require.Len(t, snap.avoidCalls, 1)
		assert.Equal(t, "ATGID", snap.avoidCalls[0].keyword)
		assert.Equal(t, "100", snap.avoidCalls[0].arg1)
		assert.Equal(t, "5", snap.avoidCalls[0].arg2)
		assert.Equal(t, 3, snap.avoidCalls[0].status)
	})

	t.Run("next_calls_next_target", func(t *testing.T) {
		client := &fakeSDS200Client{
			telemetrySnapshot: sds200.RuntimeStatus{
				HoldTarget: sds200.HoldTarget{Keyword: "CFREQ", Arg1: "120"},
			},
		}
		session := &ScannerSession{client: client}

		require.NoError(t, session.executeIntent(IntentNext, ControlParams{}))

		snap := client.snapshot()
		require.Len(t, snap.nextCalls, 1)
		assert.Equal(t, "CFREQ", snap.nextCalls[0].keyword)
		assert.Equal(t, "120", snap.nextCalls[0].arg1)
		assert.Equal(t, 1, snap.nextCalls[0].count)
	})

	t.Run("previous_calls_previous_target", func(t *testing.T) {
		client := &fakeSDS200Client{
			telemetrySnapshot: sds200.RuntimeStatus{
				HoldTarget: sds200.HoldTarget{Keyword: "TGID", Arg1: "100", Arg2: "2"},
			},
		}
		session := &ScannerSession{client: client}

		require.NoError(t, session.executeIntent(IntentPrevious, ControlParams{}))

		snap := client.snapshot()
		require.Len(t, snap.previousCalls, 1)
		assert.Equal(t, "TGID", snap.previousCalls[0].keyword)
		assert.Equal(t, "100", snap.previousCalls[0].arg1)
		assert.Equal(t, "2", snap.previousCalls[0].arg2)
		assert.Equal(t, 1, snap.previousCalls[0].count)
	})

	t.Run("next_requires_navigation_target", func(t *testing.T) {
		client := &fakeSDS200Client{}
		session := &ScannerSession{client: client}

		err := session.executeIntent(IntentNext, ControlParams{})
		require.Error(t, err)
		assert.Equal(t, "navigation target unavailable for current scanner state", err.Error())
	})

	t.Run("jump_number_tag_calls_client", func(t *testing.T) {
		client := &fakeSDS200Client{}
		session := &ScannerSession{client: client}

		err := session.executeIntent(IntentJumpNumberTag, ControlParams{FavoritesTag: 1, SystemTag: 2, ChannelTag: 3})
		require.NoError(t, err)

		snap := client.snapshot()
		require.Len(t, snap.jumpNumCalls, 1)
		assert.Equal(t, 1, snap.jumpNumCalls[0].fl)
		assert.Equal(t, 2, snap.jumpNumCalls[0].sys)
		assert.Equal(t, 3, snap.jumpNumCalls[0].chanN)
	})

	t.Run("jump_tag_invalid_favorites", func(t *testing.T) {
		client := &fakeSDS200Client{}
		session := &ScannerSession{client: client}

		err := session.executeIntent(IntentJumpNumberTag, ControlParams{FavoritesTag: -1, SystemTag: 2, ChannelTag: 3})
		require.Error(t, err)
		assert.Equal(t, "favorites tag must be in range 0-99 (got -1)", err.Error())
	})

	t.Run("jump_tag_invalid_system", func(t *testing.T) {
		client := &fakeSDS200Client{}
		session := &ScannerSession{client: client}

		err := session.executeIntent(IntentJumpNumberTag, ControlParams{FavoritesTag: 1, SystemTag: 100, ChannelTag: 3})
		require.Error(t, err)
		assert.Equal(t, "system tag must be in range 0-99 (got 100)", err.Error())
	})

	t.Run("jump_tag_invalid_channel", func(t *testing.T) {
		client := &fakeSDS200Client{}
		session := &ScannerSession{client: client}

		err := session.executeIntent(IntentJumpNumberTag, ControlParams{FavoritesTag: 1, SystemTag: 2, ChannelTag: 1000})
		require.Error(t, err)
		assert.Equal(t, "channel tag must be in range 0-999 (got 1000)", err.Error())
	})

	t.Run("quick_search_hold_calls_client", func(t *testing.T) {
		client := &fakeSDS200Client{}
		session := &ScannerSession{client: client}

		err := session.executeIntent(IntentQuickSearchHold, ControlParams{FrequencyHz: 4060000})
		require.NoError(t, err)

		snap := client.snapshot()
		assert.Equal(t, []int{4060000}, snap.quickSHold)
	})

	t.Run("quick_search_hold_validates_frequency", func(t *testing.T) {
		client := &fakeSDS200Client{}
		session := &ScannerSession{client: client}

		err := session.executeIntent(IntentQuickSearchHold, ControlParams{FrequencyHz: 0})
		require.Error(t, err)
		assert.Equal(t, "quick search frequency must be > 0", err.Error())
	})

	t.Run("jump_mode_explicit_values", func(t *testing.T) {
		client := &fakeSDS200Client{}
		session := &ScannerSession{client: client}

		err := session.executeIntent(IntentJumpMode, ControlParams{JumpMode: "WX_MODE", JumpIndex: "NORMAL"})
		require.NoError(t, err)

		snap := client.snapshot()
		require.Len(t, snap.jumpCalls, 1)
		assert.Equal(t, "WX_MODE", snap.jumpCalls[0].mode)
		assert.Equal(t, "NORMAL", snap.jumpCalls[0].index)
	})

	t.Run("jump_mode_defaults_empty", func(t *testing.T) {
		client := &fakeSDS200Client{}
		session := &ScannerSession{client: client}

		err := session.executeIntent(IntentJumpMode, ControlParams{})
		require.NoError(t, err)

		snap := client.snapshot()
		require.Len(t, snap.jumpCalls, 1)
		assert.Equal(t, "SCN_MODE", snap.jumpCalls[0].mode)
		assert.Equal(t, "0", snap.jumpCalls[0].index)
	})

	t.Run("set_favorites_resyncs", func(t *testing.T) {
		client := &fakeSDS200Client{}
		session := &ScannerSession{client: client}
		values := enabledValues(100, 2)

		err := session.executeIntent(IntentSetFavoritesQuickKeys, ControlParams{QuickKeyValues: values})
		require.NoError(t, err)

		snap := client.snapshot()
		require.Len(t, snap.setFQKCalls, 1)
		assert.Equal(t, values[0], snap.setFQKCalls[0][0])
		assert.Equal(t, values[25], snap.setFQKCalls[0][25])
		assert.Equal(t, values[99], snap.setFQKCalls[0][99])
		assert.Equal(t, 1, snap.resyncCalls)
	})

	t.Run("set_system_resyncs", func(t *testing.T) {
		client := &fakeSDS200Client{}
		session := &ScannerSession{client: client}
		values := enabledValues(100, 3)

		err := session.executeIntent(IntentSetSystemQuickKeys, ControlParams{
			ScopeFavoritesTag: 4,
			QuickKeyValues:    values,
		})
		require.NoError(t, err)

		snap := client.snapshot()
		require.Len(t, snap.setSQKCalls, 1)
		assert.Equal(t, 4, snap.setSQKCalls[0].target.fav)
		assert.Equal(t, values[4], snap.setSQKCalls[0].state[4])
		assert.Equal(t, 1, snap.resyncCalls)
	})

	t.Run("set_department_resyncs", func(t *testing.T) {
		client := &fakeSDS200Client{}
		session := &ScannerSession{client: client}
		values := enabledValues(100, 5)

		err := session.executeIntent(IntentSetDepartmentQuickKeys, ControlParams{
			ScopeFavoritesTag: 4,
			ScopeSystemTag:    9,
			QuickKeyValues:    values,
		})
		require.NoError(t, err)

		snap := client.snapshot()
		require.Len(t, snap.setDQKCalls, 1)
		assert.Equal(t, 4, snap.setDQKCalls[0].target.fav)
		assert.Equal(t, 9, snap.setDQKCalls[0].target.sys)
		assert.Equal(t, values[9], snap.setDQKCalls[0].state[9])
		assert.Equal(t, 1, snap.resyncCalls)
	})

	t.Run("set_services_resyncs", func(t *testing.T) {
		client := &fakeSDS200Client{}
		session := &ScannerSession{client: client}
		serviceTypes := enabledValues(47, 2)

		err := session.executeIntent(IntentSetServiceTypes, ControlParams{
			ServiceTypes: serviceTypes,
		})
		require.NoError(t, err)

		snap := client.snapshot()
		require.Len(t, snap.setSVCCalls, 1)
		assert.Equal(t, serviceTypes, snap.setSVCCalls[0])
		assert.Equal(t, 1, snap.resyncCalls)
	})

	t.Run("set_volume_calls_client", func(t *testing.T) {
		client := &fakeSDS200Client{}
		session := &ScannerSession{client: client}

		err := session.executeIntent(IntentSetVolume, ControlParams{Volume: 18})
		require.NoError(t, err)

		snap := client.snapshot()
		assert.Equal(t, []int{18}, snap.setVolumeCalls)
	})

	t.Run("set_squelch_calls_client", func(t *testing.T) {
		client := &fakeSDS200Client{}
		session := &ScannerSession{client: client}

		err := session.executeIntent(IntentSetSquelch, ControlParams{Squelch: 6})
		require.NoError(t, err)

		snap := client.snapshot()
		assert.Equal(t, []int{6}, snap.setSquelchCalls)
	})

	t.Run("quick_keys_require_100", func(t *testing.T) {
		client := &fakeSDS200Client{}
		session := &ScannerSession{client: client}

		err := session.executeIntent(IntentSetFavoritesQuickKeys, ControlParams{
			QuickKeyValues: []int{1, 0},
		})
		require.Error(t, err)
		assert.Equal(t, "favorites quick keys must contain 100 values (got 2)", err.Error())
	})

	t.Run("services_require_47", func(t *testing.T) {
		client := &fakeSDS200Client{}
		session := &ScannerSession{client: client}

		err := session.executeIntent(IntentSetServiceTypes, ControlParams{
			ServiceTypes: []int{1, 0},
		})
		require.Error(t, err)
		assert.Equal(t, "service types must contain 47 values (got 2)", err.Error())
	})

	t.Run("rejects_unknown_intent", func(t *testing.T) {
		client := &fakeSDS200Client{}
		session := &ScannerSession{client: client}

		err := session.executeIntent(ControlIntent("bogus"), ControlParams{})
		require.Error(t, err)
		assert.Equal(t, "unsupported control intent: bogus", err.Error())
	})
}

func TestScannerSessionReadScanScope(t *testing.T) {
	t.Parallel()

	t.Run("reads_scope_values", func(t *testing.T) {
		client := &fakeSDS200Client{
			favoritesQuickKeys:  makeQuickKeyState(100, 10, 30),
			systemQuickKeys:     makeQuickKeyState(100, 2, 4),
			departmentQuickKeys: makeQuickKeyState(100, 8),
			serviceTypes:        enabledValues(47, 3),
		}
		session := &ScannerSession{client: client}

		scope, err := session.ReadScanScope(5, 7)
		require.NoError(t, err)
		assert.Equal(t, 5, scope.FavoritesTag)
		assert.Equal(t, 7, scope.SystemTag)
		assert.Equal(t, 1, scope.FavoritesQuickKeys[10])
		assert.Equal(t, 1, scope.SystemQuickKeys[2])
		assert.Equal(t, 1, scope.DepartmentQuickKeys[8])
		assert.Equal(t, enabledValues(47, 3), scope.ServiceTypes)

		snap := client.snapshot()
		assert.Equal(t, []int{5}, snap.getSQKCalls)
		require.Len(t, snap.getDQKCalls, 1)
		assert.Equal(t, 5, snap.getDQKCalls[0].fav)
		assert.Equal(t, 7, snap.getDQKCalls[0].sys)
	})

	t.Run("rejects_invalid_favorites_tag", func(t *testing.T) {
		session := &ScannerSession{client: &fakeSDS200Client{}}

		_, err := session.ReadScanScope(-1, 0)
		require.Error(t, err)
		assert.Equal(t, "favorites quick key must be in range 0-99 (got -1)", err.Error())
	})

	t.Run("rejects_nil_session", func(t *testing.T) {
		var session *ScannerSession
		_, err := session.ReadScanScope(0, 0)
		require.Error(t, err)
		assert.Equal(t, "scanner session unavailable", err.Error())
	})

	t.Run("does_not_hold_session_lock_while_loading_scope", func(t *testing.T) {
		started := make(chan struct{}, 1)
		release := make(chan struct{})
		client := &blockingFavoritesClient{
			fakeSDS200Client:    &fakeSDS200Client{},
			getFavoritesStarted: started,
			releaseFavorites:    release,
		}
		session := &ScannerSession{client: client}

		done := make(chan struct{})
		go func() {
			_, _ = session.ReadScanScope(0, 0)
			close(done)
		}()

		select {
		case <-started:
		case <-time.After(300 * time.Millisecond):
			t.Fatal("timed out waiting for scope read to start")
		}

		lockAcquired := make(chan struct{})
		var writerObservedClient SDS200Client
		go func() {
			session.mu.Lock()
			writerObservedClient = session.client
			session.mu.Unlock()
			close(lockAcquired)
		}()
		select {
		case <-lockAcquired:
			require.NotNil(t, writerObservedClient)
		case <-time.After(300 * time.Millisecond):
			t.Fatal("session write lock blocked while scope read was in-flight")
		}

		close(release)
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for scope read to finish")
		}
	})
}

func TestScannerSessionEnqueueControl(t *testing.T) {
	t.Parallel()

	t.Run("keeps_latest_intent", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		session := &ScannerSession{ctx: ctx, controlCh: make(chan controlRequest, 1)}
		session.EnqueueControl(IntentHold)
		session.EnqueueControl(IntentResumeScan)

		intent := requireIntentFromChannel(t, session.controlCh)
		assert.Equal(t, IntentResumeScan, intent)
	})

	t.Run("canceled_drops_stale_intent", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		session := &ScannerSession{ctx: ctx, controlCh: make(chan controlRequest, 1)}
		session.controlCh <- controlRequest{intent: IntentHold}
		cancel()

		session.EnqueueControl(IntentResumeScan)
		assertNoStaleIntent(t, session.controlCh, IntentHold)
	})
}

func TestScannerSessionControlLoop(t *testing.T) {
	t.Parallel()

	t.Run("executes_control", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		client := &fakeSDS200Client{
			telemetrySnapshot: sds200.RuntimeStatus{
				HoldTarget: sds200.HoldTarget{Keyword: "TGID", Arg1: "100", Arg2: "2"},
			},
			holdSignal: make(chan struct{}, 1),
		}
		session := &ScannerSession{
			ctx:       ctx,
			cancel:    cancel,
			client:    client,
			controlCh: make(chan controlRequest, 1),
		}

		session.wg.Add(1)
		go session.controlLoop()

		session.EnqueueControl(IntentHold)
		requireSignal(t, client.holdSignal)

		cancel()
		requireWaitGroupDone(t, &session.wg)

		snap := client.snapshot()
		require.Len(t, snap.holdCalls, 1)
		assert.Equal(t, "TGID", snap.holdCalls[0].keyword)
	})
}

func TestScannerSessionExecuteControl(t *testing.T) {
	t.Parallel()

	t.Run("returns_error_when_session_unavailable", func(t *testing.T) {
		var session *ScannerSession
		err := session.ExecuteControl(IntentHold, ControlParams{})
		require.Error(t, err)
		assert.Equal(t, "scanner session unavailable", err.Error())
	})

	t.Run("returns_error_no_channel", func(t *testing.T) {
		session := &ScannerSession{}
		err := session.ExecuteControl(IntentHold, ControlParams{})
		require.Error(t, err)
		assert.Equal(t, "scanner session unavailable", err.Error())
	})

	t.Run("returns_result_from_control_loop", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		client := &fakeSDS200Client{
			telemetrySnapshot: sds200.RuntimeStatus{
				HoldTarget: sds200.HoldTarget{Keyword: "TGID", Arg1: "100", Arg2: "2"},
			},
		}
		session := &ScannerSession{
			ctx:       ctx,
			cancel:    cancel,
			client:    client,
			controlCh: make(chan controlRequest, 1),
		}
		session.wg.Add(1)
		go session.controlLoop()
		defer func() {
			cancel()
			requireWaitGroupDone(t, &session.wg)
		}()

		err := session.ExecuteControl(IntentHold, ControlParams{})
		require.NoError(t, err)

		snap := client.snapshot()
		require.Len(t, snap.holdCalls, 1)
	})
}

func TestScannerSessionClose(t *testing.T) {
	t.Parallel()

	t.Run("closes_once", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		client := &fakeSDS200Client{}
		session := &ScannerSession{
			ctx:    ctx,
			cancel: cancel,
			client: client,
		}
		session.wg.Add(1)
		go func() {
			defer session.wg.Done()
			<-ctx.Done()
		}()

		require.NoError(t, session.Close())
		require.NoError(t, session.Close())

		snap := client.snapshot()
		assert.Equal(t, 1, snap.closeCalls)
	})
}

func requireIntentFromChannel(t *testing.T, ch <-chan controlRequest) ControlIntent {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	select {
	case <-ctx.Done():
		require.FailNow(t, "timed out waiting for control intent")
		return ""
	case req := <-ch:
		return req.intent
	}
}

func assertNoStaleIntent(t *testing.T, ch <-chan controlRequest, stale ControlIntent) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	select {
	case req := <-ch:
		assert.NotEqual(t, stale, req.intent)
	case <-ctx.Done():
	}
}

func requireSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	select {
	case <-ctx.Done():
		require.FailNow(t, "timed out waiting for signal")
	case <-ch:
	}
}

func requireWaitGroupDone(t *testing.T, wg *sync.WaitGroup) {
	t.Helper()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	select {
	case <-ctx.Done():
		require.FailNow(t, "timed out waiting for goroutine shutdown")
	case <-done:
	}
}

func enabledValues(length int, every int) []int {
	out := make([]int, length)
	if every <= 0 {
		return out
	}
	for i := 0; i < length; i++ {
		if i%every == 0 {
			out[i] = 1
		}
	}
	return out
}

func makeQuickKeyState(length int, enabled ...int) sds200.QuickKeyState {
	var state sds200.QuickKeyState
	for _, idx := range enabled {
		if idx >= 0 && idx < length && idx < len(state) {
			state[idx] = 1
		}
	}
	return state
}

func TestScannerSessionConnectAndSync(t *testing.T) {
	t.Parallel()

	t.Run("returns_factory_error", func(t *testing.T) {
		session := &ScannerSession{cfg: SessionConfig{Factory: func(cfg sds200.ClientConfig) (SDS200Client, error) {
			return nil, errors.New("factory failed")
		}}}

		err := session.connectAndSync()
		require.Error(t, err)
		assert.Equal(t, "create scanner client: factory failed", err.Error())
	})

	t.Run("returns_resync_error", func(t *testing.T) {
		client := &fakeSDS200Client{resyncErr: errors.New("resync failed")}
		session := &ScannerSession{cfg: SessionConfig{Factory: func(cfg sds200.ClientConfig) (SDS200Client, error) {
			return client, nil
		}}}

		err := session.connectAndSync()
		require.Error(t, err)
		assert.Equal(t, "initial scanner resync: resync failed", err.Error())
		assert.Equal(t, 1, client.snapshot().closeCalls)
	})

	t.Run("returns_push_setup_error", func(t *testing.T) {
		client := &fakeSDS200Client{
			resyncStatus: sds200.RuntimeStatus{Connected: true},
			startPushErr: errors.New("push failed"),
		}
		session := &ScannerSession{cfg: SessionConfig{Factory: func(cfg sds200.ClientConfig) (SDS200Client, error) {
			return client, nil
		}}}

		err := session.connectAndSync()
		require.Error(t, err)
		assert.Equal(t, "enable scanner push telemetry: push failed", err.Error())
		assert.Equal(t, 1, client.snapshot().closeCalls)
	})
}

func TestScannerSessionHealthCheck(t *testing.T) {
	t.Parallel()

	t.Run("resync_reconnects_client", func(t *testing.T) {
		initial := &fakeSDS200Client{
			resyncErr: errors.New("network interrupt"),
		}
		reconnected := &fakeSDS200Client{
			resyncStatus: sds200.RuntimeStatus{Connected: true, Channel: "Dispatch 2"},
		}

		factoryCalls := 0
		session := &ScannerSession{
			cfg: SessionConfig{
				Scanner:        store.ScannerConfig{IP: "127.0.0.1"},
				PushIntervalMS: 750,
				Factory: func(cfg sds200.ClientConfig) (SDS200Client, error) {
					factoryCalls++
					if factoryCalls == 1 {
						return reconnected, nil
					}
					return nil, errors.New("unexpected factory call")
				},
			}.withDefaults(),
			client:   initial,
			stateHub: newStateHub(),
		}

		err := session.healthCheck()
		require.NoError(t, err)

		initialSnap := initial.snapshot()
		reconnectedSnap := reconnected.snapshot()
		require.Equal(t, 1, initialSnap.resyncCalls)
		require.Equal(t, 1, initialSnap.closeCalls)
		require.Equal(t, 1, factoryCalls)
		require.Equal(t, 1, reconnectedSnap.resyncCalls)
		require.Equal(t, []int{750}, reconnectedSnap.startPushCalls)
		assert.True(t, session.stateHub.snapshot().Scanner.Connected)
		assert.Equal(t, "Dispatch 2", session.stateHub.snapshot().Scanner.Channel)
	})
}

func TestScannerSessionSuperviseFaultInjection(t *testing.T) {
	t.Parallel()

	t.Run("transient_failure_recovers_without_fatal", func(t *testing.T) {
		hub := newStateHub()
		initial := &fakeSDS200Client{
			resyncStatus: sds200.RuntimeStatus{Connected: true, Channel: "Dispatch 1"},
		}
		reconnected := &fakeSDS200Client{
			resyncStatus: sds200.RuntimeStatus{Connected: true, Channel: "Dispatch 2"},
		}

		factoryCalls := 0
		session, err := NewScannerSession(t.Context(), SessionConfig{
			Scanner:             store.ScannerConfig{IP: "127.0.0.1"},
			HealthCheckInterval: 5 * time.Millisecond,
			ReconnectDelay:      time.Millisecond,
			MaxReconnectFails:   3,
			PushIntervalMS:      250,
			Factory: func(cfg sds200.ClientConfig) (SDS200Client, error) {
				factoryCalls++
				switch factoryCalls {
				case 1:
					return initial, nil
				case 2:
					return reconnected, nil
				default:
					return reconnected, nil
				}
			},
		}, hub)
		require.NoError(t, err)
		defer func() { _ = session.Close() }()

		initial.mu.Lock()
		initial.resyncErr = errors.New("temporary timeout")
		initial.mu.Unlock()

		require.Eventually(t, func() bool {
			return hub.snapshot().Scanner.Channel == "Dispatch 2"
		}, time.Second, 10*time.Millisecond)

		assertNoFatalWithin(t, session.Fatal(), 100*time.Millisecond)
	})

	t.Run("reconnect_budget_exhaustion_signals_fatal", func(t *testing.T) {
		hub := newStateHub()
		initial := &fakeSDS200Client{
			resyncStatus: sds200.RuntimeStatus{Connected: true},
		}

		factoryCalls := 0
		session, err := NewScannerSession(t.Context(), SessionConfig{
			Scanner:             store.ScannerConfig{IP: "127.0.0.1"},
			HealthCheckInterval: 5 * time.Millisecond,
			ReconnectDelay:      time.Millisecond,
			MaxReconnectFails:   2,
			Factory: func(cfg sds200.ClientConfig) (SDS200Client, error) {
				factoryCalls++
				if factoryCalls == 1 {
					return initial, nil
				}
				return nil, errors.New("dial failed")
			},
		}, hub)
		require.NoError(t, err)
		defer func() { _ = session.Close() }()

		initial.mu.Lock()
		initial.resyncErr = errors.New("socket read failed")
		initial.mu.Unlock()

		err = requireErrorFromFatal(t, session.Fatal())
		assert.Contains(t, err.Error(), "scanner reconnect budget exceeded")
		assert.Contains(t, err.Error(), "dial failed")
		assert.GreaterOrEqual(t, factoryCalls, 3)
	})
}

func requireErrorFromFatal(t *testing.T, fatal <-chan error) error {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	select {
	case <-ctx.Done():
		require.FailNow(t, "timed out waiting for fatal error")
		return nil
	case err := <-fatal:
		require.Error(t, err)
		return err
	}
}

func assertNoFatalWithin(t *testing.T, fatal <-chan error, duration time.Duration) {
	t.Helper()

	timer := time.NewTimer(duration)
	defer timer.Stop()

	select {
	case err := <-fatal:
		require.NoError(t, err)
	case <-timer.C:
	}
}
