package gui

import (
	"context"
	"time"
)

type ControlIntent string

const (
	IntentHoldCurrent     ControlIntent = "hold"
	IntentReleaseHold     ControlIntent = "resume_scan"
	IntentNext            ControlIntent = "next"
	IntentPrevious        ControlIntent = "previous"
	IntentJumpNumberTag   ControlIntent = "jump_number_tag"
	IntentQuickSearchHold ControlIntent = "quick_search_hold"
	IntentJumpMode        ControlIntent = "jump_mode"
	IntentAvoid           ControlIntent = "avoid"
	IntentUnavoid         ControlIntent = "unavoid"
	IntentSetVolume       ControlIntent = "set_volume"
	IntentSetSquelch      ControlIntent = "set_squelch"
	IntentSetFQK          ControlIntent = "set_favorites_quick_keys"
	IntentSetSQK          ControlIntent = "set_system_quick_keys"
	IntentSetDQK          ControlIntent = "set_department_quick_keys"
	IntentSetServiceTypes ControlIntent = "set_service_types"
)

const IntentResumeScan = IntentReleaseHold

type NumberTag struct {
	Favorites int
	System    int
	Channel   int
}

type ControlRequest struct {
	Intent            ControlIntent
	NumberTag         NumberTag
	FrequencyHz       int
	JumpMode          string
	JumpIndex         string
	ScopeFavoritesTag int
	ScopeSystemTag    int
	QuickKeyValues    []int
	ServiceTypes      []int
	Volume            int
	Squelch           int
}

type ControlResult struct {
	Intent      ControlIntent
	Action      string
	Command     string
	Success     bool
	Message     string
	RawReason   string
	RetryHint   string
	Unsupported bool
	At          time.Time
}

type ControlCapability struct {
	Available      bool
	DisabledReason string
}

type RuntimeState struct {
	Scanner   ScannerStatus
	Recording RecordingStatus
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
	LifecycleMode string
	Signal        int
	SquelchOpen   bool
	Active        bool
	Mute          bool
	Volume        int
	Squelch       int
	UpdatedAt     time.Time
	LastSource    string
	CanHoldTarget bool
	Avoided       bool
	AvoidKnown    bool
	Capabilities  map[ControlIntent]ControlCapability
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

type RecordingStatus struct {
	Active    bool
	StartedAt time.Time
	Trigger   string
	Manual    bool
}

type ScanScopeSnapshot struct {
	FavoritesTag        int
	SystemTag           int
	FavoritesQuickKeys  []int
	SystemQuickKeys     []int
	DepartmentQuickKeys []int
	ServiceTypes        []int
}

type ScanProfile struct {
	Name                string
	FavoritesQuickKeys  []int
	SystemQuickKeys     map[string][]int
	DepartmentQuickKeys map[string][]int
	ServiceTypes        []int
	UpdatedAt           string
}

type ActivitySettings struct {
	StartDebounceMS int
	EndDebounceMS   int
	MinActivityMS   int
}

type DeleteReportFailure struct {
	ID      string
	Stage   string
	Message string
}

type DeleteReport struct {
	Requested int
	Deleted   []string
	Failed    []DeleteReportFailure
}

type Settings struct {
	ScannerIP       string
	RecordingsPath  string
	HangTimeSeconds int
	HangTimeChanged bool
	Activity        ActivitySettings
}

type Dependencies struct {
	Title             string
	InitialState      RuntimeState
	InitialSettings   Settings
	SubscribeState    func(context.Context) <-chan RuntimeState
	ExecuteControl    func(ControlRequest) ControlResult
	LoadScanScope     func(favoritesTag, systemTag int) (ScanScopeSnapshot, error)
	LoadScanProfiles  func() ([]ScanProfile, error)
	SaveScanProfile   func(ScanProfile) error
	DeleteScanProfile func(string) error
	ApplyScanProfile  func(name string, favoritesTag int, systemTag int) error
	StartRecording    func() error
	StopRecording     func() error
	LoadRecordings    func() ([]Recording, error)
	DeleteRecordings  func([]string) (DeleteReport, error)
	SaveSettings      func(Settings) error
	Fatal             <-chan error
}
