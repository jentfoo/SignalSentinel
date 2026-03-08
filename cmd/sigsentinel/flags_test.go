package main

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/jentfoo/SignalSentinel/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseFlags(t *testing.T) {
	t.Parallel()

	t.Run("parses_values", func(t *testing.T) {
		var out bytes.Buffer
		opts, err := parseFlags([]string{
			"--config", "/tmp/custom.yaml",
			"--scanner-ip", " 127.0.0.1 ",
			"--recordings-path", " ./clips ",
		}, &out)
		require.NoError(t, err)
		assert.False(t, opts.ShowHelp)
		assert.Equal(t, "/tmp/custom.yaml", opts.ConfigPath)
		assert.Equal(t, "127.0.0.1", opts.ScannerIP)
		assert.Equal(t, "./clips", opts.RecordingsPath)
	})

	t.Run("supports_help", func(t *testing.T) {
		var out bytes.Buffer
		opts, err := parseFlags([]string{"--help"}, &out)
		require.NoError(t, err)
		assert.True(t, opts.ShowHelp)
		assert.Contains(t, out.String(), "Usage: sigsentinel [flags]")
	})

	t.Run("rejects_positional_args", func(t *testing.T) {
		var out bytes.Buffer
		_, err := parseFlags([]string{"extra"}, &out)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unexpected positional arguments")
	})
}

func TestPersistCLIOverrides(t *testing.T) {
	t.Parallel()

	t.Run("writes_scanner_and_recordings_with_defaults", func(t *testing.T) {
		configPath := filepath.Join(t.TempDir(), "config.yaml")
		err := persistCLIOverrides(cliFlags{
			ConfigPath:     configPath,
			ScannerIP:      "127.0.0.1",
			RecordingsPath: "clips",
		})
		require.NoError(t, err)

		doc, loadErr := store.New(configPath).Load()
		require.NoError(t, loadErr)
		assert.Equal(t, "127.0.0.1", doc.Config.Scanner.IP)
		assert.Equal(t, "clips", doc.Config.Storage.RecordingsPath)
		assert.Equal(t, 50536, doc.Config.Scanner.ControlPort)
		assert.Equal(t, 554, doc.Config.Scanner.RTSPPort)
		assert.Equal(t, 10, doc.Config.Recording.HangTimeSeconds)
		assert.Equal(t, store.CurrentVersion, doc.Version)
	})

	t.Run("rejects_invalid_scanner_ip", func(t *testing.T) {
		configPath := filepath.Join(t.TempDir(), "config.yaml")
		err := persistCLIOverrides(cliFlags{
			ConfigPath: configPath,
			ScannerIP:  "not_an_ip",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "scanner ip is invalid")
	})
}
