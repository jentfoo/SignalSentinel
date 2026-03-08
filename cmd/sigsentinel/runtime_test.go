package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jentfoo/SignalSentinel/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRuntimeSaveConfig(t *testing.T) {
	t.Parallel()

	t.Run("rejects_missing_store", func(t *testing.T) {
		runtime := &Runtime{}
		err := runtime.SaveConfig(validDocument())
		require.Error(t, err)
		assert.Equal(t, "runtime store unavailable", err.Error())
	})

	t.Run("rejects_nil_document", func(t *testing.T) {
		runtime := &Runtime{store: store.New(filepath.Join(t.TempDir(), "config.yaml"))}
		err := runtime.SaveConfig(nil)
		require.Error(t, err)
		assert.Equal(t, "config is required", err.Error())
	})

	t.Run("rejects_invalid_document", func(t *testing.T) {
		runtime := &Runtime{store: store.New(filepath.Join(t.TempDir(), "config.yaml"))}
		doc := validDocument()
		doc.Config.Scanner.IP = "not_an_ip"

		err := runtime.SaveConfig(doc)
		require.Error(t, err)
		assert.Equal(t, "scanner ip is invalid", err.Error())
	})

	t.Run("persists_updated_config", func(t *testing.T) {
		configPath := filepath.Join(t.TempDir(), "config.yaml")
		s := store.New(configPath)
		runtime := &Runtime{store: s}
		doc := validDocument()
		doc.Config.Storage.RecordingsPath = "clips"
		doc.Config.Scanner.IP = "127.0.0.2"

		require.NoError(t, runtime.SaveConfig(doc))

		wantRecordingsPath := filepath.Join(filepath.Dir(configPath), "clips")
		assert.Equal(t, wantRecordingsPath, runtime.RecordingsPath())
		require.NotNil(t, runtime.Config())
		assert.Equal(t, "127.0.0.2", runtime.Config().Config.Scanner.IP)
		assert.DirExists(t, wantRecordingsPath)

		loaded, err := s.Load()
		require.NoError(t, err)
		assert.Equal(t, "127.0.0.2", loaded.Config.Scanner.IP)
		assert.Equal(t, "clips", loaded.Config.Storage.RecordingsPath)
	})
}

func TestRuntimeRecordings(t *testing.T) {
	t.Parallel()

	t.Run("rejects_missing_store", func(t *testing.T) {
		runtime := &Runtime{}
		recordings, err := runtime.Recordings()
		require.Error(t, err)
		assert.Nil(t, recordings)
		assert.Equal(t, "runtime store unavailable", err.Error())
	})

	t.Run("returns_recordings_copy", func(t *testing.T) {
		s := store.New(filepath.Join(t.TempDir(), "config.yaml"))
		doc := validDocument()
		doc.State.Recordings = []store.RecordingEntry{
			{ID: "rec-1", FilePath: "/tmp/one.flac", Trigger: "voice", Duration: "1s"},
		}
		require.NoError(t, s.Save(doc))

		runtime := &Runtime{store: s, doc: cloneDocument(doc)}
		recordings, err := runtime.Recordings()
		require.NoError(t, err)
		require.Len(t, recordings, 1)

		recordings[0].ID = "mutated"
		recordingsAgain, err := runtime.Recordings()
		require.NoError(t, err)
		require.Len(t, recordingsAgain, 1)
		assert.Equal(t, "rec-1", recordingsAgain[0].ID)
	})
}

func TestRuntimeAppendRecordingPreservedAcrossConfigUpdate(t *testing.T) {
	t.Parallel()

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	s := store.New(configPath)
	runtime := &Runtime{store: s}
	doc := validDocument()
	doc.Config.Storage.RecordingsPath = "clips"
	require.NoError(t, runtime.SaveConfig(doc))

	entry := store.RecordingEntry{
		ID:        "rec-42",
		StartedAt: "2026-03-08T10:00:00Z",
		EndedAt:   "2026-03-08T10:00:05Z",
		Duration:  "5s",
		FilePath:  "/tmp/rec-42.flac",
		FileSize:  42,
		Trigger:   "telemetry",
	}
	require.NoError(t, runtime.AppendRecording(entry))

	require.NoError(t, runtime.UpdateConfig(func(doc *store.Document) error {
		doc.Config.Scanner.IP = "127.0.0.2"
		return nil
	}))

	recordings, err := runtime.Recordings()
	require.NoError(t, err)
	require.Len(t, recordings, 1)
	assert.Equal(t, "rec-42", recordings[0].ID)

	loaded, err := s.Load()
	require.NoError(t, err)
	require.Len(t, loaded.State.Recordings, 1)
	assert.Equal(t, "rec-42", loaded.State.Recordings[0].ID)
	assert.Equal(t, "127.0.0.2", loaded.Config.Scanner.IP)
}

func TestRuntimeEnqueueControl(t *testing.T) {
	t.Parallel()

	t.Run("handles_nil_receiver", func(t *testing.T) {
		var runtime *Runtime
		assert.NotPanics(t, func() {
			runtime.EnqueueControl(IntentHold)
		})
	})

	t.Run("handles_nil_session", func(t *testing.T) {
		runtime := &Runtime{}
		assert.NotPanics(t, func() {
			runtime.EnqueueControl(IntentResumeScan)
		})
	})

	t.Run("forwards_control_intent", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		runtime := &Runtime{
			session: &ScannerSession{
				ctx:       ctx,
				controlCh: make(chan ControlIntent, 1),
			},
		}

		runtime.EnqueueControl(IntentResumeScan)
		assert.Equal(t, IntentResumeScan, requireControlIntent(t, runtime.session.controlCh))
	})
}

func TestResolveRecordingsPath(t *testing.T) {
	t.Parallel()

	t.Run("keeps_absolute_path", func(t *testing.T) {
		absolutePath := filepath.Join(t.TempDir(), "clips")
		doc := validDocument()
		doc.Config.Storage.RecordingsPath = absolutePath

		resolved, err := resolveRecordingsPath(doc, store.New(filepath.Join(t.TempDir(), "config.yaml")))
		require.NoError(t, err)
		assert.Equal(t, absolutePath, resolved)
	})

	t.Run("joins_relative_path", func(t *testing.T) {
		configPath := filepath.Join(t.TempDir(), "config.yaml")
		doc := validDocument()
		doc.Config.Storage.RecordingsPath = "clips"

		resolved, err := resolveRecordingsPath(doc, store.New(configPath))
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(filepath.Dir(configPath), "clips"), resolved)
	})
}

func TestEnsureDirectoryWritable(t *testing.T) {
	t.Parallel()

	t.Run("creates_missing_directory", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "nested", "clips")
		require.NoError(t, ensureDirectoryWritable(path))
		assert.DirExists(t, path)
	})

	t.Run("rejects_file_path", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "not_a_directory")
		require.NoError(t, os.WriteFile(path, []byte("x"), 0o644))
		err := ensureDirectoryWritable(path)
		require.Error(t, err)
	})
}

func validDocument() *store.Document {
	doc := &store.Document{
		Version: store.CurrentVersion,
		Config: store.Config{
			Scanner: store.ScannerConfig{
				IP:          "127.0.0.1",
				ControlPort: 50536,
				RTSPPort:    554,
			},
			Storage: store.StorageConfig{
				RecordingsPath: "recordings",
			},
			Recording: store.RecordingConfig{
				HangTimeSeconds: 10,
			},
		},
	}
	doc.ApplyDefaults()
	return doc
}

func requireControlIntent(t *testing.T, ch <-chan ControlIntent) ControlIntent {
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
