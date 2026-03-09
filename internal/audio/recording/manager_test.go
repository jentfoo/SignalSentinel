package recording

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/jentfoo/SignalSentinel/internal/activity"
	"github.com/jentfoo/SignalSentinel/internal/sds200"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testWriter struct {
	writes      int
	samples     int
	finalized   bool
	aborted     bool
	finalizeOps int
	abortOps    int
	finalizeLen int64
	writeErr    error
	finalizeErr error
	writeCount  int
	failAfter   int
}

func (w *testWriter) WritePCM(samples []int16) error {
	w.writeCount++
	if w.failAfter > 0 && w.writeCount >= w.failAfter {
		return w.writeErr
	}
	w.writes++
	w.samples += len(samples)
	return nil
}

func (w *testWriter) Finalize() (int64, error) {
	w.finalizeOps++
	if w.finalizeErr != nil {
		return 0, w.finalizeErr
	}
	w.finalized = true
	return w.finalizeLen, nil
}

func (w *testWriter) Abort() error {
	w.abortOps++
	w.aborted = true
	return nil
}

func activeStatus() sds200.RuntimeStatus {
	return sds200.RuntimeStatus{Connected: true, SquelchOpen: true, Frequency: "155.2200", System: "County", Channel: "Ops"}
}

func idleStatus() sds200.RuntimeStatus {
	return sds200.RuntimeStatus{Connected: true, SquelchOpen: false, Frequency: "155.2200", System: "County", Channel: "Ops"}
}

func TestNewManager(t *testing.T) {
	t.Parallel()

	m := NewManager(Config{})
	require.NotNil(t, m)
	assert.Equal(t, activity.StateIdle, m.detector.State())
	assert.Equal(t, 10*time.Second, m.cfg.HangTime)
}

