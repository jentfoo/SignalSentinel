package main

import (
	"context"
	"errors"
	"testing"
	"time"

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
		t.Fatalf("timed out waiting for error")
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
		t.Fatalf("timed out waiting for channel close")
	case _, ok := <-ch:
		assert.False(t, ok)
	}
}
