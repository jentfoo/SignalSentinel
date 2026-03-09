package gui

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAppendActivity(t *testing.T) {
	t.Parallel()

	t.Run("starts_session_after_start_debounce", func(t *testing.T) {
		model := &uiModel{activity: ActivitySettings{StartDebounceMS: 100, EndDebounceMS: 200, MinActivityMS: 300}}
		t0 := time.Date(2026, 3, 8, 10, 0, 0, 0, time.UTC)

		appendActivity(model, RuntimeState{Scanner: ScannerStatus{Connected: true, UpdatedAt: t0}})
		appendActivity(model, RuntimeState{Scanner: ScannerStatus{
			Connected: true,
			Active:    true,
			Frequency: "155.2200",
			System:    "County",
			Channel:   "Ops",
			UpdatedAt: t0.Add(time.Second),
		}})
		assert.Empty(t, model.activities)

		appendActivity(model, RuntimeState{Scanner: ScannerStatus{
			Connected: true,
			Active:    true,
			Frequency: "155.2200",
			System:    "County",
			Channel:   "Ops",
			UpdatedAt: t0.Add(time.Second + 150*time.Millisecond),
		}})

		require.Len(t, model.activities, 1)
		assert.Contains(t, model.activities[0], "active")
		assert.Contains(t, model.activities[0], "155.2200 MHz")
	})

	t.Run("finalizes_after_end_debounce", func(t *testing.T) {
		model := &uiModel{activity: ActivitySettings{StartDebounceMS: 100, EndDebounceMS: 200, MinActivityMS: 50}}
		t0 := time.Date(2026, 3, 8, 10, 0, 0, 0, time.UTC)

		appendActivity(model, RuntimeState{Scanner: ScannerStatus{Connected: true, Active: true, UpdatedAt: t0}})
		appendActivity(model, RuntimeState{Scanner: ScannerStatus{Connected: true, Active: true, UpdatedAt: t0.Add(150 * time.Millisecond)}})
		appendActivity(model, RuntimeState{Scanner: ScannerStatus{Connected: true, Active: false, UpdatedAt: t0.Add(500 * time.Millisecond)}})

		require.Len(t, model.activities, 1)
		assert.Contains(t, model.activities[0], "active")

		appendActivity(model, RuntimeState{Scanner: ScannerStatus{Connected: true, Active: false, UpdatedAt: t0.Add(750 * time.Millisecond)}})
		require.Len(t, model.activities, 1)
		assert.Contains(t, model.activities[0], "->")
		assert.NotContains(t, model.activities[0], "active")
	})

	t.Run("adds_disconnect_event", func(t *testing.T) {
		model := &uiModel{activity: ActivitySettings{StartDebounceMS: 100, EndDebounceMS: 100, MinActivityMS: 100}}
		t0 := time.Date(2026, 3, 8, 10, 0, 0, 0, time.UTC)

		appendActivity(model, RuntimeState{Scanner: ScannerStatus{Connected: true, UpdatedAt: t0}})
		appendActivity(model, RuntimeState{Scanner: ScannerStatus{Connected: false, UpdatedAt: t0.Add(time.Second)}})

		require.Len(t, model.activities, 1)
		assert.Contains(t, model.activities[0], "scanner disconnected")
	})

	t.Run("disconnect_finalizes_session", func(t *testing.T) {
		model := &uiModel{activity: ActivitySettings{StartDebounceMS: 100, EndDebounceMS: 100, MinActivityMS: 50}}
		t0 := time.Date(2026, 3, 8, 10, 0, 0, 0, time.UTC)

		appendActivity(model, RuntimeState{Scanner: ScannerStatus{Connected: true, Active: true, UpdatedAt: t0}})
		appendActivity(model, RuntimeState{Scanner: ScannerStatus{Connected: true, Active: true, UpdatedAt: t0.Add(150 * time.Millisecond)}})
		appendActivity(model, RuntimeState{Scanner: ScannerStatus{Connected: false, UpdatedAt: t0.Add(400 * time.Millisecond)}})

		require.Len(t, model.activities, 2)
		assert.Contains(t, model.activities[0], "scanner disconnected")
		assert.Contains(t, model.activities[1], "->")
	})

	t.Run("suppresses_short_sessions", func(t *testing.T) {
		model := &uiModel{activity: ActivitySettings{StartDebounceMS: 100, EndDebounceMS: 100, MinActivityMS: 1000}}
		t0 := time.Date(2026, 3, 8, 10, 0, 0, 0, time.UTC)

		appendActivity(model, RuntimeState{Scanner: ScannerStatus{Connected: true, UpdatedAt: t0}})
		appendActivity(model, RuntimeState{Scanner: ScannerStatus{
			Connected: true,
			Active:    true,
			Frequency: "460.5500",
			System:    "Metro",
			Channel:   "Dispatch",
			UpdatedAt: t0.Add(time.Second),
		}})
		appendActivity(model, RuntimeState{Scanner: ScannerStatus{
			Connected: true,
			Active:    true,
			Frequency: "460.5500",
			System:    "Metro",
			Channel:   "Dispatch",
			UpdatedAt: t0.Add(time.Second + 150*time.Millisecond),
		}})
		appendActivity(model, RuntimeState{Scanner: ScannerStatus{Connected: true, Active: false, UpdatedAt: t0.Add(time.Second + 400*time.Millisecond)}})
		appendActivity(model, RuntimeState{Scanner: ScannerStatus{Connected: true, Active: false, UpdatedAt: t0.Add(time.Second + 600*time.Millisecond)}})

		assert.Empty(t, model.activities)
		require.Len(t, model.suppressed, 1)
		assert.Contains(t, model.suppressed[0], "duration below minimum")
	})

	t.Run("suppresses_pre_debounce_blips", func(t *testing.T) {
		model := &uiModel{activity: ActivitySettings{StartDebounceMS: 300, EndDebounceMS: 100, MinActivityMS: 100}}
		t0 := time.Date(2026, 3, 8, 10, 0, 0, 0, time.UTC)

		appendActivity(model, RuntimeState{Scanner: ScannerStatus{Connected: true, Active: true, UpdatedAt: t0}})
		appendActivity(model, RuntimeState{Scanner: ScannerStatus{Connected: true, Active: false, UpdatedAt: t0.Add(100 * time.Millisecond)}})

		assert.Empty(t, model.activities)
		require.Len(t, model.suppressed, 1)
		assert.Contains(t, model.suppressed[0], "start debounce not met")
	})

	t.Run("promotes_pending_with_samples", func(t *testing.T) {
		model := &uiModel{activity: ActivitySettings{StartDebounceMS: 300, EndDebounceMS: 100, MinActivityMS: 100}}
		t0 := time.Date(2026, 3, 8, 10, 0, 0, 0, time.UTC)

		appendActivity(model, RuntimeState{Scanner: ScannerStatus{Connected: true, Active: true, UpdatedAt: t0}})
		appendActivity(model, RuntimeState{Scanner: ScannerStatus{Connected: true, Active: true, UpdatedAt: t0.Add(100 * time.Millisecond)}})
		appendActivity(model, RuntimeState{Scanner: ScannerStatus{Connected: true, Active: false, UpdatedAt: t0.Add(450 * time.Millisecond)}})
		appendActivity(model, RuntimeState{Scanner: ScannerStatus{Connected: true, Active: false, UpdatedAt: t0.Add(600 * time.Millisecond)}})

		require.Len(t, model.activities, 1)
		assert.Contains(t, model.activities[0], "->")
		assert.Empty(t, model.suppressed)
	})

	t.Run("single_sample_suppressed", func(t *testing.T) {
		model := &uiModel{activity: ActivitySettings{StartDebounceMS: 300, EndDebounceMS: 100, MinActivityMS: 100}}
		t0 := time.Date(2026, 3, 8, 10, 0, 0, 0, time.UTC)

		appendActivity(model, RuntimeState{Scanner: ScannerStatus{Connected: true, Active: true, UpdatedAt: t0}})
		appendActivity(model, RuntimeState{Scanner: ScannerStatus{Connected: true, Active: false, UpdatedAt: t0.Add(500 * time.Millisecond)}})

		assert.Empty(t, model.activities)
		require.Len(t, model.suppressed, 1)
		assert.Contains(t, model.suppressed[0], "insufficient active samples")
	})

	t.Run("disconnect_uses_pending_end", func(t *testing.T) {
		model := &uiModel{activity: ActivitySettings{StartDebounceMS: 100, EndDebounceMS: 800, MinActivityMS: 100}}
		t0 := time.Date(2026, 3, 8, 10, 0, 0, 0, time.UTC)

		appendActivity(model, RuntimeState{Scanner: ScannerStatus{Connected: true, Active: true, UpdatedAt: t0}})
		appendActivity(model, RuntimeState{Scanner: ScannerStatus{Connected: true, Active: true, UpdatedAt: t0.Add(150 * time.Millisecond)}})
		appendActivity(model, RuntimeState{Scanner: ScannerStatus{Connected: true, Active: false, UpdatedAt: t0.Add(time.Second)}})
		appendActivity(model, RuntimeState{Scanner: ScannerStatus{Connected: false, UpdatedAt: t0.Add(2 * time.Second)}})

		require.Len(t, model.activities, 2)
		assert.Contains(t, model.activities[1], "-> 2026-03-08 10:00:01 | 1s")
		assert.NotContains(t, model.activities[1], "| 2s")
	})

	t.Run("caps_activity_length", func(t *testing.T) {
		model := &uiModel{
			initialized:     true,
			lastConnected:   true,
			activity:        ActivitySettings{StartDebounceMS: 100, EndDebounceMS: 100, MinActivityMS: 100},
			sessionRowIndex: -1,
		}
		for i := 0; i < 200; i++ {
			model.activities = append(model.activities, "old_event")
		}
		t0 := time.Date(2026, 3, 8, 10, 0, 0, 0, time.UTC)

		appendActivity(model, RuntimeState{Scanner: ScannerStatus{Connected: false, UpdatedAt: t0}})

		require.Len(t, model.activities, 200)
		assert.Contains(t, model.activities[0], "scanner disconnected")
		assert.Equal(t, "old_event", model.activities[len(model.activities)-1])
	})
}