func TestManagerUpdateTelemetry(t *testing.T) {
	t.Parallel()

	t.Run("basic_lifecycle", func(t *testing.T) {
		t0 := time.Date(2026, 3, 8, 10, 0, 0, 0, time.UTC)
		writer := &testWriter{finalizeLen: 99}
		m := NewManager(Config{
			OutputDir: filepath.Join(t.TempDir(), "clips"),
			HangTime:  10 * time.Second,
			WriterFactory: func(path string) (PCMWriter, error) {
				return writer, nil
			},
		})

		require.NoError(t, m.UpdateTelemetry(activeStatus(), t0))
		require.NotNil(t, m.writer)
		require.NoError(t, m.UpdateTelemetry(idleStatus(), t0.Add(time.Second)))
	})

	t.Run("start_debounce_delays_auto_recording_start", func(t *testing.T) {
		t0 := time.Date(2026, 3, 8, 10, 0, 0, 0, time.UTC)
		writer := &testWriter{}
		m := NewManager(Config{
			OutputDir:     filepath.Join(t.TempDir(), "clips"),
			HangTime:      10 * time.Second,
			StartDebounce: 200 * time.Millisecond,
			WriterFactory: func(path string) (PCMWriter, error) {
				return writer, nil
			},
		})

		require.NoError(t, m.UpdateTelemetry(activeStatus(), t0))
		assert.Nil(t, m.writer)

		require.NoError(t, m.UpdateTelemetry(activeStatus(), t0.Add(150*time.Millisecond)))
		assert.Nil(t, m.writer)

		require.NoError(t, m.UpdateTelemetry(activeStatus(), t0.Add(200*time.Millisecond)))
		require.NotNil(t, m.writer)
		assert.Equal(t, t0, m.started)
	})

	t.Run("finalize_error_aborts_writer", func(t *testing.T) {
		t0 := time.Date(2026, 3, 8, 10, 0, 0, 0, time.UTC)
		writer := &testWriter{finalizeErr: errors.New("disk full")}
		m := NewManager(Config{
			OutputDir: filepath.Join(t.TempDir(), "clips"),
			HangTime:  10 * time.Second,
			WriterFactory: func(path string) (PCMWriter, error) {
				return writer, nil
			},
		})

		require.NoError(t, m.UpdateTelemetry(activeStatus(), t0))
		require.NoError(t, m.UpdateTelemetry(idleStatus(), t0.Add(time.Second)))

		err := m.UpdateTelemetry(idleStatus(), t0.Add(12*time.Second))
		require.Error(t, err)
		assert.True(t, writer.aborted)
	})

	t.Run("frequency_change_splits_auto_recording_immediately", func(t *testing.T) {
		t0 := time.Date(2026, 3, 8, 10, 0, 0, 0, time.UTC)
		var clips []Metadata
		m := NewManager(Config{
			OutputDir: filepath.Join(t.TempDir(), "clips"),
			HangTime:  10 * time.Second,
			OnFinalized: func(meta Metadata) error {
				clips = append(clips, meta)
				return nil
			},
		})

		first := sds200.RuntimeStatus{
			Connected:   true,
			SquelchOpen: true,
			Frequency:   "155.2200",
			System:      "County",
			Channel:     "Ops 1",
		}
		second := sds200.RuntimeStatus{
			Connected:   true,
			SquelchOpen: true,
			Frequency:   "460.0000",
			System:      "Metro",
			Channel:     "Dispatch 9",
		}

		require.NoError(t, m.UpdateTelemetry(first, t0))
		require.NoError(t, m.PushPCM([]int16{1, 2, 3}, t0.Add(time.Second)))
		require.NoError(t, m.UpdateTelemetry(second, t0.Add(2*time.Second)))

		require.Len(t, clips, 1)
		assert.Equal(t, "155.2200", clips[0].Frequency)

		snap := m.Snapshot()
		assert.True(t, snap.Active)
		assert.False(t, snap.Manual)
		assert.Equal(t, t0.Add(2*time.Second), snap.StartedAt)
		assert.Equal(t, "telemetry", snap.Trigger)
	})

	t.Run("avoid_stops_auto_recording_immediately", func(t *testing.T) {
		t0 := time.Date(2026, 3, 8, 10, 0, 0, 0, time.UTC)
		var clips []Metadata
		m := NewManager(Config{
			OutputDir: filepath.Join(t.TempDir(), "clips"),
			HangTime:  10 * time.Second,
			OnFinalized: func(meta Metadata) error {
				clips = append(clips, meta)
				return nil
			},
		})

		start := sds200.RuntimeStatus{
			Connected:   true,
			SquelchOpen: true,
			Frequency:   "155.2200",
			System:      "County",
			Channel:     "Ops 1",
			AvoidKnown:  true,
			Avoided:     false,
		}
		avoided := start
		avoided.Avoided = true

		require.NoError(t, m.UpdateTelemetry(start, t0))
		require.NoError(t, m.PushPCM([]int16{1, 2, 3}, t0.Add(time.Second)))
		require.NoError(t, m.UpdateTelemetry(avoided, t0.Add(2*time.Second)))

		require.Len(t, clips, 1)
		assert.Equal(t, "155.2200", clips[0].Frequency)
		assert.False(t, m.Snapshot().Active)
	})

	t.Run("suppresses_auto_recording_with_insufficient_non_silent_audio", func(t *testing.T) {
		t0 := time.Date(2026, 3, 8, 10, 0, 0, 0, time.UTC)
		var clips []Metadata
		m := NewManager(Config{
			OutputDir:            filepath.Join(t.TempDir(), "clips"),
			HangTime:             10 * time.Second,
			MinAutoDuration:      200 * time.Millisecond,
			MinNonSilentDuration: 300 * time.Millisecond,
			OnFinalized: func(meta Metadata) error {
				clips = append(clips, meta)
				return nil
			},
		})

		require.NoError(t, m.UpdateTelemetry(activeStatus(), t0))
		brief := make([]int16, 800) // 100ms at 8kHz
		for i := range brief {
			brief[i] = 1500
		}
		require.NoError(t, m.PushPCM(brief, t0.Add(50*time.Millisecond)))
		require.NoError(t, m.UpdateTelemetry(idleStatus(), t0.Add(150*time.Millisecond)))
		require.NoError(t, m.Tick(t0.Add(11*time.Second)))

		assert.Empty(t, clips)
		assert.False(t, m.Snapshot().Active)
	})

	t.Run("keeps_auto_recording_when_non_silent_audio_threshold_is_met", func(t *testing.T) {
		t0 := time.Date(2026, 3, 8, 10, 0, 0, 0, time.UTC)
		var clips []Metadata
		m := NewManager(Config{
			OutputDir:            filepath.Join(t.TempDir(), "clips"),
			HangTime:             10 * time.Second,
			MinAutoDuration:      200 * time.Millisecond,
			MinNonSilentDuration: 300 * time.Millisecond,
			OnFinalized: func(meta Metadata) error {
				clips = append(clips, meta)
				return nil
			},
		})

		require.NoError(t, m.UpdateTelemetry(activeStatus(), t0))
		voiced := make([]int16, 3200) // 400ms at 8kHz
		for i := range voiced {
			voiced[i] = 1500
		}
		require.NoError(t, m.PushPCM(voiced, t0.Add(100*time.Millisecond)))
		require.NoError(t, m.UpdateTelemetry(idleStatus(), t0.Add(500*time.Millisecond)))
		require.NoError(t, m.Tick(t0.Add(11*time.Second)))

		require.Len(t, clips, 1)
		assert.Equal(t, "telemetry", clips[0].Trigger)
	})
}

