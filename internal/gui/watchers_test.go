//go:build !headless

package gui

import (
	"errors"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApplyState(t *testing.T) {
	t.Run("disconnected_disables_controls", func(t *testing.T) {
		model := &uiModel{selectedClip: -1}
		ui := newTestUIViews(model)

		applyState(ui, model, RuntimeState{Scanner: ScannerStatus{Connected: false, UpdatedAt: time.Date(2026, 3, 8, 10, 0, 0, 0, time.UTC)}})

		assert.Equal(t, "Disconnected", ui.connLabel.Text)
		assert.True(t, ui.reconnectLabel.Visible())
		assert.Equal(t, "Connecting...", ui.reconnectLabel.Text)
		assert.True(t, ui.spinner.Visible())
		assert.True(t, ui.holdButton.Disabled())
		assert.True(t, ui.resumeButton.Disabled())
	})

	t.Run("hold_target_enables_hold", func(t *testing.T) {
		model := &uiModel{selectedClip: -1}
		ui := newTestUIViews(model)

		applyState(ui, model, RuntimeState{Scanner: ScannerStatus{
			Connected:   true,
			SquelchOpen: false,
			UpdatedAt:   time.Date(2026, 3, 8, 10, 0, 0, 0, time.UTC),
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
			UpdatedAt:     time.Date(2026, 3, 8, 10, 0, 2, 0, time.UTC),
		}})

		assert.Equal(t, "Connected", ui.connLabel.Text)
		assert.False(t, ui.reconnectLabel.Visible())
		assert.False(t, ui.spinner.Visible())
		assert.False(t, ui.holdButton.Disabled())
		assert.True(t, ui.resumeButton.Disabled())
		assert.Equal(t, "scan", ui.modeLabel.Text)
		assert.Equal(t, "155.2200", ui.freqLabel.Text)
		assert.Equal(t, "open", ui.squelchLabel.Text)
		assert.Equal(t, "0", ui.squelchLvlLabel.Text)
		assert.Equal(t, "unmuted", ui.muteLabel.Text)
		require.Len(t, model.activities, 1)
		assert.Contains(t, model.activities[0], "transmission start")
	})

	t.Run("hold_state_enables_resume", func(t *testing.T) {
		model := &uiModel{selectedClip: -1}
		ui := newTestUIViews(model)

		applyState(ui, model, RuntimeState{Scanner: ScannerStatus{Connected: true, Hold: true, CanHoldTarget: true, UpdatedAt: time.Date(2026, 3, 8, 10, 0, 2, 0, time.UTC)}})

		assert.True(t, ui.holdButton.Disabled())
		assert.False(t, ui.resumeButton.Disabled())
	})

	t.Run("disconnected_after_connection_shows_reconnecting", func(t *testing.T) {
		model := &uiModel{selectedClip: -1}
		ui := newTestUIViews(model)

		applyState(ui, model, RuntimeState{Scanner: ScannerStatus{Connected: true, UpdatedAt: time.Date(2026, 3, 8, 10, 0, 0, 0, time.UTC)}})
		applyState(ui, model, RuntimeState{Scanner: ScannerStatus{Connected: false, UpdatedAt: time.Date(2026, 3, 8, 10, 0, 1, 0, time.UTC)}})

		assert.Equal(t, "Disconnected", ui.connLabel.Text)
		assert.True(t, ui.reconnectLabel.Visible())
		assert.Equal(t, "Reconnecting...", ui.reconnectLabel.Text)
		assert.True(t, ui.spinner.Visible())
	})

	t.Run("fatal_hides_reconnect_indicator", func(t *testing.T) {
		model := &uiModel{selectedClip: -1, fatalReceived: true, everConnected: true}
		ui := newTestUIViews(model)

		applyState(ui, model, RuntimeState{Scanner: ScannerStatus{Connected: false, UpdatedAt: time.Date(2026, 3, 8, 10, 0, 2, 0, time.UTC)}})

		assert.Equal(t, "Disconnected", ui.connLabel.Text)
		assert.False(t, ui.reconnectLabel.Visible())
		assert.False(t, ui.spinner.Visible())
	})
}

func TestApplyRecordingsLoadResult(t *testing.T) {
	t.Run("records_loader_error", func(t *testing.T) {
		model := &uiModel{selectedClip: -1}
		ui := newTestUIViews(model)

		applyRecordingsLoadResult(model, ui.recordingsList, ui.recordingsErr, ui.playButton, nil, errors.New("disk offline"), false)

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
		applyRecordingsLoadResult(model, ui.recordingsList, ui.recordingsErr, ui.playButton, recs, nil, false)

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

		applyRecordingsLoadResult(model, ui.recordingsList, ui.recordingsErr, ui.playButton, recs, nil, false)

		require.Len(t, model.recordings, 1)
		assert.Equal(t, "one", model.recordings[0].ID)
		assert.Equal(t, 0, model.selectedClip)
		assert.False(t, ui.playButton.Disabled())
	})

	t.Run("resets_selection_when_recordings_change_even_if_index_in_bounds", func(t *testing.T) {
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
		applyRecordingsLoadResult(model, ui.recordingsList, ui.recordingsErr, ui.playButton, next, nil, false)

		assert.Equal(t, -1, model.selectedClip)
		assert.True(t, ui.playButton.Disabled())
	})

	t.Run("force_refresh_keeps_selection_when_data_unchanged", func(t *testing.T) {
		recs := []Recording{{ID: "one", FilePath: "/tmp/one.flac"}}
		model := &uiModel{recordings: recs, selectedClip: 0}
		ui := newTestUIViews(model)
		ui.playButton.Enable()

		applyRecordingsLoadResult(model, ui.recordingsList, ui.recordingsErr, ui.playButton, recs, nil, true)

		assert.Equal(t, 0, model.selectedClip)
		assert.False(t, ui.playButton.Disabled())
	})
}

func newTestUIViews(model *uiModel) uiViews {
	reconnect := widget.NewLabel("Reconnecting...")
	reconnect.Hide()
	spinner := widget.NewProgressBarInfinite()
	spinner.Hide()

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
		connLabel:       widget.NewLabel(""),
		reconnectLabel:  reconnect,
		fatalReason:     widget.NewLabel(""),
		modeLabel:       widget.NewLabel(""),
		sourceLabel:     widget.NewLabel(""),
		freqLabel:       widget.NewLabel(""),
		systemLabel:     widget.NewLabel(""),
		deptLabel:       widget.NewLabel(""),
		channelLabel:    widget.NewLabel(""),
		tgidLabel:       widget.NewLabel(""),
		signalLabel:     widget.NewLabel(""),
		squelchLabel:    widget.NewLabel(""),
		squelchLvlLabel: widget.NewLabel(""),
		muteLabel:       widget.NewLabel(""),
		volumeLabel:     widget.NewLabel(""),
		updatedLabel:    widget.NewLabel(""),
		holdButton:      widget.NewButton("Hold", nil),
		resumeButton:    widget.NewButton("Resume", nil),
		playButton:      widget.NewButton("Play", nil),
		activityList:    activityList,
		recordingsList:  recordingsList,
		recordingsErr:   widget.NewLabel(""),
		spinner:         spinner,
	}
}
