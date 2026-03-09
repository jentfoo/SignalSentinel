package store

import (
	"errors"
	"fmt"
	"net"
	"strings"
)

const (
	LegacyVersion1 = 1
	CurrentVersion = 2
)

// Document is the persisted YAML payload for app-owned config and metadata.
type Document struct {
	Version int    `yaml:"version"`
	Config  Config `yaml:"config"`
	State   State  `yaml:"state"`
}

// Config contains user-managed values that should persist.
type Config struct {
	Scanner      ScannerConfig      `yaml:"scanner"`
	Storage      StorageConfig      `yaml:"storage"`
	Recording    RecordingConfig    `yaml:"recording"`
	Activity     ActivityConfig     `yaml:"activity"`
	AudioMonitor AudioMonitorConfig `yaml:"audio_monitor"`
	UI           UIConfig           `yaml:"ui"`
}

type ScannerConfig struct {
	IP          string `yaml:"ip"`
	ControlPort int    `yaml:"control_port"`
	RTSPPort    int    `yaml:"rtsp_port"`
}

type StorageConfig struct {
	RecordingsPath string `yaml:"recordings_path"`
}

type RecordingConfig struct {
	// HangTimeSeconds keeps an auto recording open after activity drops.
	HangTimeSeconds int `yaml:"hang_time_seconds"`
	// MinAutoDurationSeconds suppresses telemetry-triggered clips shorter than this duration.
	MinAutoDurationSeconds int `yaml:"min_auto_duration_seconds"`
}

type ActivityConfig struct {
	StartDebounceMS int `yaml:"start_debounce_ms"`
	EndDebounceMS   int `yaml:"end_debounce_ms"`
	MinActivityMS   int `yaml:"min_activity_ms"`
}

type AudioMonitorConfig struct {
	DefaultEnabled bool    `yaml:"default_enabled"`
	OutputDevice   string  `yaml:"output_device"`
	GainDB         float64 `yaml:"gain_db"`
}

type UIConfig struct {
	ExpertModeEnabled bool `yaml:"expert_mode_enabled"`
}

// State contains persisted, app-managed metadata and never scanner live state.
type State struct {
	Favorites    []Favorite       `yaml:"favorites"`
	Recordings   []RecordingEntry `yaml:"recordings,omitempty"`
	ScanProfiles []ScanProfile    `yaml:"scan_profiles,omitempty"`
}

// Favorite is an app-level quick entry for operator navigation.
type Favorite struct {
	Name      string `yaml:"name"`
	Frequency string `yaml:"frequency,omitempty"`
	Channel   string `yaml:"channel,omitempty"`
}

// RecordingEntry is persisted metadata for finalized recordings.
type RecordingEntry struct {
	ID        string `yaml:"id"`
	StartedAt string `yaml:"started_at"`
	EndedAt   string `yaml:"ended_at"`
	Duration  string `yaml:"duration"`
	Frequency string `yaml:"frequency,omitempty"`
	System    string `yaml:"system,omitempty"`
	Channel   string `yaml:"channel,omitempty"`
	Talkgroup string `yaml:"talkgroup,omitempty"`
	FilePath  string `yaml:"file_path"`
	FileSize  int64  `yaml:"file_size"`
	Trigger   string `yaml:"trigger"`
}

// ScanProfile stores app-owned scan scope presets for quick keys and service types.
type ScanProfile struct {
	Name                string           `yaml:"name"`
	FavoritesQuickKeys  []int            `yaml:"favorites_quick_keys,omitempty"`
	SystemQuickKeys     map[string][]int `yaml:"system_quick_keys,omitempty"`
	DepartmentQuickKeys map[string][]int `yaml:"department_quick_keys,omitempty"`
	ServiceTypes        []int            `yaml:"service_types,omitempty"`
	UpdatedAt           string           `yaml:"updated_at,omitempty"`
}