func TestManagerPushPCM(t *testing.T) {
	t.Parallel()

	t.Run("ignores_when_idle", func(t *testing.T) {
		writer := &testWriter{}
		m := NewManager(Config{
			OutputDir: filepath.Join(t.TempDir(), "clips"),
			WriterFactory: func(path string) (PCMWriter, error) {
				return writer, nil
			},
		})
		require.NoError(t, m.PushPCM([]int16{1, 2, 3}, time.Now()))
		assert.Equal(t, 0, writer.writes)
	})

	t.Run("writes_when_active", func(t *testing.T) {
		t0 := time.Date(2026, 3, 8, 10, 0, 0, 0, time.UTC)
		writer := &testWriter{}
		m := NewManager(Config{
			OutputDir: filepath.Join(t.TempDir(), "clips"),
			WriterFactory: func(path string) (PCMWriter, error) {
				return writer, nil
			},
		})
		require.NoError(t, m.UpdateTelemetry(activeStatus(), t0))
		require.NoError(t, m.PushPCM([]int16{1, 2, 3}, t0.Add(time.Second)))
		assert.Equal(t, 1, writer.writes)
		assert.Equal(t, 3, writer.samples)
	})

	t.Run("write_error_aborts_writer", func(t *testing.T) {
		t0 := time.Date(2026, 3, 8, 10, 0, 0, 0, time.UTC)
		writer := &testWriter{
			writeErr:  errors.New("write failed"),
			failAfter: 2,
		}
		m := NewManager(Config{
			OutputDir: filepath.Join(t.TempDir(), "clips"),
			WriterFactory: func(path string) (PCMWriter, error) {
				return writer, nil
			},
		})

		require.NoError(t, m.UpdateTelemetry(activeStatus(), t0))
		require.NoError(t, m.PushPCM([]int16{1, 2, 3}, t0.Add(time.Second)))

		err := m.PushPCM([]int16{4, 5, 6}, t0.Add(2*time.Second))
		require.Error(t, err)
		assert.True(t, writer.aborted)

		err = m.PushPCM([]int16{7, 8, 9}, t0.Add(3*time.Second))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "recording write fault")
	})
}

func TestManagerTick(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, 3, 8, 10, 0, 0, 0, time.UTC)
	writer := &testWriter{finalizeLen: 5}
	m := NewManager(Config{
		OutputDir: filepath.Join(t.TempDir(), "clips"),
		HangTime:  10 * time.Second,
		WriterFactory: func(path string) (PCMWriter, error) {
			return writer, nil
		},
	})
	require.NoError(t, m.UpdateTelemetry(activeStatus(), t0))
	require.NoError(t, m.UpdateTelemetry(idleStatus(), t0.Add(2*time.Second)))
	require.NoError(t, m.Tick(t0.Add(12*time.Second)))
	assert.True(t, writer.finalized)
}

