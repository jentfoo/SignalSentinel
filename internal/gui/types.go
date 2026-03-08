package gui

import (
	"context"
	"time"
)

type ControlIntent string

const (
	IntentHoldCurrent ControlIntent = "hold"
	IntentResumeScan  ControlIntent = "resume_scan"
)

type RuntimeState struct {
	Scanner ScannerStatus
}

type ScannerStatus struct {
	Connected     bool
	Mode          string
	ViewScreen    string
	Frequency     string
	System        string
	Department    string
	Channel       string
	Talkgroup     string
	Hold          bool
	Signal        int
	SquelchOpen   bool
	Active        bool
	Mute          bool
	Volume        int
	Squelch       int
	UpdatedAt     time.Time
	LastSource    string
	CanHoldTarget bool
}

type Recording struct {
	ID        string
	StartedAt string
	EndedAt   string
	Duration  string
	Frequency string
	System    string
	Channel   string
	Talkgroup string
	FilePath  string
	FileSize  int64
	Trigger   string
}

type Settings struct {
	ScannerIP       string
	RecordingsPath  string
	HangTimeSeconds int
	HangTimeChanged bool
}

type Dependencies struct {
	Title           string
	InitialState    RuntimeState
	InitialSettings Settings
	SubscribeState  func(context.Context) <-chan RuntimeState
	EnqueueControl  func(ControlIntent)
	LoadRecordings  func() ([]Recording, error)
	SaveSettings    func(Settings) error
	Fatal           <-chan error
}
