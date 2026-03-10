//go:build !headless

package gui

import (
	"errors"
	"image/color"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/widget"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApplyState(t *testing.T) {
	// Fyne widget state is process-global in tests; keep serial to avoid races.
	t.Run("disconnected_disables_controls", func(t *testing.T) {
		model := &uiModel{selectedClip: -1}
		ui := newTestUIViews(model)

		applyState(ui, model, RuntimeState{Scanner: ScannerStatus{Connected: false, UpdatedAt: time.Date(2026, 3, 8, 10, 0, 0, 0, time.UTC)}})

		assert.Equal(t, "Connecting...", ui.connectionLabel.Text)
		assert.True(t, ui.holdButton.Disabled())
		assert.True(t, ui.nextButton.Disabled())
		assert.True(t, ui.previousButton.Disabled())
		assert.True(t, ui.avoidButton.Disabled())
		assert.Contains(t, ui.controlAvailability.Text, "scanner is disconnected")
		assert.True(t, ui.startRecButton.Disabled())
		assert.Equal(t, "Start Recording", ui.startRecButton.Text)
	})

	t.Run("fallback_capabilities_use_runtime_state", func(t *testing.T) {
		model := &uiModel{selectedClip: -1}
		ui := newTestUIViews(model)

		applyState(ui, model, RuntimeState{Scanner: ScannerStatus{
			Connected:     true,
			CanHoldTarget: true,
			UpdatedAt:     time.Date(2026, 3, 8, 10, 0, 1, 0, time.UTC),
		}})

		assert.False(t, ui.holdButton.Disabled())
		assert.False(t, ui.nextButton.Disabled())
		assert.False(t, ui.previousButton.Disabled())
		assert.False(t, ui.avoidButton.Disabled())
		assert.Empty(t, ui.controlAvailability.Text)
	})

	t.Run("pending_control_keeps_buttons_disabled", func(t *testing.T) {
		model := &uiModel{selectedClip: -1, pendingControlAction: true}
		ui := newTestUIViews(model)

		applyState(ui, model, RuntimeState{Scanner: ScannerStatus{
			Connected:     true,
			CanHoldTarget: true,
			UpdatedAt:     time.Date(2026, 3, 8, 10, 0, 1, 0, time.UTC),
			Capabilities: map[ControlIntent]ControlCapability{
				IntentHoldCurrent:     {Available: true},
				IntentNext:            {Available: true},
				IntentPrevious:        {Available: true},
				IntentAvoid:           {Available: true},
				IntentJumpNumberTag:   {Available: true},
				IntentQuickSearchHold: {Available: true},
				IntentJumpMode:        {Available: true},
				IntentSetVolume:       {Available: true},
				IntentSetSquelch:      {Available: true},
			},
		}})

		assert.True(t, ui.holdButton.Disabled())
		assert.True(t, ui.nextButton.Disabled())
		assert.True(t, ui.previousButton.Disabled())
		assert.True(t, ui.avoidButton.Disabled())
		assert.True(t, ui.jumpTagButton.Disabled())
	})

	t.Run("hold_target_enables_hold", func(t *testing.T) {
		model := &uiModel{selectedClip: -1}
		ui := newTestUIViews(model)

		applyState(ui, model, RuntimeState{Scanner: ScannerStatus{
			Connected:   true,
			SquelchOpen: false,
			UpdatedAt:   time.Date(2026, 3, 8, 10, 0, 0, 0, time.UTC),
		}})

		allAvailable := map[ControlIntent]ControlCapability{
			IntentHoldCurrent:     {Available: true},
			IntentReleaseHold:     {Available: true},
			IntentNext:            {Available: true},
			IntentPrevious:        {Available: true},
			IntentJumpNumberTag:   {Available: true},
			IntentQuickSearchHold: {Available: true},
			IntentJumpMode:        {Available: true},
			IntentAvoid:           {Available: true},
			IntentUnavoid:         {Available: true},
			IntentSetVolume:       {Available: true},
			IntentSetSquelch:      {Available: true},
		}
		applyState(ui, model, RuntimeState{Scanner: ScannerStatus{
			Connected:     true,
			Mode:          "scan",
			ViewScreen:    "conventional",
			Frequency:     "155.2200",
			System:        "County",
			Department:    "Fire",
			Channel:       "Ops",
			Signal:        4,
			SquelchOpen:   true,
			Active:        true,
			Volume:        11,
			CanHoldTarget: true,
			Capabilities:  allAvailable,
			UpdatedAt:     time.Date(2026, 3, 8, 10, 0, 2, 0, time.UTC),
		}})
		applyState(ui, model, RuntimeState{Scanner: ScannerStatus{
			Connected:     true,
			Mode:          "scan",
			ViewScreen:    "conventional",
			Frequency:     "155.2200",
			System:        "County",
			Department:    "Fire",
			Channel:       "Ops",
			Signal:        4,
			SquelchOpen:   true,
			Active:        true,
			Volume:        11,
			CanHoldTarget: true,
			Capabilities:  allAvailable,
			UpdatedAt:     time.Date(2026, 3, 8, 10, 0, 3, 0, time.UTC),
		}})

		assert.Equal(t, "Connected", ui.connectionLabel.Text)
		assert.False(t, ui.holdButton.Disabled())
		assert.Equal(t, "Hold", ui.holdButton.Text)
		assert.False(t, ui.nextButton.Disabled())
		assert.False(t, ui.previousButton.Disabled())
		assert.False(t, ui.avoidButton.Disabled())
		assert.Empty(t, ui.controlAvailability.Text)
		assert.False(t, ui.startRecButton.Disabled())
		assert.Equal(t, "Start Recording", ui.startRecButton.Text)
		assert.Equal(t, "Scanning", ui.lifecycleLabel.Text)
		assert.Equal(t, "scan", ui.modeLabel.Text)
		assert.Equal(t, "155.2200", ui.freqLabel.Text)
		assert.Equal(t, "0 (open)", ui.squelchLabel.Text)
		assert.Equal(t, "11 (unmuted)", ui.volumeLabel.Text)
		require.Len(t, model.activities, 1)
		assert.Contains(t, model.activities[0], "active")
	})

	t.Run("hold_state_enables_resume", func(t *testing.T) {
		model := &uiModel{selectedClip: -1}
		ui := newTestUIViews(model)

		applyState(ui, model, RuntimeState{Scanner: ScannerStatus{
			Connected:     true,
			Hold:          true,
			CanHoldTarget: true,
			UpdatedAt:     time.Date(2026, 3, 8, 10, 0, 2, 0, time.UTC),
			Capabilities: map[ControlIntent]ControlCapability{
				IntentReleaseHold: {Available: true},
				IntentAvoid:       {Available: true},
				IntentUnavoid:     {Available: true},
			},
		}})

		assert.False(t, ui.holdButton.Disabled())
		assert.Equal(t, "Release Hold", ui.holdButton.Text)
		assert.False(t, ui.avoidButton.Disabled())
		assert.Equal(t, "Hold", ui.lifecycleLabel.Text)
	})

	t.Run("avoid_button_shows_unavoid", func(t *testing.T) {
		model := &uiModel{selectedClip: -1}
		ui := newTestUIViews(model)

		applyState(ui, model, RuntimeState{Scanner: ScannerStatus{
			Connected:     true,
			CanHoldTarget: true,
			AvoidKnown:    true,
			Avoided:       true,
			UpdatedAt:     time.Date(2026, 3, 8, 10, 0, 2, 0, time.UTC),
			Capabilities: map[ControlIntent]ControlCapability{
				IntentUnavoid: {Available: true},
			},
		}})

		assert.Equal(t, "Unavoid", ui.avoidButton.Text)
		assert.False(t, ui.avoidButton.Disabled())
	})

	t.Run("disconnected_after_connection_shows_reconnecting", func(t *testing.T) {
		model := &uiModel{selectedClip: -1}
		ui := newTestUIViews(model)

		applyState(ui, model, RuntimeState{Scanner: ScannerStatus{Connected: true, UpdatedAt: time.Date(2026, 3, 8, 10, 0, 0, 0, time.UTC)}})
		applyState(ui, model, RuntimeState{Scanner: ScannerStatus{Connected: false, UpdatedAt: time.Date(2026, 3, 8, 10, 0, 1, 0, time.UTC)}})

		assert.Equal(t, "Reconnecting...", ui.connectionLabel.Text)
	})

	t.Run("fatal_hides_reconnect_indicator", func(t *testing.T) {
		model := &uiModel{selectedClip: -1, fatalReceived: true, everConnected: true}
		ui := newTestUIViews(model)

		applyState(ui, model, RuntimeState{Scanner: ScannerStatus{Connected: false, UpdatedAt: time.Date(2026, 3, 8, 10, 0, 2, 0, time.UTC)}})

		assert.Equal(t, "Disconnected", ui.connectionLabel.Text)
	})

	t.Run("manual_recording_shows_stop", func(t *testing.T) {
		model := &uiModel{selectedClip: -1}
		ui := newTestUIViews(model)
		started := time.Date(2026, 3, 8, 10, 0, 0, 0, time.UTC)

		applyState(ui, model, RuntimeState{
			Scanner: ScannerStatus{
				Connected: true,
				UpdatedAt: started,
			},
			Recording: RecordingStatus{
				Active:    true,
				StartedAt: started.Add(-75 * time.Second),
				Manual:    true,
				Trigger:   "manual",
			},
		})

		assert.False(t, ui.startRecButton.Disabled())
		assert.Contains(t, ui.startRecButton.Text, "Stop (")
	})

	t.Run("activity_recording_disables_button", func(t *testing.T) {
		model := &uiModel{selectedClip: -1}
		ui := newTestUIViews(model)
		started := time.Date(2026, 3, 8, 10, 0, 0, 0, time.UTC)

		applyState(ui, model, RuntimeState{
			Scanner: ScannerStatus{
				Connected: true,
				UpdatedAt: started,
			},
			Recording: RecordingStatus{
				Active:    true,
				StartedAt: started.Add(-20 * time.Second),
				Manual:    false,
				Trigger:   "telemetry",
			},
		})

		assert.True(t, ui.startRecButton.Disabled())
		assert.Contains(t, ui.startRecButton.Text, "Recording (")
	})

	t.Run("monitor_controls_respect_availability_flags", func(t *testing.T) {
		model := &uiModel{selectedClip: -1}
		ui := newTestUIViews(model)
		ui.monitorListenButton = widget.NewButton("Listen", nil)
		ui.monitorMuteButton = widget.NewButton("Mute", nil)
		ui.monitorApplyButton = widget.NewButton("Apply", nil)

		applyState(ui, model, RuntimeState{
			Scanner: ScannerStatus{Connected: true, UpdatedAt: time.Date(2026, 3, 8, 10, 0, 0, 0, time.UTC)},
			Monitor: MonitorStatus{Enabled: false, Muted: false},
		})
		assert.True(t, ui.monitorListenButton.Disabled())
		assert.True(t, ui.monitorMuteButton.Disabled())
		assert.True(t, ui.monitorApplyButton.Disabled())

		model.monitorListenAvailable = true
		model.monitorMuteAvailable = true
		model.monitorApplyAvailable = true
		applyState(ui, model, RuntimeState{
			Scanner: ScannerStatus{Connected: true, UpdatedAt: time.Date(2026, 3, 8, 10, 0, 1, 0, time.UTC)},
			Monitor: MonitorStatus{Enabled: true, Muted: true},
		})
		assert.False(t, ui.monitorListenButton.Disabled())
		assert.False(t, ui.monitorMuteButton.Disabled())
		assert.False(t, ui.monitorApplyButton.Disabled())
		assert.Equal(t, "Stop Listening", ui.monitorListenButton.Text)
		assert.Equal(t, "Unmute", ui.monitorMuteButton.Text)
	})
}