func TestManagerClose(t *testing.T) {
	t.Parallel()

	t.Run("normal_close_finalizes_once_without_abort", func(t *testing.T) {
		t0 := time.Date(2026, 3, 8, 10, 0, 0, 0, time.UTC)
		writer := &testWriter{finalizeLen: 8}
		m := NewManager(Config{
			OutputDir: filepath.Join(t.TempDir(), "clips"),
			WriterFactory: func(path string) (PCMWriter, error) {
				return writer, nil
			},
		})
		require.NoError(t, m.UpdateTelemetry(activeStatus(), t0))
		require.NoError(t, m.Close())
		assert.True(t, writer.finalized)
		assert.Equal(t, 1, writer.finalizeOps)
		assert.Equal(t, 0, writer.abortOps)
	})

	t.Run("faulted_close_returns_same_error_without_finalize", func(t *testing.T) {
		t0 := time.Date(2026, 3, 8, 10, 0, 0, 0, time.UTC)
		writer := &testWriter{
			writeErr:  errors.New("disk write fault"),
			failAfter: 1,
		}
		m := NewManager(Config{
			OutputDir: filepath.Join(t.TempDir(), "clips"),
			WriterFactory: func(path string) (PCMWriter, error) {
				return writer, nil
			},
		})
		require.NoError(t, m.UpdateTelemetry(activeStatus(), t0))

		err := m.PushPCM([]int16{1, 2, 3}, t0.Add(time.Second))
		require.Error(t, err)

		closeErr := m.Close()
		require.Error(t, closeErr)
		assert.Equal(t, err.Error(), closeErr.Error())
		assert.Equal(t, 0, writer.finalizeOps)
		assert.Equal(t, 1, writer.abortOps)
	})
}

func TestManagerUpdateOutputDir(t *testing.T) {
	t.Parallel()

	t.Run("rejects_blank_path", func(t *testing.T) {
		m := NewManager(Config{OutputDir: filepath.Join(t.TempDir(), "clips")})
		err := m.UpdateOutputDir("   ")
		require.Error(t, err)
		assert.Equal(t, "recording output directory is required", err.Error())
	})

	t.Run("updates_output_path", func(t *testing.T) {
		m := NewManager(Config{OutputDir: filepath.Join(t.TempDir(), "clips")})
		nextDir := filepath.Join(t.TempDir(), "new-clips")

		require.NoError(t, m.UpdateOutputDir(nextDir))

		m.mu.Lock()
		defer m.mu.Unlock()
		assert.Equal(t, nextDir, m.cfg.OutputDir)
	})
}

