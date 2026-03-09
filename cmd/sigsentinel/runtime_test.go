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

const testRecordingsPath = "clips"

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
		doc.Config.Storage.RecordingsPath = testRecordingsPath
		doc.Config.Scanner.IP = "127.0.0.2"

		require.NoError(t, runtime.SaveConfig(doc))

		wantRecordingsPath := filepath.Join(filepath.Dir(configPath), testRecordingsPath)
		assert.Equal(t, wantRecordingsPath, runtime.RecordingsPath())
		require.NotNil(t, runtime.Config())
		assert.Equal(t, "127.0.0.2", runtime.Config().Config.Scanner.IP)
		assert.DirExists(t, wantRecordingsPath)

		loaded, err := s.Load()
		require.NoError(t, err)
		assert.Equal(t, "127.0.0.2", loaded.Config.Scanner.IP)
		assert.Equal(t, testRecordingsPath, loaded.Config.Storage.RecordingsPath)
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

func TestRuntimeAppendRecording(t *testing.T) {
	t.Parallel()

	t.Run("preserved_after_update", func(t *testing.T) {
		configPath := filepath.Join(t.TempDir(), "config.yaml")
		s := store.New(configPath)
		runtime := &Runtime{store: s}
		doc := validDocument()
		doc.Config.Storage.RecordingsPath = testRecordingsPath
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
	})
}

func TestRuntimeDeleteRecordingsByID(t *testing.T) {
	t.Parallel()

	t.Run("deletes_file_and_metadata", func(t *testing.T) {
		configPath := filepath.Join(t.TempDir(), "config.yaml")
		s := store.New(configPath)
		runtime := &Runtime{store: s}
		doc := validDocument()
		doc.Config.Storage.RecordingsPath = testRecordingsPath
		require.NoError(t, runtime.SaveConfig(doc))

		clipPath := filepath.Join(t.TempDir(), "clip-1.flac")
		require.NoError(t, os.WriteFile(clipPath, []byte("clip"), 0o644))
		require.NoError(t, runtime.AppendRecording(store.RecordingEntry{
			ID:       "clip-1",
			FilePath: clipPath,
			Trigger:  "manual",
		}))

		report, err := runtime.DeleteRecordingByID("clip-1")
		require.NoError(t, err)
		assert.Equal(t, 1, report.Requested)
		assert.Equal(t, []string{"clip-1"}, report.Deleted)
		assert.Empty(t, report.Failed)
		_, statErr := os.Stat(clipPath)
		require.ErrorIs(t, statErr, os.ErrNotExist)

		recordings, loadErr := runtime.Recordings()
		require.NoError(t, loadErr)
		assert.Empty(t, recordings)
	})

	t.Run("partial_failure_keeps_metadata", func(t *testing.T) {
		configPath := filepath.Join(t.TempDir(), "config.yaml")
		s := store.New(configPath)
		runtime := &Runtime{store: s}
		doc := validDocument()
		doc.Config.Storage.RecordingsPath = testRecordingsPath
		require.NoError(t, runtime.SaveConfig(doc))

		badPath := filepath.Join(t.TempDir(), "non-empty-dir")
		require.NoError(t, os.MkdirAll(badPath, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(badPath, "child"), []byte("x"), 0o644))
		require.NoError(t, runtime.AppendRecording(store.RecordingEntry{
			ID:       "bad-delete",
			FilePath: badPath,
			Trigger:  "telemetry",
		}))

		report, err := runtime.DeleteRecordingsByID([]string{"missing", "bad-delete"})
		require.NoError(t, err)
		assert.Equal(t, 2, report.Requested)
		assert.Empty(t, report.Deleted)
		require.Len(t, report.Failed, 2)
		assert.Equal(t, "lookup", report.Failed[0].Stage)
		assert.Equal(t, "file_delete", report.Failed[1].Stage)

		recordings, loadErr := runtime.Recordings()
		require.NoError(t, loadErr)
		require.Len(t, recordings, 1)
		assert.Equal(t, "bad-delete", recordings[0].ID)
	})
}

