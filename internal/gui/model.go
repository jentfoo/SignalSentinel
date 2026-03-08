package gui

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type uiModel struct {
	mu            sync.Mutex
	state         RuntimeState
	recordings    []Recording
	recordingsErr string
	activities    []string
	selectedClip  int
	selectedID    string
	recordingOn   bool
	lastActive    bool
	lastConnected bool
	everConnected bool
	fatalReceived bool
	initialized   bool
}

func appendActivity(model *uiModel, state RuntimeState) {
	scanner := state.Scanner
	if scanner.Connected {
		model.everConnected = true
	}
	now := scanner.UpdatedAt
	if now.IsZero() {
		now = time.Now()
	}
	ts := now.UTC().Format("2006-01-02 15:04:05")
	active := scanner.Active

	event := ""
	if !model.initialized {
		model.lastActive = active
		model.lastConnected = scanner.Connected
		model.initialized = true
	}
	if !scanner.Connected && model.lastConnected {
		event = ts + " scanner disconnected"
	} else if active && !model.lastActive {
		event = fmt.Sprintf("%s transmission start: %s / %s", ts, formatFrequency(scanner.Frequency), formatSystemChannel(scanner.System, scanner.Channel))
	} else if !active && model.lastActive {
		event = ts + " transmission end"
	}
	model.lastActive = active
	model.lastConnected = scanner.Connected
	if event != "" {
		model.activities = append([]string{event}, model.activities...)
		if len(model.activities) > 200 {
			model.activities = model.activities[:200]
		}
	}
}

func recordingsEqual(a, b []Recording) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func formatRecording(rec Recording) string {
	name := "(no file)"
	if strings.TrimSpace(rec.FilePath) != "" {
		name = filepath.Base(rec.FilePath)
	}
	parts := []string{
		orDash(rec.StartedAt),
		orDash(rec.Duration),
		formatFrequency(rec.Frequency),
		formatSystemChannel(rec.System, rec.Channel),
	}
	return strings.Join(parts, " | ") + " | " + name
}

func boolWord(v bool, yes, no string) string {
	if v {
		return yes
	}
	return no
}

func orDash(v string) string {
	if strings.TrimSpace(v) == "" {
		return "-"
	}
	return v
}

func formatFrequency(freq string) string {
	freq = strings.TrimSpace(freq)
	if freq == "" {
		return "-"
	}
	if strings.Contains(strings.ToLower(freq), "mhz") {
		return freq
	}
	return freq + " MHz"
}

func formatSystemChannel(system, channel string) string {
	return orDash(system) + " / " + orDash(channel)
}
