package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jentfoo/SignalSentinel/internal/audio/recording"
	"github.com/jentfoo/SignalSentinel/internal/gui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSuperviseSubsystems(t *testing.T) {
	t.Parallel()

	t.Run("runtime_error_wins", func(t *testing.T) {
		runtime, cancelRuntime, runtimeFatal := newTestRuntime(t)
		defer cancelRuntime()

		audioErrs := make(chan error, 1)
		out := superviseSubsystems(t.Context(), runtime, audioErrs)

		wantErr := errors.New("runtime failed")
		runtimeFatal <- wantErr

		err := requireErrorFromChannel(t, out)
		assert.ErrorIs(t, err, wantErr)
	})

	t.Run("audio_error_wins", func(t *testing.T) {
		runtime, cancelRuntime, _ := newTestRuntime(t)
		defer cancelRuntime()

		audioErrs := make(chan error, 1)
		out := superviseSubsystems(t.Context(), runtime, audioErrs)

		wantErr := errors.New("audio failed")
		audioErrs <- wantErr

		err := requireErrorFromChannel(t, out)
		assert.ErrorIs(t, err, wantErr)
	})

	t.Run("ignores_canceled_errors", func(t *testing.T) {
		runtime, cancelRuntime, runtimeFatal := newTestRuntime(t)
		defer cancelRuntime()

		audioErrs := make(chan error, 2)
		out := superviseSubsystems(t.Context(), runtime, audioErrs)

		audioErrs <- context.Canceled
		wantErr := errors.New("scanner fault")
		runtimeFatal <- wantErr

		err := requireErrorFromChannel(t, out)
		assert.ErrorIs(t, err, wantErr)
	})

	t.Run("context_closes_channel", func(t *testing.T) {
		runtime, cancelRuntime, _ := newTestRuntime(t)
		defer cancelRuntime()

		ctx, cancel := context.WithCancel(t.Context())
		audioErrs := make(chan error, 1)
		out := superviseSubsystems(ctx, runtime, audioErrs)
		cancel()

		requireChannelClosed(t, out)
	})
}

func TestPublishLatestGUIState(t *testing.T) {
	t.Parallel()

	t.Run("writes_to_empty_channel", func(t *testing.T) {
		out := make(chan gui.RuntimeState, 1)
		want := gui.RuntimeState{Scanner: gui.ScannerStatus{Mode: "scan"}}

		publishLatestGUIState(out, want)

		got := <-out
		assert.Equal(t, want, got)
	})

	t.Run("replaces_stale_value_when_full", func(t *testing.T) {
		out := make(chan gui.RuntimeState, 1)
		out <- gui.RuntimeState{Scanner: gui.ScannerStatus{Mode: "old"}}
		want := gui.RuntimeState{Scanner: gui.ScannerStatus{Mode: "new"}}

		publishLatestGUIState(out, want)

		got := <-out
		assert.Equal(t, want, got)
	})
}

func TestMapGUIControlRequest(t *testing.T) {
	t.Parallel()

	t.Run("maps_jump_number_tag", func(t *testing.T) {
		intent, params, action, err := mapGUIControlRequest(gui.ControlRequest{
			Intent:    gui.IntentJumpNumberTag,
			NumberTag: gui.NumberTag{Favorites: 1, System: 2, Channel: 3},
		})
		require.NoError(t, err)
		assert.Equal(t, IntentJumpNumberTag, intent)
		assert.Equal(t, 1, params.FavoritesTag)
		assert.Equal(t, 2, params.SystemTag)
		assert.Equal(t, 3, params.ChannelTag)
		assert.Equal(t, "Jump Number Tag", action)
	})

	t.Run("rejects_unknown_intent", func(t *testing.T) {
		_, _, _, err := mapGUIControlRequest(gui.ControlRequest{
			Intent: gui.ControlIntent("bogus"),
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported control intent")
	})

	t.Run("maps_set_volume", func(t *testing.T) {
		intent, params, action, err := mapGUIControlRequest(gui.ControlRequest{
			Intent: gui.IntentSetVolume,
			Volume: 17,
		})
		require.NoError(t, err)
		assert.Equal(t, IntentSetVolume, intent)
		assert.Equal(t, 17, params.Volume)
		assert.Equal(t, "Set Volume", action)
	})

	t.Run("maps_set_service_types", func(t *testing.T) {
		intent, params, action, err := mapGUIControlRequest(gui.ControlRequest{
			Intent:       gui.IntentSetServiceTypes,
			ServiceTypes: []int{1, 0, 1},
		})
		require.NoError(t, err)
		assert.Equal(t, IntentSetServiceTypes, intent)
		assert.Equal(t, []int{1, 0, 1}, params.ServiceTypes)
		assert.Equal(t, "Set Service Types", action)
	})
}

func TestDeriveLifecycleMode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		connected bool
		hold      bool
		mode      string
		want      string
	}{
		{name: "disconnected", connected: false, hold: false, mode: "", want: "Disconnected"},
		{name: "hold", connected: true, hold: true, mode: "Scan", want: "Hold"},
		{name: "paused_analyze", connected: true, hold: false, mode: "Analyze", want: "Paused/Analyze"},
		{name: "scanning", connected: true, hold: false, mode: "Scan Mode", want: "Scanning"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, gui.DeriveLifecycleMode(tt.connected, tt.hold, tt.mode))
		})
	}
}