func TestManagerManualLifecycle(t *testing.T) {
	t.Parallel()

	t.Run("manual_start_stop_uses_manual_trigger", func(t *testing.T) {
		t0 := time.Date(2026, 3, 8, 10, 0, 0, 0, time.UTC)
		var clips []Metadata
		m := NewManager(Config{
			OutputDir: filepath.Join(t.TempDir(), "clips"),
			OnFinalized: func(meta Metadata) error {
				clips = append(clips, meta)
				return nil
			},
		})

		require.NoError(t, m.StartManual(idleStatus(), t0))
		require.NoError(t, m.PushPCM([]int16{1, 2, 3}, t0.Add(time.Second)))
		require.NoError(t, m.StopManual(t0.Add(2*time.Second)))

		require.Len(t, clips, 1)
		assert.Equal(t, "manual", clips[0].Trigger)
	})

	t.Run("manual_start_on_active_uses_mixed_trigger", func(t *testing.T) {
		t0 := time.Date(2026, 3, 8, 10, 0, 0, 0, time.UTC)
		var clips []Metadata
		m := NewManager(Config{
			OutputDir: filepath.Join(t.TempDir(), "clips"),
			OnFinalized: func(meta Metadata) error {
				clips = append(clips, meta)
				return nil
			},
		})

		require.NoError(t, m.StartManual(activeStatus(), t0))
		require.NoError(t, m.PushPCM([]int16{1, 2, 3}, t0.Add(time.Second)))
		require.NoError(t, m.StopManual(t0.Add(2*time.Second)))

		require.Len(t, clips, 1)
		assert.Equal(t, "mixed", clips[0].Trigger)
	})

	t.Run("telemetry_session_becomes_mixed_after_manual_start", func(t *testing.T) {
		t0 := time.Date(2026, 3, 8, 10, 0, 0, 0, time.UTC)
		var clips []Metadata
		m := NewManager(Config{
			OutputDir: filepath.Join(t.TempDir(), "clips"),
			HangTime:  10 * time.Second,
			OnFinalized: func(meta Metadata) error {
				clips = append(clips, meta)
				return nil
			},
		})

		require.NoError(t, m.UpdateTelemetry(activeStatus(), t0))
		require.NoError(t, m.PushPCM([]int16{1, 2, 3}, t0.Add(time.Second)))
		require.NoError(t, m.StartManual(activeStatus(), t0.Add(2*time.Second)))
		require.NoError(t, m.StopManual(t0.Add(3*time.Second)))

		require.Len(t, clips, 1)
		assert.Equal(t, "mixed", clips[0].Trigger)
	})

	t.Run("manual_recording_ignores_avoid_and_frequency_changes", func(t *testing.T) {
		t0 := time.Date(2026, 3, 8, 10, 0, 0, 0, time.UTC)
		m := NewManager(Config{
			OutputDir: filepath.Join(t.TempDir(), "clips"),
			HangTime:  10 * time.Second,
		})

		start := sds200.RuntimeStatus{
			Connected:   true,
			SquelchOpen: false,
			Frequency:   "155.2200",
			System:      "County",
			Channel:     "Ops 1",
			AvoidKnown:  true,
			Avoided:     false,
		}
		changed := sds200.RuntimeStatus{
			Connected:   true,
			SquelchOpen: false,
			Frequency:   "460.0000",
			System:      "Metro",
			Channel:     "Dispatch 9",
			AvoidKnown:  true,
			Avoided:     true,
		}

		require.NoError(t, m.StartManual(start, t0))
		require.NoError(t, m.UpdateTelemetry(changed, t0.Add(2*time.Second)))

		snap := m.Snapshot()
		assert.True(t, snap.Active)
		assert.True(t, snap.Manual)
	})

	t.Run("manual_recording_is_not_suppressed_by_non_silent_threshold", func(t *testing.T) {
		t0 := time.Date(2026, 3, 8, 10, 0, 0, 0, time.UTC)
		var clips []Metadata
		m := NewManager(Config{
			OutputDir:            filepath.Join(t.TempDir(), "clips"),
			MinNonSilentDuration: time.Second,
			OnFinalized: func(meta Metadata) error {
				clips = append(clips, meta)
				return nil
			},
		})

		require.NoError(t, m.StartManual(idleStatus(), t0))
		require.NoError(t, m.PushPCM([]int16{1000, 1000, 1000}, t0.Add(100*time.Millisecond)))
		require.NoError(t, m.StopManual(t0.Add(200*time.Millisecond)))

		require.Len(t, clips, 1)
		assert.Equal(t, "manual", clips[0].Trigger)
	})
}

func TestManagerSnapshot(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, 3, 8, 10, 0, 0, 0, time.UTC)
	m := NewManager(Config{
		OutputDir: filepath.Join(t.TempDir(), "clips"),
	})

	snap := m.Snapshot()
	assert.False(t, snap.Active)
	assert.False(t, snap.Manual)
	assert.True(t, snap.StartedAt.IsZero())

	require.NoError(t, m.StartManual(idleStatus(), t0))
	snap = m.Snapshot()
	assert.True(t, snap.Active)
	assert.True(t, snap.Manual)
	assert.Equal(t, t0, snap.StartedAt)
	assert.Equal(t, "manual", snap.Trigger)

	require.NoError(t, m.StopManual(t0.Add(2*time.Second)))
	snap = m.Snapshot()
	assert.False(t, snap.Active)
	assert.False(t, snap.Manual)
	assert.True(t, snap.StartedAt.IsZero())
}

