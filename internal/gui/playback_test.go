//go:build !headless

package gui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStartPlayback(t *testing.T) {
	t.Run("rejects_empty_path", func(t *testing.T) {
		cmd, controllable, err := startPlayback("   ")
		require.Error(t, err)
		assert.Nil(t, cmd)
		assert.False(t, controllable)
		assert.Equal(t, "recording path is empty", err.Error())
	})

	t.Run("uses_ffplay_when_present", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("shell-script command fixture is only used on unix-like systems")
		}

		dir := t.TempDir()
		marker := filepath.Join(dir, "ffplay.log")
		writeUnixScript(t, filepath.Join(dir, "ffplay"), fmt.Sprintf("#!/bin/sh\necho \"$@\" > %q\n", marker))
		t.Setenv("PATH", dir)

		cmd, controllable, err := startPlayback("/tmp/clip.flac")
		require.NoError(t, err)
		require.NotNil(t, cmd)
		assert.True(t, controllable)

		waitForPath(t, marker)
		payload, readErr := os.ReadFile(marker)
		require.NoError(t, readErr)
		assert.Contains(t, string(payload), "-nodisp")
		assert.Contains(t, string(payload), "-autoexit")
		assert.Contains(t, string(payload), "/tmp/clip.flac")
	})

	t.Run("falls_back_to_open_command", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("shell-script command fixture is only used on unix-like systems")
		}

		dir := t.TempDir()
		marker := filepath.Join(dir, "open.log")

		openerName := "xdg-open"
		if runtime.GOOS == "darwin" {
			openerName = "open"
		}

		writeUnixScript(t, filepath.Join(dir, openerName), fmt.Sprintf("#!/bin/sh\necho \"$@\" > %q\n", marker))
		t.Setenv("PATH", dir)

		cmd, controllable, err := startPlayback("/tmp/fallback.flac")
		require.NoError(t, err)
		assert.Nil(t, cmd)
		assert.False(t, controllable)

		waitForPath(t, marker)
		payload, readErr := os.ReadFile(marker)
		require.NoError(t, readErr)
		assert.Contains(t, string(payload), "/tmp/fallback.flac")
	})
}

func writeUnixScript(t *testing.T, path, body string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(body), 0o755))
}

func waitForPath(t *testing.T, path string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for file %s", path)
		case <-ticker.C:
		}
	}
}