func TestApplyRecordingsLoadResult(t *testing.T) {
	// Fyne widget state is process-global in tests; keep serial to avoid races.
	t.Run("records_loader_error", func(t *testing.T) {
		model := &uiModel{selectedClip: -1}
		ui := newTestUIViews(model)

		applyRecordingsLoadResult(model, ui.recordingsList, ui.recordingsErr, ui.playButton, ui.deleteButton, nil, errors.New("disk offline"), false)

		assert.Equal(t, "disk offline", model.recordingsErr)
		assert.True(t, ui.recordingsErr.Visible())
		assert.Contains(t, ui.recordingsErr.Text, "disk offline")
	})

	t.Run("updates_recordings_state", func(t *testing.T) {
		model := &uiModel{
			recordingsErr: "old error",
			recordings: []Recording{
				{ID: "old"},
			},
			selectedClip: 4,
		}
		ui := newTestUIViews(model)
		ui.recordingsErr.SetText("Recordings load error: old error")
		ui.recordingsErr.Show()
		ui.playButton.Enable()

		recs := []Recording{{ID: "new", Channel: "Fire Ops", FilePath: "/tmp/new.flac"}}
		applyRecordingsLoadResult(model, ui.recordingsList, ui.recordingsErr, ui.playButton, ui.deleteButton, recs, nil, false)

		assert.Empty(t, model.recordingsErr)
		assert.False(t, ui.recordingsErr.Visible())
		assert.Equal(t, -1, model.selectedClip)
		assert.True(t, ui.playButton.Disabled())
		require.Len(t, model.recordings, 1)
		assert.Equal(t, "new", model.recordings[0].ID)
	})

	t.Run("preserves_equal_recordings", func(t *testing.T) {
		recs := []Recording{{ID: "one", FilePath: "/tmp/one.flac"}}
		model := &uiModel{recordings: recs, selectedClip: 0}
		ui := newTestUIViews(model)
		ui.playButton.Enable()

		applyRecordingsLoadResult(model, ui.recordingsList, ui.recordingsErr, ui.playButton, ui.deleteButton, recs, nil, false)

		require.Len(t, model.recordings, 1)
		assert.Equal(t, "one", model.recordings[0].ID)
		assert.Equal(t, 0, model.selectedClip)
		assert.False(t, ui.playButton.Disabled())
	})

	t.Run("restores_selection_by_id", func(t *testing.T) {
		model := &uiModel{
			recordings: []Recording{
				{ID: "new"},
				{ID: "selected"},
			},
			selectedClip: 1,
		}
		ui := newTestUIViews(model)
		ui.playButton.Enable()

		next := []Recording{
			{ID: "latest"},
			{ID: "new"},
			{ID: "selected"},
		}
		model.selectedID = "selected"
		applyRecordingsLoadResult(model, ui.recordingsList, ui.recordingsErr, ui.playButton, ui.deleteButton, next, nil, false)

		assert.Equal(t, 2, model.selectedClip)
		assert.False(t, ui.playButton.Disabled())
	})

	t.Run("force_refresh_keeps_selection", func(t *testing.T) {
		recs := []Recording{{ID: "one", FilePath: "/tmp/one.flac"}}
		model := &uiModel{recordings: recs, selectedClip: 0}
		ui := newTestUIViews(model)
		ui.playButton.Enable()

		applyRecordingsLoadResult(model, ui.recordingsList, ui.recordingsErr, ui.playButton, ui.deleteButton, recs, nil, true)

		assert.Equal(t, 0, model.selectedClip)
		assert.False(t, ui.playButton.Disabled())
	})
}