func TestRuntimeEnqueueControl(t *testing.T) {
	t.Parallel()

	t.Run("forwards_control_intent", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		runtime := &Runtime{
			session: &ScannerSession{
				ctx:       ctx,
				controlCh: make(chan controlRequest, 1),
			},
		}

		runtime.EnqueueControl(IntentResumeScan)
		assert.Equal(t, IntentResumeScan, requireControlIntent(t, runtime.session.controlCh))
	})
}

func TestRuntimeSaveScanProfile(t *testing.T) {
	t.Parallel()

	t.Run("save_updates_profile", func(t *testing.T) {
		configPath := filepath.Join(t.TempDir(), "config.yaml")
		s := store.New(configPath)
		runtime := &Runtime{store: s}
		doc := validDocument()
		doc.Config.Storage.RecordingsPath = testRecordingsPath
		require.NoError(t, runtime.SaveConfig(doc))

		profile := store.ScanProfile{
			Name:               "Ops",
			FavoritesQuickKeys: enabledValues(100, 3),
			ServiceTypes:       enabledValues(47, 2),
			SystemQuickKeys: map[string][]int{
				"4": enabledValues(100, 4),
			},
			DepartmentQuickKeys: map[string][]int{
				"4:9": enabledValues(100, 5),
			},
		}
		require.NoError(t, runtime.SaveScanProfile(profile))

		profiles, err := runtime.ScanProfiles()
		require.NoError(t, err)
		require.Len(t, profiles, 1)
		assert.Equal(t, "Ops", profiles[0].Name)
		assert.NotEmpty(t, profiles[0].UpdatedAt)
		assert.Equal(t, 1, profiles[0].FavoritesQuickKeys[0])

		profile.FavoritesQuickKeys = enabledValues(100, 7)
		require.NoError(t, runtime.SaveScanProfile(profile))
		profiles, err = runtime.ScanProfiles()
		require.NoError(t, err)
		require.Len(t, profiles, 1)
		assert.Equal(t, 1, profiles[0].FavoritesQuickKeys[7])
		assert.Equal(t, 0, profiles[0].FavoritesQuickKeys[3])
	})

	t.Run("save_profile_merges_maps", func(t *testing.T) {
		configPath := filepath.Join(t.TempDir(), "config.yaml")
		s := store.New(configPath)
		runtime := &Runtime{store: s}
		doc := validDocument()
		doc.Config.Storage.RecordingsPath = testRecordingsPath
		require.NoError(t, runtime.SaveConfig(doc))

		require.NoError(t, runtime.SaveScanProfile(store.ScanProfile{
			Name:               "Ops",
			FavoritesQuickKeys: enabledValues(100, 2),
			ServiceTypes:       enabledValues(47, 2),
			SystemQuickKeys: map[string][]int{
				"4": enabledValues(100, 4),
			},
			DepartmentQuickKeys: map[string][]int{
				"4:9": enabledValues(100, 9),
			},
		}))

		require.NoError(t, runtime.SaveScanProfile(store.ScanProfile{
			Name:               "Ops",
			FavoritesQuickKeys: enabledValues(100, 3),
			ServiceTypes:       enabledValues(47, 3),
			SystemQuickKeys: map[string][]int{
				"5": enabledValues(100, 5),
			},
			DepartmentQuickKeys: map[string][]int{
				"5:8": enabledValues(100, 8),
			},
		}))

		profiles, err := runtime.ScanProfiles()
		require.NoError(t, err)
		require.Len(t, profiles, 1)
		require.Contains(t, profiles[0].SystemQuickKeys, "4")
		require.Contains(t, profiles[0].SystemQuickKeys, "5")
		require.Contains(t, profiles[0].DepartmentQuickKeys, "4:9")
		require.Contains(t, profiles[0].DepartmentQuickKeys, "5:8")
	})

	t.Run("canonicalizes_scope_keys", func(t *testing.T) {
		configPath := filepath.Join(t.TempDir(), "config.yaml")
		s := store.New(configPath)
		runtime := &Runtime{store: s}
		doc := validDocument()
		doc.Config.Storage.RecordingsPath = testRecordingsPath
		require.NoError(t, runtime.SaveConfig(doc))

		require.NoError(t, runtime.SaveScanProfile(store.ScanProfile{
			Name:               "Canon",
			FavoritesQuickKeys: enabledValues(100, 2),
			ServiceTypes:       enabledValues(47, 2),
			SystemQuickKeys: map[string][]int{
				"04": enabledValues(100, 4),
			},
			DepartmentQuickKeys: map[string][]int{
				"04:09": enabledValues(100, 9),
			},
		}))

		profiles, err := runtime.ScanProfiles()
		require.NoError(t, err)
		require.Len(t, profiles, 1)
		require.Contains(t, profiles[0].SystemQuickKeys, "4")
		require.Contains(t, profiles[0].DepartmentQuickKeys, "4:9")
		assert.NotContains(t, profiles[0].SystemQuickKeys, "04")
		assert.NotContains(t, profiles[0].DepartmentQuickKeys, "04:09")
	})

	t.Run("rewrites_legacy_keys", func(t *testing.T) {
		configPath := filepath.Join(t.TempDir(), "config.yaml")
		s := store.New(configPath)
		runtime := &Runtime{store: s}
		doc := validDocument()
		doc.Config.Storage.RecordingsPath = testRecordingsPath
		doc.State.ScanProfiles = []store.ScanProfile{
			{
				Name:               "LegacyMerge",
				FavoritesQuickKeys: enabledValues(100, 2),
				ServiceTypes:       enabledValues(47, 2),
				SystemQuickKeys: map[string][]int{
					"04": enabledValues(100, 4),
				},
				DepartmentQuickKeys: map[string][]int{
					"04:09": enabledValues(100, 9),
				},
			},
		}
		require.NoError(t, runtime.SaveConfig(doc))

		require.NoError(t, runtime.SaveScanProfile(store.ScanProfile{
			Name:               "LegacyMerge",
			FavoritesQuickKeys: enabledValues(100, 3),
			ServiceTypes:       enabledValues(47, 3),
			SystemQuickKeys: map[string][]int{
				"4": enabledValues(100, 5),
			},
			DepartmentQuickKeys: map[string][]int{
				"4:9": enabledValues(100, 6),
			},
		}))

		profiles, err := runtime.ScanProfiles()
		require.NoError(t, err)
		require.Len(t, profiles, 1)
		require.Contains(t, profiles[0].SystemQuickKeys, "4")
		require.Contains(t, profiles[0].DepartmentQuickKeys, "4:9")
		assert.NotContains(t, profiles[0].SystemQuickKeys, "04")
		assert.NotContains(t, profiles[0].DepartmentQuickKeys, "04:09")
	})
}