func (d *Document) Migrate() (bool, error) {
	if d == nil {
		return false, errors.New("document is nil")
	}

	changed := false
	if d.Version == 0 {
		d.Version = LegacyVersion1
		changed = true
	}
	switch d.Version {
	case LegacyVersion1:
		d.Version = CurrentVersion
		changed = true
	case CurrentVersion:
	default:
		return false, fmt.Errorf("unsupported version: %d", d.Version)
	}
	d.ApplyDefaults()
	return changed, nil
}

func (d *Document) ApplyDefaults() {
	if d == nil {
		return
	}
	if d.Version == 0 {
		d.Version = CurrentVersion
	}
	if d.Config.Scanner.ControlPort == 0 {
		d.Config.Scanner.ControlPort = 50536
	}
	if d.Config.Scanner.RTSPPort == 0 {
		d.Config.Scanner.RTSPPort = 554
	}
	if d.Config.Storage.RecordingsPath == "" {
		d.Config.Storage.RecordingsPath = "recordings"
	}
	if d.Config.Recording.HangTimeSeconds == 0 {
		d.Config.Recording.HangTimeSeconds = 10
	}
	if d.Config.Recording.MinAutoDurationSeconds == 0 {
		d.Config.Recording.MinAutoDurationSeconds = d.Config.Recording.HangTimeSeconds + 10
	}
	if d.Config.Activity.StartDebounceMS == 0 {
		d.Config.Activity.StartDebounceMS = 150
	}
	if d.Config.Activity.EndDebounceMS == 0 {
		d.Config.Activity.EndDebounceMS = 600
	}
	if d.Config.Activity.MinActivityMS == 0 {
		d.Config.Activity.MinActivityMS = 300
	}
	if strings.TrimSpace(d.Config.AudioMonitor.OutputDevice) == "" {
		d.Config.AudioMonitor.OutputDevice = "system-default"
	}
}

func (d *Document) Validate() error {
	if d == nil {
		return errors.New("document is nil")
	} else if d.Version != CurrentVersion {
		return fmt.Errorf("unsupported version: %d", d.Version)
	} else if net.ParseIP(d.Config.Scanner.IP) == nil {
		return errors.New("scanner ip is invalid")
	} else if d.Config.Scanner.ControlPort < 1 || d.Config.Scanner.ControlPort > 65535 {
		return errors.New("scanner control port is invalid")
	} else if d.Config.Scanner.RTSPPort < 1 || d.Config.Scanner.RTSPPort > 65535 {
		return errors.New("scanner rtsp port is invalid")
	} else if d.Config.Storage.RecordingsPath == "" {
		return errors.New("recordings path is required")
	} else if d.Config.Recording.HangTimeSeconds < 1 {
		return errors.New("hang time must be >= 1")
	} else if d.Config.Recording.MinAutoDurationSeconds < 0 {
		return errors.New("recording minimum auto duration must be >= 0")
	} else if d.Config.Activity.StartDebounceMS < 0 {
		return errors.New("activity start debounce must be >= 0")
	} else if d.Config.Activity.EndDebounceMS < 0 {
		return errors.New("activity end debounce must be >= 0")
	} else if d.Config.Activity.MinActivityMS < 0 {
		return errors.New("activity minimum duration must be >= 0")
	} else if d.Config.AudioMonitor.GainDB < -60 || d.Config.AudioMonitor.GainDB > 24 {
		return errors.New("audio monitor gain must be between -60 and 24 dB")
	}
	names := map[string]struct{}{}
	for _, profile := range d.State.ScanProfiles {
		name := strings.TrimSpace(profile.Name)
		if name == "" {
			return errors.New("scan profile name is required")
		}
		key := strings.ToLower(name)
		if _, exists := names[key]; exists {
			return fmt.Errorf("scan profile name must be unique: %s", profile.Name)
		}
		names[key] = struct{}{}
	}
	return nil
}