func TestConnectionMetricText(t *testing.T) {
	model := &uiModel{}
	at := time.Date(2026, 3, 8, 10, 0, 0, 0, time.UTC)

	assert.Equal(t, "Q0 | 200ms", connectionMetricText(model, at.Add(-200*time.Millisecond), false, at))
	assert.Equal(t, "Q0 | 200ms", connectionMetricText(model, at.Add(5*time.Second-20*time.Millisecond), false, at.Add(5*time.Second)))
	assert.Equal(t, "Q0 | 300ms", connectionMetricText(model, at.Add(10*time.Second-300*time.Millisecond), false, at.Add(10*time.Second)))
	assert.Equal(t, "Q1 | -", connectionMetricText(model, time.Time{}, true, at.Add(11*time.Second)))
}

func TestControlAvailabilityText(t *testing.T) {
	assert.Empty(t, controlAvailabilityText(nil))
	assert.Equal(t, "Blocked: Hold (scanner is disconnected)", controlAvailabilityText([]string{"Hold (scanner is disconnected)"}))
	assert.Equal(t, "Blocked: Hold (scanner is disconnected) +2", controlAvailabilityText([]string{
		"Hold (scanner is disconnected)",
		"Next (scanner is disconnected)",
		"Previous (scanner is disconnected)",
	}))
}

