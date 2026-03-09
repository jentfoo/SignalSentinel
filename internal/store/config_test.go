package store

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testScannerIP = "10.0.1.10"

func defaultDocument() *Document {
	d := &Document{}
	d.ApplyDefaults()
	return d
}

func TestDocumentValidate(t *testing.T) {
	t.Parallel()

	t.Run("accepts_valid_document", func(t *testing.T) {
		doc := defaultDocument()
		doc.Config.Scanner.IP = testScannerIP

		err := doc.Validate()
		assert.NoError(t, err)
	})

	t.Run("invalid_scanner_ip", func(t *testing.T) {
		doc := defaultDocument()
		doc.Config.Scanner.IP = "not-an-ip"

		err := doc.Validate()
		assert.Error(t, err)
	})

	t.Run("rejects_invalid_port", func(t *testing.T) {
		doc := defaultDocument()
		doc.Config.Scanner.IP = "10.0.2.10"
		doc.Config.Scanner.ControlPort = 70000

		err := doc.Validate()
		assert.Error(t, err)
	})

	t.Run("rejects_nil_document", func(t *testing.T) {
		var doc *Document
		err := doc.Validate()
		require.Error(t, err)
	})

	t.Run("rejects_wrong_version", func(t *testing.T) {
		doc := defaultDocument()
		doc.Config.Scanner.IP = testScannerIP
		doc.Version = 99

		err := doc.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "version")
	})

	t.Run("invalid_rtsp_port", func(t *testing.T) {
		doc := defaultDocument()
		doc.Config.Scanner.IP = testScannerIP
		doc.Config.Scanner.RTSPPort = 70000

		err := doc.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "port")
	})

	t.Run("empty_recordings_path", func(t *testing.T) {
		doc := defaultDocument()
		doc.Config.Scanner.IP = testScannerIP
		doc.Config.Storage.RecordingsPath = ""

		err := doc.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "path")
	})

	t.Run("invalid_hang_time", func(t *testing.T) {
		doc := defaultDocument()
		doc.Config.Scanner.IP = testScannerIP
		doc.Config.Recording.HangTimeSeconds = 0

		err := doc.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "hang")
	})

	t.Run("invalid_recording_min_auto_duration", func(t *testing.T) {
		doc := defaultDocument()
		doc.Config.Scanner.IP = testScannerIP
		doc.Config.Recording.MinAutoDurationSeconds = -1

		err := doc.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "minimum auto duration")
	})

	t.Run("invalid_audio_gain", func(t *testing.T) {
		doc := defaultDocument()
		doc.Config.Scanner.IP = testScannerIP
		doc.Config.AudioMonitor.GainDB = 99

		err := doc.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "gain")
	})

	t.Run("duplicate_scan_profile_name", func(t *testing.T) {
		doc := defaultDocument()
		doc.Config.Scanner.IP = testScannerIP
		doc.State.ScanProfiles = []ScanProfile{
			{Name: "Ops"},
			{Name: "ops"},
		}

		err := doc.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unique")
	})
}

func TestDocumentApplyDefaults(t *testing.T) {
	t.Parallel()

	t.Run("preserves_user_values", func(t *testing.T) {
		doc := &Document{}
		doc.Config.Scanner.ControlPort = 12345
		doc.Config.Recording.HangTimeSeconds = 5
		doc.Config.Recording.MinAutoDurationSeconds = 42
		doc.ApplyDefaults()

		assert.Equal(t, 12345, doc.Config.Scanner.ControlPort)
		assert.Equal(t, 5, doc.Config.Recording.HangTimeSeconds)
		assert.Equal(t, 42, doc.Config.Recording.MinAutoDurationSeconds)
	})

	t.Run("fills_zero_values", func(t *testing.T) {
		doc := &Document{}
		doc.ApplyDefaults()

		assert.Equal(t, CurrentVersion, doc.Version)
		assert.Equal(t, 50536, doc.Config.Scanner.ControlPort)
		assert.Equal(t, 554, doc.Config.Scanner.RTSPPort)
		assert.Equal(t, "recordings", doc.Config.Storage.RecordingsPath)
		assert.Equal(t, 10, doc.Config.Recording.HangTimeSeconds)
		assert.Equal(t, 20, doc.Config.Recording.MinAutoDurationSeconds)
		assert.Equal(t, 150, doc.Config.Activity.StartDebounceMS)
		assert.Equal(t, 600, doc.Config.Activity.EndDebounceMS)
		assert.Equal(t, 300, doc.Config.Activity.MinActivityMS)
		assert.InDelta(t, 0.0, doc.Config.AudioMonitor.GainDB, 0.000001)
		assert.Equal(t, "system-default", doc.Config.AudioMonitor.OutputDevice)
		assert.False(t, doc.Config.UI.ExpertModeEnabled)
	})

	t.Run("derives_min_duration_from_hang", func(t *testing.T) {
		doc := &Document{}
		doc.Config.Recording.HangTimeSeconds = 15

		doc.ApplyDefaults()

		assert.Equal(t, 25, doc.Config.Recording.MinAutoDurationSeconds)
	})

	t.Run("preserves_non_zero_activity_minimum", func(t *testing.T) {
		doc := &Document{}
		doc.Config.Activity.MinActivityMS = 250

		doc.ApplyDefaults()

		assert.Equal(t, 250, doc.Config.Activity.MinActivityMS)
	})
}

func TestDocumentMigrate(t *testing.T) {
	t.Parallel()

	t.Run("migrates_v1_to_current_version", func(t *testing.T) {
		doc := &Document{
			Version: LegacyVersion1,
			Config: Config{
				Scanner: ScannerConfig{IP: testScannerIP},
			},
		}

		changed, err := doc.Migrate()
		require.NoError(t, err)
		assert.True(t, changed)
		assert.Equal(t, CurrentVersion, doc.Version)
		assert.Equal(t, 150, doc.Config.Activity.StartDebounceMS)
	})

	t.Run("rejects_unknown_version", func(t *testing.T) {
		doc := &Document{Version: 99}
		changed, err := doc.Migrate()
		require.Error(t, err)
		assert.False(t, changed)
	})
}