func TestManagerIntegrationFlow(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	var clips []Metadata

	m := NewManager(Config{
		OutputDir: dir,
		HangTime:  10 * time.Second,
		OnFinalized: func(meta Metadata) error {
			clips = append(clips, meta)
			return nil
		},
	})

	t0 := time.Date(2026, 3, 8, 10, 0, 0, 0, time.UTC)
	active := activeStatus()
	idle := idleStatus()

	require.NoError(t, m.UpdateTelemetry(active, t0))
	require.NoError(t, m.PushPCM([]int16{1, 2, 3}, t0.Add(time.Second)))
	require.NoError(t, m.UpdateTelemetry(idle, t0.Add(2*time.Second)))
	require.NoError(t, m.UpdateTelemetry(active, t0.Add(8*time.Second)))
	require.NoError(t, m.PushPCM([]int16{4, 5, 6}, t0.Add(9*time.Second)))
	require.NoError(t, m.UpdateTelemetry(idle, t0.Add(10*time.Second)))
	require.NoError(t, m.Tick(t0.Add(21*time.Second)))

	require.Len(t, clips, 1)
	assert.Contains(t, filepath.Base(clips[0].FilePath), "155.2200")
	assert.Positive(t, clips[0].FileSize)

	require.NoError(t, m.UpdateTelemetry(active, t0.Add(40*time.Second)))
	require.NoError(t, m.PushPCM([]int16{7, 8, 9}, t0.Add(41*time.Second)))
	require.NoError(t, m.UpdateTelemetry(idle, t0.Add(42*time.Second)))
	require.NoError(t, m.Tick(t0.Add(53*time.Second)))

	require.Len(t, clips, 2)
}

func TestManagerFinalizeUsesStartContext(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	var clips []Metadata
	m := NewManager(Config{
		OutputDir: dir,
		HangTime:  10 * time.Second,
		OnFinalized: func(meta Metadata) error {
			clips = append(clips, meta)
			return nil
		},
	})

	t0 := time.Date(2026, 3, 8, 10, 0, 0, 0, time.UTC)
	start := sds200.RuntimeStatus{
		Connected:   true,
		SquelchOpen: true,
		Frequency:   "155.2200",
		System:      "County",
		Channel:     "Ops 1",
	}
	require.NoError(t, m.UpdateTelemetry(start, t0))
	require.NoError(t, m.PushPCM([]int16{1, 2, 3}, t0.Add(time.Second)))

	changed := sds200.RuntimeStatus{
		Connected:   true,
		SquelchOpen: false,
		Frequency:   "460.0000",
		System:      "Metro",
		Channel:     "Dispatch 9",
	}
	require.NoError(t, m.UpdateTelemetry(changed, t0.Add(2*time.Second)))
	require.NoError(t, m.Tick(t0.Add(12*time.Second)))

	require.Len(t, clips, 1)
	assert.Equal(t, "155.2200", clips[0].Frequency)
	assert.Equal(t, "County", clips[0].System)
	assert.Equal(t, "Ops 1", clips[0].Channel)
}

func TestManagerOnFinalizedError(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, 3, 8, 10, 0, 0, 0, time.UTC)
	writer := &testWriter{finalizeLen: 10}
	cbErr := errors.New("callback failed")
	m := NewManager(Config{
		OutputDir: filepath.Join(t.TempDir(), "clips"),
		HangTime:  10 * time.Second,
		WriterFactory: func(path string) (PCMWriter, error) {
			return writer, nil
		},
		OnFinalized: func(meta Metadata) error {
			return cbErr
		},
	})

	require.NoError(t, m.UpdateTelemetry(activeStatus(), t0))
	require.NoError(t, m.UpdateTelemetry(idleStatus(), t0.Add(time.Second)))

	err := m.Tick(t0.Add(12 * time.Second))
	require.Error(t, err)
	assert.ErrorIs(t, err, cbErr)
}