func TestApplyConnectionOverview(t *testing.T) {
	model := &uiModel{}
	metric := widget.NewLabel("")
	indicator := canvas.NewCircle(color.NRGBA{})
	scanner := ScannerStatus{
		Connected: true,
		UpdatedAt: time.Date(2026, 3, 8, 10, 0, 0, 0, time.UTC),
	}

	applyConnectionOverview(uiViews{
		connectionMetric:    metric,
		connectionIndicator: indicator,
	}, model, scanner, true, scanner.UpdatedAt.Add(500*time.Millisecond))

	assert.Equal(t, "Q1 | 500ms", metric.Text)
	gotColor := color.NRGBAModel.Convert(indicator.FillColor).(color.NRGBA)
	assert.Equal(t, color.NRGBA{R: 46, G: 184, B: 74, A: 255}, gotColor)
}

func newTestUIViews(model *uiModel) uiViews {
	activityList := widget.NewList(
		func() int {
			model.mu.Lock()
			defer model.mu.Unlock()
			return len(model.activities)
		},
		func() fyne.CanvasObject {
			return widget.NewLabel("")
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			_ = id
			_ = obj
		},
	)
	suppressedList := widget.NewList(
		func() int {
			model.mu.Lock()
			defer model.mu.Unlock()
			return len(model.suppressed)
		},
		func() fyne.CanvasObject {
			return widget.NewLabel("")
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			_ = id
			_ = obj
		},
	)

	recordingsList := widget.NewList(
		func() int {
			model.mu.Lock()
			defer model.mu.Unlock()
			return len(model.recordings)
		},
		func() fyne.CanvasObject {
			return widget.NewLabel("")
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			_ = id
			_ = obj
		},
	)

	return uiViews{
		connectionLabel:     widget.NewLabel(""),
		controlAvailability: widget.NewLabel(""),
		connectionMetric:    widget.NewLabel(""),
		connectionIndicator: canvas.NewCircle(color.NRGBA{}),
		modeLabel:           widget.NewLabel(""),
		lifecycleLabel:      widget.NewLabel(""),
		sourceLabel:         widget.NewLabel(""),
		freqLabel:           widget.NewLabel(""),
		systemLabel:         widget.NewLabel(""),
		deptLabel:           widget.NewLabel(""),
		channelLabel:        widget.NewLabel(""),
		tgidLabel:           widget.NewLabel(""),
		signalLabel:         widget.NewLabel(""),
		squelchLabel:        widget.NewLabel(""),
		volumeLabel:         widget.NewLabel(""),
		holdButton:          widget.NewButton("Hold", nil),
		nextButton:          widget.NewButton("Next", nil),
		previousButton:      widget.NewButton("Previous", nil),
		jumpTagButton:       widget.NewButton("Jump Tag", nil),
		qshButton:           widget.NewButton("QSH", nil),
		jumpScanButton:      widget.NewButton("Jump Scan", nil),
		jumpWXButton:        widget.NewButton("Jump WX", nil),
		avoidButton:         widget.NewButton("Avoid", nil),
		setVolumeButton:     widget.NewButton("Set Volume", nil),
		setSquelchButton:    widget.NewButton("Set Squelch", nil),
		commandAction:       widget.NewLabel(""),
		commandStatus:       widget.NewLabel(""),
		commandMessage:      widget.NewLabel(""),
		tagFavEntry:         widget.NewEntry(),
		tagSysEntry:         widget.NewEntry(),
		tagChanEntry:        widget.NewEntry(),
		qshFreqEntry:        widget.NewEntry(),
		startRecButton:      widget.NewButton("Start Recording", nil),
		playButton:          widget.NewButton("Play", nil),
		deleteButton:        widget.NewButton("Delete Selected", nil),
		activityList:        activityList,
		suppressedList:      suppressedList,
		recordingsList:      recordingsList,
		recordingsErr:       widget.NewLabel(""),
	}
}