func TestRuntimeScanProfiles(t *testing.T) {
	t.Parallel()

	t.Run("returns_saved_profiles", func(t *testing.T) {
		configPath := filepath.Join(t.TempDir(), "config.yaml")
		s := store.New(configPath)
		runtime := &Runtime{store: s}
		doc := validDocument()
		doc.Config.Storage.RecordingsPath = testRecordingsPath
		require.NoError(t, runtime.SaveConfig(doc))
		require.NoError(t, runtime.SaveScanProfile(store.ScanProfile{
			Name:               "Ops",
			FavoritesQuickKeys: enabledValues(100, 2),
			ServiceTypes:       enabledValues(47, 2),
		}))

		profiles, err := runtime.ScanProfiles()
		require.NoError(t, err)
		require.Len(t, profiles, 1)
		assert.Equal(t, "Ops", profiles[0].Name)
	})
}

func TestRuntimeDeleteScanProfile(t *testing.T) {
	t.Parallel()

	t.Run("deletes_profile", func(t *testing.T) {
		configPath := filepath.Join(t.TempDir(), "config.yaml")
		s := store.New(configPath)
		runtime := &Runtime{store: s}
		doc := validDocument()
		doc.Config.Storage.RecordingsPath = testRecordingsPath
		require.NoError(t, runtime.SaveConfig(doc))
		require.NoError(t, runtime.SaveScanProfile(store.ScanProfile{
			Name:               "Ops",
			FavoritesQuickKeys: enabledValues(100, 2),
			ServiceTypes:       enabledValues(47, 2),
		}))
		require.NoError(t, runtime.DeleteScanProfile("ops"))

		profiles, err := runtime.ScanProfiles()
		require.NoError(t, err)
		assert.Empty(t, profiles)
	})
}

