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

	IntentMenuEnter        ControlIntent = "menu_enter"
	IntentMenuStatus       ControlIntent = "menu_status"
	IntentMenuSetValue     ControlIntent = "menu_set_value"
	IntentMenuBack         ControlIntent = "menu_back"
	IntentAnalyzeStart     ControlIntent = "analyze_start"
	IntentAnalyzePause     ControlIntent = "analyze_pause_resume"
	IntentPushWaterfall    ControlIntent = "push_waterfall"
	IntentGetWaterfall     ControlIntent = "get_waterfall"
	IntentSetDateTime      ControlIntent = "set_date_time"
	IntentGetDateTime      ControlIntent = "get_date_time"
	IntentSetLocationRange ControlIntent = "set_location_range"
	IntentGetLocationRange ControlIntent = "get_location_range"
	IntentGetDeviceInfo    ControlIntent = "get_device_info"
	IntentGetModel         ControlIntent = "get_model"
	IntentGetFirmware      ControlIntent = "get_firmware"
	IntentGetChargeStatus  ControlIntent = "get_charge_status"
	IntentKeepAlive        ControlIntent = "keep_alive"
	IntentPowerOff         ControlIntent = "power_off"
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
	MenuID            string
	MenuIndex         string
	MenuValue         string
	MenuBackLevel     string
	AnalyzeMode       string
	AnalyzeParams     []string
	FFTType           int
	FFTEnabled        bool
	DaylightSaving    int
	DateTime          time.Time
	Latitude          string
	Longitude         string
	Range             string
	Confirmed         bool
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
	Monitor   MonitorStatus
	Expert    ExpertStatus
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

type MonitorStatus struct {
	Enabled      bool
	Muted        bool
	GainDB       float64
	OutputDevice string
	LastError    string
	UpdatedAt    time.Time
}

type ExpertStatus struct {
	Enabled             bool
	MenuStatusSummary   string
	AnalyzeSummary      string
	WaterfallSummary    string
	DateTimeSummary     string
	DateTimeValue       time.Time
	DaylightSaving      int
	HasDateTime         bool
	LocationSummary     string
	Latitude            string
	Longitude           string
	Range               string
	DeviceModel         string
	FirmwareVersion     string
	ChargeStatusSummary string
	KeepAliveStatus     string
	UpdatedAt           time.Time
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
	ScannerIP              string
	RecordingsPath         string
	HangTimeSeconds        int
	MinAutoDurationSeconds int
	Activity               ActivitySettings

	AudioMonitorDefaultEnabled bool
	AudioMonitorOutputDevice   string
	AudioMonitorGainDB         float64
	ExpertModeEnabled          bool
}

type Dependencies struct {
	Title                    string
	InitialState             RuntimeState
	InitialSettings          Settings
	SubscribeState           func(context.Context) <-chan RuntimeState
	ExecuteControl           func(ControlRequest) ControlResult
	LoadScanScope            func(favoritesTag, systemTag int) (ScanScopeSnapshot, error)
	LoadScanProfiles         func() ([]ScanProfile, error)
	SaveScanProfile          func(ScanProfile) error
	DeleteScanProfile        func(string) error
	ApplyScanProfile         func(name string, favoritesTag int, systemTag int) error
	StartRecording           func() error
	StopRecording            func() error
	SetMonitorListen         func(bool) error
	SetMonitorMute           func(bool) error
	SetMonitorGain           func(float64) error
	SetMonitorOutputDevice   func(string) error
	ListMonitorOutputDevices func() ([]string, error)
	LoadRecordings           func() ([]Recording, error)
	DeleteRecordings         func([]string) (DeleteReport, error)
	SaveSettings             func(Settings) error
	Fatal                    <-chan error
}
