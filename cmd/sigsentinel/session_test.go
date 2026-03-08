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

	resyncStatus sds200.RuntimeStatus
	resyncErr    error
	startPushErr error
	holdErr      error
	jumpErr      error
	closeErr     error

	telemetrySnapshot sds200.RuntimeStatus
	onTelemetry       func(sds200.RuntimeStatus)

	resyncCalls    int
	startPushCalls []int
	holdCalls      []holdCall
	jumpCalls      []jumpCall
	closeCalls     int

	holdSignal chan struct{}
	jumpSignal chan struct{}
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
		resyncCalls:    f.resyncCalls,
		startPushCalls: append([]int(nil), f.startPushCalls...),
		holdCalls:      append([]holdCall(nil), f.holdCalls...),
		jumpCalls:      append([]jumpCall(nil), f.jumpCalls...),
		closeCalls:     f.closeCalls,
		hasTelemetry:   f.onTelemetry != nil,
	}
}

type fakeClientSnapshot struct {
	resyncCalls    int
	startPushCalls []int
	holdCalls      []holdCall
	jumpCalls      []jumpCall
	closeCalls     int
	hasTelemetry   bool
}

func TestSessionConfigWithDefaults(t *testing.T) {
	t.Parallel()

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
	require.NotNil(t, cfg.Logger)
	require.NotNil(t, cfg.Factory)
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
		err := session.executeIntent(IntentHold)
		require.Error(t, err)
		assert.Equal(t, "scanner client unavailable", err.Error())
	})

	t.Run("resume_calls_jump_mode", func(t *testing.T) {
		client := &fakeSDS200Client{}
		session := &ScannerSession{client: client}

		require.NoError(t, session.executeIntent(IntentResumeScan))

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

		require.NoError(t, session.executeIntent(IntentHold))

		snap := client.snapshot()
		require.Len(t, snap.holdCalls, 1)
		assert.Equal(t, "TGID", snap.holdCalls[0].keyword)
		assert.Equal(t, "100", snap.holdCalls[0].arg1)
		assert.Equal(t, "2", snap.holdCalls[0].arg2)
	})

	t.Run("hold_requires_hold_target", func(t *testing.T) {
		client := &fakeSDS200Client{}
		session := &ScannerSession{client: client}

		err := session.executeIntent(IntentHold)
		require.Error(t, err)
		assert.Equal(t, "hold target unavailable for current scanner state", err.Error())
	})

	t.Run("rejects_unknown_intent", func(t *testing.T) {
		client := &fakeSDS200Client{}
		session := &ScannerSession{client: client}

		err := session.executeIntent(ControlIntent("bogus"))
		require.Error(t, err)
		assert.Equal(t, "unsupported control intent: bogus", err.Error())
	})
}

func TestScannerSessionEnqueueControl(t *testing.T) {
	t.Parallel()

	t.Run("keeps_latest_intent", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		session := &ScannerSession{ctx: ctx, controlCh: make(chan ControlIntent, 1)}
		session.EnqueueControl(IntentHold)
		session.EnqueueControl(IntentResumeScan)

		intent := requireIntentFromChannel(t, session.controlCh)
		assert.Equal(t, IntentResumeScan, intent)
	})

	t.Run("canceled_drops_stale_intent", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		session := &ScannerSession{ctx: ctx, controlCh: make(chan ControlIntent, 1)}
		session.controlCh <- IntentHold
		cancel()

		session.EnqueueControl(IntentResumeScan)
		assertNoStaleIntent(t, session.controlCh, IntentHold)
	})
}

func TestScannerSessionControlLoop(t *testing.T) {
	t.Parallel()

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
		controlCh: make(chan ControlIntent, 1),
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
}

func TestScannerSessionClose(t *testing.T) {
	t.Parallel()

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
}

func requireIntentFromChannel(t *testing.T, ch <-chan ControlIntent) ControlIntent {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	select {
	case <-ctx.Done():
		t.Fatalf("timed out waiting for control intent")
		return ""
	case intent := <-ch:
		return intent
	}
}

func assertNoStaleIntent(t *testing.T, ch <-chan ControlIntent, stale ControlIntent) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	select {
	case intent := <-ch:
		assert.NotEqual(t, stale, intent)
	case <-ctx.Done():
	}
}

func requireSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	select {
	case <-ctx.Done():
		t.Fatalf("timed out waiting for signal")
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
		t.Fatalf("timed out waiting for goroutine shutdown")
	case <-done:
	}
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