func TestRecordingsEqual(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		a    []Recording
		b    []Recording
		want bool
	}{
		{
			name: "equal_recording_slices",
			a: []Recording{
				{ID: "1", Channel: "A"},
			},
			b: []Recording{
				{ID: "1", Channel: "A"},
			},
			want: true,
		},
		{
			name: "different_slice_length",
			a:    []Recording{{ID: "1"}},
			b:    []Recording{},
			want: false,
		},
		{
			name: "different_slice_values",
			a:    []Recording{{ID: "1", Channel: "A"}},
			b:    []Recording{{ID: "1", Channel: "B"}},
			want: false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, recordingsEqual(tt.a, tt.b))
		})
	}
}

func TestFormatRecording(t *testing.T) {
	t.Parallel()

	t.Run("uses_base_file_name", func(t *testing.T) {
		formatted := formatRecording(Recording{
			StartedAt: "2026-03-08T10:00:00Z",
			Duration:  "3s",
			Frequency: "155.1300",
			System:    "County",
			Channel:   "Fire Ops",
			FilePath:  "/tmp/recordings/call-001.flac",
		})
		assert.Equal(t, "2026-03-08T10:00:00Z | 3s | 155.1300 MHz | County / Fire Ops | call-001.flac", formatted)
	})

	t.Run("uses_dash_for_empty", func(t *testing.T) {
		formatted := formatRecording(Recording{FilePath: "/tmp/clip.flac"})
		assert.True(t, strings.HasPrefix(formatted, "- | - | - | - / - | clip.flac"))
	})

	t.Run("uses_placeholder_no_file", func(t *testing.T) {
		formatted := formatRecording(Recording{
			StartedAt: "2026-03-08T10:00:00Z",
			Duration:  "3s",
			Frequency: "155.1300",
			System:    "County",
			Channel:   "Fire Ops",
			FilePath:  "   ",
		})
		assert.Equal(t, "2026-03-08T10:00:00Z | 3s | 155.1300 MHz | County / Fire Ops | (no file)", formatted)
	})
}

