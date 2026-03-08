package store

import (
	"errors"
	"fmt"
	"net"
)

const CurrentVersion = 1

// Document is the persisted YAML payload for app-owned config and metadata.
type Document struct {
	Version int    `yaml:"version"`
	Config  Config `yaml:"config"`
	State   State  `yaml:"state"`
}

// Config contains user-managed values that should persist.
type Config struct {
	Scanner   ScannerConfig   `yaml:"scanner"`
	Storage   StorageConfig   `yaml:"storage"`
	Recording RecordingConfig `yaml:"recording"`
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
	HangTimeSeconds int `yaml:"hang_time_seconds"`
}

// State contains persisted, app-managed metadata and never scanner live state.
type State struct {
	Favorites  []Favorite       `yaml:"favorites"`
	Recordings []RecordingEntry `yaml:"recordings,omitempty"`
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
	}
	return nil
}