func TestRuntimeApplyScanProfile(t *testing.T) {
	t.Parallel()

	t.Run("apply_profile_executes_controls", func(t *testing.T) {
		configPath := filepath.Join(t.TempDir(), "config.yaml")
		s := store.New(configPath)
		runtime := &Runtime{store: s}
		doc := validDocument()
		doc.Config.Storage.RecordingsPath = testRecordingsPath
		require.NoError(t, runtime.SaveConfig(doc))

		require.NoError(t, runtime.SaveScanProfile(store.ScanProfile{
			Name:               "Night",
			FavoritesQuickKeys: enabledValues(100, 2),
			ServiceTypes:       enabledValues(47, 3),
			SystemQuickKeys: map[string][]int{
				"4": enabledValues(100, 4),
			},
			DepartmentQuickKeys: map[string][]int{
				"4:9": enabledValues(100, 5),
			},
		}))

		ctx, cancel := context.WithCancel(t.Context())
		client := &fakeSDS200Client{}
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

		runtime.session = session

		err := runtime.ApplyScanProfile("night", ProfileScopeSelector{FavoritesTag: 4, SystemTag: 9})
		require.NoError(t, err)

		snap := client.snapshot()
		require.Len(t, snap.setFQKCalls, 1)
		require.Len(t, snap.setSQKCalls, 1)
		require.Len(t, snap.setDQKCalls, 1)
		require.Len(t, snap.setSVCCalls, 1)
	})

	t.Run("apply_profile_accepts_legacy", func(t *testing.T) {
		configPath := filepath.Join(t.TempDir(), "config.yaml")
		s := store.New(configPath)
		runtime := &Runtime{store: s}
		doc := validDocument()
		doc.Config.Storage.RecordingsPath = testRecordingsPath
		doc.State.ScanProfiles = []store.ScanProfile{
			{
				Name:               "Legacy",
				FavoritesQuickKeys: enabledValues(100, 2),
				ServiceTypes:       enabledValues(47, 3),
				SystemQuickKeys: map[string][]int{
					"04": enabledValues(100, 4),
				},
				DepartmentQuickKeys: map[string][]int{
					"04:09": enabledValues(100, 9),
				},
			},
		}
		require.NoError(t, runtime.SaveConfig(doc))

		ctx, cancel := context.WithCancel(t.Context())
		client := &fakeSDS200Client{}
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
		runtime.session = session

		err := runtime.ApplyScanProfile("legacy", ProfileScopeSelector{FavoritesTag: 4, SystemTag: 9})
		require.NoError(t, err)

		snap := client.snapshot()
		require.Len(t, snap.setSQKCalls, 1)
		require.Len(t, snap.setDQKCalls, 1)
	})
}

func TestResolveRecordingsPath(t *testing.T) {
	t.Parallel()

	t.Run("keeps_absolute_path", func(t *testing.T) {
		absolutePath := filepath.Join(t.TempDir(), testRecordingsPath)
		doc := validDocument()
		doc.Config.Storage.RecordingsPath = absolutePath

		resolved, err := resolveRecordingsPath(doc, store.New(filepath.Join(t.TempDir(), "config.yaml")))
		require.NoError(t, err)
		assert.Equal(t, absolutePath, resolved)
	})

	t.Run("joins_relative_path", func(t *testing.T) {
		configPath := filepath.Join(t.TempDir(), "config.yaml")
		doc := validDocument()
		doc.Config.Storage.RecordingsPath = testRecordingsPath

		resolved, err := resolveRecordingsPath(doc, store.New(configPath))
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(filepath.Dir(configPath), testRecordingsPath), resolved)
	})
}

func TestEnsureDirectoryWritable(t *testing.T) {
	t.Parallel()

	t.Run("creates_missing_directory", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "nested", testRecordingsPath)
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

func requireControlIntent(t *testing.T, ch <-chan controlRequest) ControlIntent {
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
