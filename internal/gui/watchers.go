//go:build !headless

package gui

import (
	"context"
	"strconv"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

func watchState(ctx context.Context, deps Dependencies, ui uiViews, model *uiModel) {
	ch := deps.SubscribeState(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case state, ok := <-ch:
			if !ok {
				return
			}
			fyne.Do(func() {
				applyState(ui, model, state)
			})
		}
	}
}

func pollRecordings(ctx context.Context, deps Dependencies, ui uiViews, model *uiModel) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			recs, err := deps.LoadRecordings()
			fyne.Do(func() {
				applyRecordingsLoadResult(model, ui.recordingsList, ui.recordingsErr, ui.playButton, recs, err, false)
			})
		}
	}
}

func watchFatal(ctx context.Context, fatal <-chan error, window fyne.Window, ui uiViews, model *uiModel, out chan<- error) {
	select {
	case <-ctx.Done():
		return
	case err, ok := <-fatal:
		if !ok || err == nil {
			return
		}
		model.mu.Lock()
		model.fatalReceived = true
		model.mu.Unlock()
		fyne.Do(func() {
			ui.connLabel.SetText("Disconnected")
			ui.reconnectLabel.Hide()
			ui.spinner.Stop()
			ui.spinner.Hide()
			ui.fatalReason.SetText("Fatal: " + err.Error())
			errDialog := dialog.NewError(err, window)
			errDialog.SetOnClosed(func() {
				window.Close()
			})
			errDialog.Show()
		})
		select {
		case out <- err:
		default:
		}
	}
}

func applyState(ui uiViews, model *uiModel, state RuntimeState) {
	model.mu.Lock()
	appendActivity(model, state)
	model.state = state
	fatalReceived := model.fatalReceived
	everConnected := model.everConnected
	model.mu.Unlock()

	scanner := state.Scanner
	if scanner.Connected {
		ui.connLabel.SetText("Connected")
		ui.reconnectLabel.Hide()
		ui.spinner.Stop()
		ui.spinner.Hide()
	} else if fatalReceived {
		ui.connLabel.SetText("Disconnected")
		ui.reconnectLabel.Hide()
		ui.spinner.Stop()
		ui.spinner.Hide()
	} else {
		ui.connLabel.SetText("Disconnected")
		if everConnected {
			ui.reconnectLabel.SetText("Reconnecting...")
		} else {
			ui.reconnectLabel.SetText("Connecting...")
		}
		ui.reconnectLabel.Show()
		ui.spinner.Start()
		ui.spinner.Show()
	}

	ui.modeLabel.SetText(orDash(scanner.Mode))
	ui.sourceLabel.SetText(orDash(scanner.ViewScreen))
	ui.freqLabel.SetText(orDash(scanner.Frequency))
	ui.systemLabel.SetText(orDash(scanner.System))
	ui.deptLabel.SetText(orDash(scanner.Department))
	ui.channelLabel.SetText(orDash(scanner.Channel))
	ui.tgidLabel.SetText(orDash(scanner.Talkgroup))
	ui.signalLabel.SetText(strconv.Itoa(scanner.Signal))
	ui.squelchLabel.SetText(boolWord(scanner.SquelchOpen, "open", "closed"))
	ui.squelchLvlLabel.SetText(strconv.Itoa(scanner.Squelch))
	ui.muteLabel.SetText(boolWord(scanner.Mute, "muted", "unmuted"))
	ui.volumeLabel.SetText(strconv.Itoa(scanner.Volume))
	if scanner.UpdatedAt.IsZero() {
		ui.updatedLabel.SetText("-")
	} else {
		ui.updatedLabel.SetText(scanner.UpdatedAt.UTC().Format(time.RFC3339))
	}

	canControl := scanner.Connected
	canHold := canControl && scanner.CanHoldTarget && !scanner.Hold
	canResume := canControl && scanner.Hold
	if canHold {
		ui.holdButton.Enable()
	} else {
		ui.holdButton.Disable()
	}
	if canResume {
		ui.resumeButton.Enable()
	} else {
		ui.resumeButton.Disable()
	}

	ui.activityList.Refresh()
}

func applyRecordingsLoadResult(model *uiModel, list *widget.List, errLabel *widget.Label, playButton *widget.Button, recs []Recording, loadErr error, forceRefresh bool) {
	if loadErr != nil {
		model.mu.Lock()
		model.recordingsErr = loadErr.Error()
		model.mu.Unlock()
		errLabel.SetText("Recordings load error: " + loadErr.Error())
		errLabel.Show()
		return
	}

	model.mu.Lock()
	dataChanged := !recordingsEqual(model.recordings, recs)
	changed := forceRefresh || dataChanged
	hadErr := model.recordingsErr != ""
	model.recordingsErr = ""
	if dataChanged {
		model.selectedClip = -1
		model.recordings = recs
	}
	model.mu.Unlock()

	if hadErr {
		errLabel.SetText("")
		errLabel.Hide()
	}
	if dataChanged {
		if playButton != nil {
			playButton.Disable()
		}
	}
	if changed {
		list.Refresh()
	}
}