func TestBuildGUICapabilities(t *testing.T) {
	t.Parallel()

	t.Run("maps_capabilities", func(t *testing.T) {
		caps := buildGUICapabilities(map[ControlIntent]CapabilityAvailability{
			IntentHold:       {Available: true},
			IntentResumeScan: {Available: false, DisabledReason: "scanner is not in hold mode"},
		})
		require.NotNil(t, caps)
		assert.True(t, caps[gui.IntentHoldCurrent].Available)
		assert.False(t, caps[gui.IntentReleaseHold].Available)
		assert.Equal(t, "scanner is not in hold mode", caps[gui.IntentReleaseHold].DisabledReason)
	})
}

func TestClassifyControlError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		err             error
		wantMessage     string
		wantUnsupported bool
	}{
		{
			name:            "unsupported_intent",
			err:             errors.New("unsupported control intent: foo"),
			wantMessage:     "operation not supported",
			wantUnsupported: true,
		},
		{
			name:            "invalid_range",
			err:             errors.New("volume must be in range 0-29"),
			wantMessage:     "invalid input",
			wantUnsupported: false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			msg, hint, unsupported := classifyControlError(tt.err)
			assert.Equal(t, tt.wantMessage, msg)
			assert.Equal(t, tt.wantUnsupported, unsupported)
			assert.NotEmpty(t, hint)
		})
	}
}

func TestRecordingStatusChanged(t *testing.T) {
	t.Parallel()

	base := recording.Status{
		Active:    true,
		StartedAt: time.Date(2026, 3, 8, 10, 0, 0, 0, time.UTC),
		Trigger:   "manual",
		Manual:    true,
	}
	tests := []struct {
		name string
		next recording.Status
		want bool
	}{
		{name: "same_status", next: base, want: false},
		{
			name: "active_changed",
			next: func() recording.Status {
				changed := base
				changed.Active = false
				return changed
			}(),
			want: true,
		},
		{
			name: "manual_changed",
			next: func() recording.Status {
				changed := base
				changed.Manual = false
				return changed
			}(),
			want: true,
		},
		{
			name: "trigger_changed",
			next: func() recording.Status {
				changed := base
				changed.Trigger = "mixed"
				return changed
			}(),
			want: true,
		},
		{
			name: "start_changed",
			next: func() recording.Status {
				changed := base
				changed.StartedAt = changed.StartedAt.Add(time.Second)
				return changed
			}(),
			want: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, recordingStatusChanged(base, tt.next))
		})
	}
}

func newTestRuntime(t *testing.T) (*Runtime, context.CancelFunc, chan error) {
	t.Helper()

	runtimeCtx, cancelRuntime := context.WithCancel(t.Context())
	runtimeFatal := make(chan error, 1)
	runtime := &Runtime{
		ctx: runtimeCtx,
		session: &ScannerSession{
			fatalErr: runtimeFatal,
		},
	}
	return runtime, cancelRuntime, runtimeFatal
}

func requireErrorFromChannel(t *testing.T, ch <-chan error) error {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	select {
	case <-ctx.Done():
		require.FailNow(t, "timed out waiting for error")
		return nil
	case err, ok := <-ch:
		require.True(t, ok)
		require.Error(t, err)
		return err
	}
}

func requireChannelClosed(t *testing.T, ch <-chan error) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	select {
	case <-ctx.Done():
		require.FailNow(t, "timed out waiting for channel close")
	case _, ok := <-ch:
		assert.False(t, ok)
	}
}
