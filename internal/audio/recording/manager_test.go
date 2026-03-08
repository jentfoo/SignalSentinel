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
	if w.finalizeErr != nil {
		return 0, w.finalizeErr
	}
	w.finalized = true
	return w.finalizeLen, nil
}

func (w *testWriter) Abort() error {
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
