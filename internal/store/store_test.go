package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStoreLoad(t *testing.T) {
	t.Parallel()

	t.Run("missing_returns_defaults", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "runtime.yaml")
		s := New(path)

		doc, err := s.Load()
		require.NoError(t, err)

		assert.Equal(t, CurrentVersion, doc.Version)
		assert.Equal(t, 50536, doc.Config.Scanner.ControlPort)
		assert.Equal(t, 554, doc.Config.Scanner.RTSPPort)
		assert.Equal(t, 10, doc.Config.Recording.HangTimeSeconds)
		assert.Equal(t, 20, doc.Config.Recording.MinAutoDurationSeconds)
		assert.Equal(t, 150, doc.Config.Activity.StartDebounceMS)
		assert.Equal(t, 600, doc.Config.Activity.EndDebounceMS)
		assert.Equal(t, 300, doc.Config.Activity.MinActivityMS)
		assert.InDelta(t, 0.0, doc.Config.AudioMonitor.GainDB, 0.000001)
		assert.Equal(t, "system-default", doc.Config.AudioMonitor.OutputDevice)
		assert.Empty(t, doc.State.Favorites)
		assert.Empty(t, doc.State.Recordings)
		assert.Empty(t, doc.State.ScanProfiles)
	})

	t.Run("corrupt_yaml_error", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "runtime.yaml")
		require.NoError(t, os.WriteFile(path, []byte("{{invalid yaml"), 0o644))

		s := New(path)
		_, err := s.Load()
		require.Error(t, err)
	})

	t.Run("invalid_applies_defaults", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "runtime.yaml")
		raw := []byte("version: 1\nconfig:\n  scanner:\n    ip: not-an-ip\n")
		require.NoError(t, os.WriteFile(path, raw, 0o644))

		s := New(path)
		doc, err := s.Load()
		require.NoError(t, err)
		assert.Equal(t, CurrentVersion, doc.Version)
		assert.Equal(t, "not-an-ip", doc.Config.Scanner.IP)
		assert.Equal(t, 50536, doc.Config.Scanner.ControlPort)
	})
}

func TestStoreSave(t *testing.T) {
	t.Run("save_then_load", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "runtime.yaml")
		s := New(path)

		doc := defaultDocument()
		doc.Config.Scanner.IP = "192.168.1.50"
		doc.Config.Storage.RecordingsPath = "/tmp/recs"
		doc.State.Favorites = []Favorite{{Name: "Local PD", Frequency: "155.190"}}
		doc.State.Recordings = []RecordingEntry{{
			ID:        "1",
			StartedAt: "2026-03-08T10:00:00Z",
			EndedAt:   "2026-03-08T10:00:12Z",
			Duration:  "12s",
			Frequency: "155.1900",
			System:    "County",
			Channel:   "Dispatch",
			FilePath:  "/tmp/recs/clip.flac",
			FileSize:  12345,
			Trigger:   "telemetry",
		}}
		doc.State.ScanProfiles = []ScanProfile{{
			Name:               "default",
			FavoritesQuickKeys: []int{1, 0, 1},
			ServiceTypes:       []int{1, 1, 0},
			UpdatedAt:          "2026-03-08T16:00:00Z",
		}}

		require.NoError(t, s.Save(doc))

		loaded, err := s.Load()
		require.NoError(t, err)

		assert.Equal(t, "192.168.1.50", loaded.Config.Scanner.IP)
		assert.Equal(t, "/tmp/recs", loaded.Config.Storage.RecordingsPath)
		require.Len(t, loaded.State.Favorites, 1)
		assert.Equal(t, "Local PD", loaded.State.Favorites[0].Name)
		require.Len(t, loaded.State.Recordings, 1)
		assert.Equal(t, "telemetry", loaded.State.Recordings[0].Trigger)
		require.Len(t, loaded.State.ScanProfiles, 1)
		assert.Equal(t, "default", loaded.State.ScanProfiles[0].Name)
	})

	t.Run("empty_path_uses_default", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		s := New("")

		doc := defaultDocument()
		doc.Config.Scanner.IP = "10.0.0.10"
		require.NoError(t, s.Save(doc))

		defaultPath := filepath.Join(home, ".sigsentinel", "config.yaml")
		_, err := os.Stat(defaultPath)
		require.NoError(t, err)

		loaded, err := s.Load()
		require.NoError(t, err)
		assert.Equal(t, "10.0.0.10", loaded.Config.Scanner.IP)
	})
}

func TestStoreAppendRecording(t *testing.T) {
	t.Parallel()

	t.Run("appends_recording_entry", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "runtime.yaml")
		s := New(path)
		doc := defaultDocument()
		doc.Config.Scanner.IP = "10.0.0.20"
		require.NoError(t, s.Save(doc))

		entry := RecordingEntry{
			ID:        "clip-1",
			StartedAt: "2026-03-08T10:00:00Z",
			EndedAt:   "2026-03-08T10:00:05Z",
			Duration:  "5s",
			FilePath:  "/tmp/recs/clip-1.flac",
			FileSize:  101,
			Trigger:   "telemetry",
		}
		require.NoError(t, s.AppendRecording(entry))

		loaded, err := s.Load()
		require.NoError(t, err)
		require.Len(t, loaded.State.Recordings, 1)
		assert.Equal(t, "clip-1", loaded.State.Recordings[0].ID)
	})
}
