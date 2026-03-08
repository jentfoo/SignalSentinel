package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testScannerIP = "10.0.1.10"

func DefaultDocument() *Document {
	d := &Document{}
	d.ApplyDefaults()
	return d
}

func TestStoreLoadAndSave(t *testing.T) {
	t.Run("load_missing_returns_defaults", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "runtime.yaml")
		s := New(path)

		doc, err := s.Load()
		require.NoError(t, err)

		assert.Equal(t, CurrentVersion, doc.Version)
		assert.Equal(t, 50536, doc.Config.Scanner.ControlPort)
		assert.Equal(t, 554, doc.Config.Scanner.RTSPPort)
		assert.Equal(t, 10, doc.Config.Recording.HangTimeSeconds)
		assert.Empty(t, doc.State.Favorites)
		assert.Empty(t, doc.State.Recordings)
	})

	t.Run("save_then_load_roundtrip", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "runtime.yaml")
		s := New(path)

		doc := DefaultDocument()
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

		err := s.Save(doc)
		require.NoError(t, err)

		loaded, err := s.Load()
		require.NoError(t, err)

		assert.Equal(t, "192.168.1.50", loaded.Config.Scanner.IP)
		assert.Equal(t, "/tmp/recs", loaded.Config.Storage.RecordingsPath)
		require.Len(t, loaded.State.Favorites, 1)
		assert.Equal(t, "Local PD", loaded.State.Favorites[0].Name)
		require.Len(t, loaded.State.Recordings, 1)
		assert.Equal(t, "telemetry", loaded.State.Recordings[0].Trigger)
	})

	t.Run("empty_path_uses_default", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		s := New("")

		doc := DefaultDocument()
		doc.Config.Scanner.IP = "10.0.0.10"

		err := s.Save(doc)
		require.NoError(t, err)

		defaultPath := filepath.Join(home, ".sigsentinel", "config.yaml")
		_, err = os.Stat(defaultPath)
		require.NoError(t, err)

		loaded, err := s.Load()
		require.NoError(t, err)
		assert.Equal(t, "10.0.0.10", loaded.Config.Scanner.IP)
	})

	t.Run("append_recording_persists_entry", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "runtime.yaml")
		s := New(path)
		doc := DefaultDocument()
		doc.Config.Scanner.IP = "10.0.0.20"
		require.NoError(t, s.Save(doc))

		err := s.AppendRecording(RecordingEntry{
			ID:        "clip-1",
			StartedAt: "2026-03-08T10:00:00Z",
			EndedAt:   "2026-03-08T10:00:05Z",
			Duration:  "5s",
			FilePath:  "/tmp/recs/clip-1.flac",
			FileSize:  101,
			Trigger:   "telemetry",
		})
		require.NoError(t, err)

		loaded, err := s.Load()
		require.NoError(t, err)
		require.Len(t, loaded.State.Recordings, 1)
		assert.Equal(t, "clip-1", loaded.State.Recordings[0].ID)
	})

	t.Run("corrupt_yaml_returns_error", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "runtime.yaml")
		require.NoError(t, os.WriteFile(path, []byte("{{invalid yaml"), 0o644))

		s := New(path)
		_, err := s.Load()
		require.Error(t, err)
	})

	t.Run("load_existing_invalid_document_applies_defaults_without_validation", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "runtime.yaml")
		raw := []byte("version: 1\nconfig:\n  scanner:\n    ip: not-an-ip\n")
		require.NoError(t, os.WriteFile(path, raw, 0o644))

		s := New(path)
		doc, err := s.Load()
		require.NoError(t, err)
		assert.Equal(t, "not-an-ip", doc.Config.Scanner.IP)
		assert.Equal(t, 50536, doc.Config.Scanner.ControlPort)
	})
}

func TestDocumentValidate(t *testing.T) {
	t.Parallel()

	t.Run("valid_document", func(t *testing.T) {
		doc := DefaultDocument()
		doc.Config.Scanner.IP = testScannerIP

		err := doc.Validate()
		assert.NoError(t, err)
	})

	t.Run("invalid_scanner_ip", func(t *testing.T) {
		doc := DefaultDocument()
		doc.Config.Scanner.IP = "not-an-ip"

		err := doc.Validate()
		assert.Error(t, err)
	})

	t.Run("invalid_port", func(t *testing.T) {
		doc := DefaultDocument()
		doc.Config.Scanner.IP = "10.0.2.10"
		doc.Config.Scanner.ControlPort = 70000

		err := doc.Validate()
		assert.Error(t, err)
	})

	t.Run("nil_document", func(t *testing.T) {
		var doc *Document
		err := doc.Validate()
		require.Error(t, err)
	})

	t.Run("wrong_version", func(t *testing.T) {
		doc := DefaultDocument()
		doc.Config.Scanner.IP = testScannerIP
		doc.Version = 99

		err := doc.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "version")
	})

	t.Run("invalid_rtsp_port", func(t *testing.T) {
		doc := DefaultDocument()
		doc.Config.Scanner.IP = testScannerIP
		doc.Config.Scanner.RTSPPort = 70000

		err := doc.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "port")
	})

	t.Run("empty_recordings_path", func(t *testing.T) {
		doc := DefaultDocument()
		doc.Config.Scanner.IP = testScannerIP
		doc.Config.Storage.RecordingsPath = ""

		err := doc.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "path")
	})

	t.Run("invalid_hang_time", func(t *testing.T) {
		doc := DefaultDocument()
		doc.Config.Scanner.IP = testScannerIP
		doc.Config.Recording.HangTimeSeconds = 0

		err := doc.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "hang")
	})
}

func TestApplyDefaults(t *testing.T) {
	t.Parallel()

	t.Run("preserves_user_values", func(t *testing.T) {
		doc := &Document{}
		doc.Config.Scanner.ControlPort = 12345
		doc.Config.Recording.HangTimeSeconds = 5
		doc.ApplyDefaults()

		assert.Equal(t, 12345, doc.Config.Scanner.ControlPort)
		assert.Equal(t, 5, doc.Config.Recording.HangTimeSeconds)
	})

	t.Run("fills_zero_values", func(t *testing.T) {
		doc := &Document{}
		doc.ApplyDefaults()

		assert.Equal(t, CurrentVersion, doc.Version)
		assert.Equal(t, 50536, doc.Config.Scanner.ControlPort)
		assert.Equal(t, 554, doc.Config.Scanner.RTSPPort)
		assert.Equal(t, "recordings", doc.Config.Storage.RecordingsPath)
		assert.Equal(t, 10, doc.Config.Recording.HangTimeSeconds)
	})
}