func TestBoolWord(t *testing.T) {
	t.Parallel()

	t.Run("returns_yes_for_true", func(t *testing.T) {
		assert.Equal(t, "open", boolWord(true, "open", "closed"))
	})

	t.Run("returns_no_for_false", func(t *testing.T) {
		assert.Equal(t, "closed", boolWord(false, "open", "closed"))
	})
}

func TestOrDash(t *testing.T) {
	t.Parallel()

	t.Run("returns_dash_for_blank", func(t *testing.T) {
		assert.Equal(t, "-", orDash("   "))
	})

	t.Run("returns_value_for_text", func(t *testing.T) {
		assert.Equal(t, "County", orDash("County"))
	})
}

func TestFormatFrequency(t *testing.T) {
	t.Parallel()

	t.Run("adds_mhz_suffix", func(t *testing.T) {
		assert.Equal(t, "155.1300 MHz", formatFrequency("155.1300"))
	})

	t.Run("preserves_existing_units", func(t *testing.T) {
		assert.Equal(t, "155.1300 MHz", formatFrequency("155.1300 MHz"))
	})

	t.Run("blank_returns_dash", func(t *testing.T) {
		assert.Equal(t, "-", formatFrequency("   "))
	})
}

func TestFormatSystemChannel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		system  string
		channel string
		want    string
	}{
		{name: "both_values", system: "County", channel: "Dispatch", want: "County / Dispatch"},
		{name: "missing_system", system: "", channel: "Dispatch", want: "- / Dispatch"},
		{name: "missing_channel", system: "County", channel: "", want: "County / -"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, formatSystemChannel(tt.system, tt.channel))
		})
	}
}