func TestSanitizeSegment(t *testing.T) {
	t.Parallel()

	t.Run("special_chars_replaced", func(t *testing.T) {
		t0 := time.Date(2026, 3, 8, 10, 0, 0, 0, time.UTC)
		var finalMeta Metadata
		m := NewManager(Config{
			OutputDir: filepath.Join(t.TempDir(), "clips"),
			HangTime:  10 * time.Second,
			OnFinalized: func(meta Metadata) error {
				finalMeta = meta
				return nil
			},
		})

		status := sds200.RuntimeStatus{
			Connected:   true,
			SquelchOpen: true,
			Frequency:   "155.2200",
			System:      "System/Name",
			Channel:     "Ch:1",
		}
		require.NoError(t, m.UpdateTelemetry(status, t0))
		require.NoError(t, m.UpdateTelemetry(idleStatus(), t0.Add(time.Second)))
		require.NoError(t, m.Tick(t0.Add(12*time.Second)))

		name := filepath.Base(finalMeta.FilePath)
		assert.NotContains(t, name, "/")
		assert.NotContains(t, name, ":")
		assert.Contains(t, name, "system_name")
		assert.Contains(t, name, "ch_1")
	})

	t.Run("empty_labels_use_fallback", func(t *testing.T) {
		t0 := time.Date(2026, 3, 8, 10, 0, 0, 0, time.UTC)
		var finalMeta Metadata
		m := NewManager(Config{
			OutputDir: filepath.Join(t.TempDir(), "clips"),
			HangTime:  10 * time.Second,
			OnFinalized: func(meta Metadata) error {
				finalMeta = meta
				return nil
			},
		})

		status := sds200.RuntimeStatus{
			Connected:   true,
			SquelchOpen: true,
		}
		require.NoError(t, m.UpdateTelemetry(status, t0))
		require.NoError(t, m.UpdateTelemetry(sds200.RuntimeStatus{Connected: true}, t0.Add(time.Second)))
		require.NoError(t, m.Tick(t0.Add(12*time.Second)))

		name := filepath.Base(finalMeta.FilePath)
		assert.Contains(t, name, "unknown_frequency")
		assert.Contains(t, name, "unknown_system")
		assert.Contains(t, name, "unknown_channel")
	})
}

func TestIsActive(t *testing.T) {
	t.Parallel()

	t.Run("squelch_open_is_active", func(t *testing.T) {
		t0 := time.Date(2026, 3, 8, 10, 0, 0, 0, time.UTC)
		writer := &testWriter{}
		m := NewManager(Config{
			OutputDir: filepath.Join(t.TempDir(), "clips"),
			WriterFactory: func(path string) (PCMWriter, error) {
				return writer, nil
			},
		})

		status := sds200.RuntimeStatus{Connected: true, SquelchOpen: true}
		require.NoError(t, m.UpdateTelemetry(status, t0))
		assert.NotNil(t, m.writer)
	})

	t.Run("signal_no_mute_is_active", func(t *testing.T) {
		t0 := time.Date(2026, 3, 8, 10, 0, 0, 0, time.UTC)
		writer := &testWriter{}
		m := NewManager(Config{
			OutputDir: filepath.Join(t.TempDir(), "clips"),
			WriterFactory: func(path string) (PCMWriter, error) {
				return writer, nil
			},
		})

		status := sds200.RuntimeStatus{Connected: true, Signal: 1, Mute: false, SquelchOpen: false}
		require.NoError(t, m.UpdateTelemetry(status, t0))
		assert.NotNil(t, m.writer)
	})

	t.Run("p25_data_is_inactive", func(t *testing.T) {
		t0 := time.Date(2026, 3, 8, 10, 0, 0, 0, time.UTC)
		writer := &testWriter{}
		m := NewManager(Config{
			OutputDir: filepath.Join(t.TempDir(), "clips"),
			WriterFactory: func(path string) (PCMWriter, error) {
				return writer, nil
			},
		})

		status := sds200.RuntimeStatus{
			Connected:   true,
			Signal:      4,
			SquelchOpen: true,
			P25Status:   "Data",
		}
		require.NoError(t, m.UpdateTelemetry(status, t0))
		assert.Nil(t, m.writer)
	})

	t.Run("disconnected_is_inactive", func(t *testing.T) {
		t0 := time.Date(2026, 3, 8, 10, 0, 0, 0, time.UTC)
		writer := &testWriter{}
		m := NewManager(Config{
			OutputDir: filepath.Join(t.TempDir(), "clips"),
			WriterFactory: func(path string) (PCMWriter, error) {
				return writer, nil
			},
		})

		status := sds200.RuntimeStatus{Connected: false, SquelchOpen: true}
		require.NoError(t, m.UpdateTelemetry(status, t0))
		assert.Nil(t, m.writer)
	})
}
