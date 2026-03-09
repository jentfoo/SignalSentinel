//go:build !headless

package gui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
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
				applyRecordingsLoadResult(model, ui.recordingsList, ui.recordingsErr, ui.playButton, ui.deleteButton, recs, err, false)
			})
		}
	}
}

func watchRecordingDuration(ctx context.Context, ui uiViews, model *uiModel) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case at := <-ticker.C:
			fyne.Do(func() {
				model.mu.Lock()
				state := model.state
				pending := model.pendingRecordingAction
				pendingStop := model.pendingRecordingStop
				model.mu.Unlock()
				applyRecordingButtonState(ui.startRecButton, state, pending, pendingStop, at)
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
			if ui.connectionLabel != nil {
				ui.connectionLabel.SetText("Disconnected")
			}
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
	pendingControlAction := model.pendingControlAction
	pendingRecordingAction := model.pendingRecordingAction
	pendingRecordingStop := model.pendingRecordingStop
	model.mu.Unlock()

	scanner := state.Scanner
	if ui.connectionLabel != nil {
		ui.connectionLabel.SetText(connectionStatusText(scanner.Connected, fatalReceived, everConnected))
	}
	ui.modeLabel.SetText(orDash(scanner.Mode))
	if ui.lifecycleLabel != nil {
		lifecycle := strings.TrimSpace(scanner.LifecycleMode)
		if lifecycle == "" {
			lifecycle = DeriveLifecycleMode(scanner.Connected, scanner.Hold, scanner.Mode)
		}
		ui.lifecycleLabel.SetText(orDash(lifecycle))
	}
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

	holdIntent := IntentHoldCurrent
	holdLabel := "Hold"
	if scanner.Hold {
		holdIntent = IntentReleaseHold
		holdLabel = "Release Hold"
	}
	if ui.holdButton != nil {
		ui.holdButton.SetText(holdLabel)
	}
	if ui.holdStatusLabel != nil {
		ui.holdStatusLabel.SetText(boolWord(scanner.Hold, "Yes", "No"))
	}
	holdCap := capabilityFor(scanner, holdIntent)
	nextCap := capabilityFor(scanner, IntentNext)
	prevCap := capabilityFor(scanner, IntentPrevious)
	jumpTagCap := capabilityFor(scanner, IntentJumpNumberTag)
	qshCap := capabilityFor(scanner, IntentQuickSearchHold)
	jumpScanCap := capabilityFor(scanner, IntentJumpMode)
	jumpWXCap := capabilityFor(scanner, IntentJumpMode)
	avoidIntent := IntentAvoid
	avoidLabel := "Avoid"
	if !scanner.AvoidKnown {
		avoidLabel = "Toggle Avoid"
	} else if scanner.Avoided {
		avoidIntent = IntentUnavoid
		avoidLabel = "Unavoid"
	}
	if ui.avoidButton != nil {
		ui.avoidButton.SetText(avoidLabel)
	}
	avoidCap := capabilityFor(scanner, avoidIntent)
	volumeCap := capabilityFor(scanner, IntentSetVolume)
	squelchCap := capabilityFor(scanner, IntentSetSquelch)
	applyControlState(ui.holdButton, holdCap)
	applyControlState(ui.nextButton, nextCap)
	applyControlState(ui.previousButton, prevCap)
	applyControlState(ui.jumpTagButton, jumpTagCap)
	applyControlState(ui.qshButton, qshCap)
	applyControlState(ui.jumpScanButton, jumpScanCap)
	applyControlState(ui.jumpWXButton, jumpWXCap)
	applyControlState(ui.avoidButton, avoidCap)
	applyControlState(ui.setVolumeButton, volumeCap)
	applyControlState(ui.setSquelchButton, squelchCap)
	if pendingControlAction {
		setEnabled(ui.holdButton, false)
		setEnabled(ui.nextButton, false)
		setEnabled(ui.previousButton, false)
		setEnabled(ui.jumpTagButton, false)
		setEnabled(ui.qshButton, false)
		setEnabled(ui.jumpScanButton, false)
		setEnabled(ui.jumpWXButton, false)
		setEnabled(ui.avoidButton, false)
		setEnabled(ui.setVolumeButton, false)
		setEnabled(ui.setSquelchButton, false)
	}
	if ui.controlStatus != nil {
		disabled := make([]string, 0, 10)
		addDisabled := func(name string, capability ControlCapability) {
			if capability.Available {
				return
			}
			reason := strings.TrimSpace(capability.DisabledReason)
			if reason == "" {
				reason = "unavailable"
			}
			disabled = append(disabled, fmt.Sprintf("%s: %s", name, reason))
		}
		addDisabled(holdLabel, holdCap)
		addDisabled("Next", nextCap)
		addDisabled("Previous", prevCap)
		addDisabled("Jump Tag", jumpTagCap)
		addDisabled("QSH", qshCap)
		addDisabled("Jump Scan", jumpScanCap)
		addDisabled("Jump WX", jumpWXCap)
		addDisabled(avoidLabel, avoidCap)
		addDisabled("Volume", volumeCap)
		addDisabled("Squelch", squelchCap)
		switch len(disabled) {
		case 0:
			ui.controlStatus.SetText("All visible controls available.")
		case 1, 2, 3:
			ui.controlStatus.SetText(strings.Join(disabled, " | "))
		default:
			ui.controlStatus.SetText(fmt.Sprintf("%s | +%d more", strings.Join(disabled[:3], " | "), len(disabled)-3))
		}
	}
	applyRecordingButtonState(ui.startRecButton, state, pendingRecordingAction, pendingRecordingStop, time.Now())

	ui.activityList.Refresh()
	if ui.suppressedList != nil {
		ui.suppressedList.Refresh()
	}
}

func connectionStatusText(connected, fatalReceived, everConnected bool) string {
	if connected {
		return "Connected"
	}
	if fatalReceived {
		return "Disconnected"
	}
	if everConnected {
		return "Reconnecting..."
	}
	return "Connecting..."
}

func capabilityFor(scanner ScannerStatus, intent ControlIntent) ControlCapability {
	if scanner.Capabilities != nil {
		if cap, ok := scanner.Capabilities[intent]; ok {
			return cap
		}
	}
	if !scanner.Connected {
		return ControlCapability{Available: false, DisabledReason: "scanner is disconnected"}
	}
	switch intent {
	case IntentHoldCurrent:
		if scanner.Hold {
			return ControlCapability{Available: false, DisabledReason: "scanner is already in hold mode"}
		}
		if !scanner.CanHoldTarget {
			return ControlCapability{Available: false, DisabledReason: "hold target unavailable"}
		}
		return ControlCapability{Available: true}
	case IntentReleaseHold:
		if !scanner.Hold {
			return ControlCapability{Available: false, DisabledReason: "scanner is not in hold mode"}
		}
		return ControlCapability{Available: true}
	case IntentNext, IntentPrevious, IntentAvoid, IntentUnavoid:
		if !scanner.CanHoldTarget {
			return ControlCapability{Available: false, DisabledReason: "hold target unavailable"}
		}
		return ControlCapability{Available: true}
	default:
		return ControlCapability{Available: true}
	}
}

func applyControlState(button *widget.Button, capability ControlCapability) {
	if button == nil {
		return
	}
	if capability.Available {
		button.Enable()
		return
	}
	button.Disable()
}

func applyRecordingsLoadResult(model *uiModel, list *widget.List, errLabel *widget.Label, playButton *widget.Button, deleteButton *widget.Button, recs []Recording, loadErr error, forceRefresh bool) {
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
		prevSelectedID := model.selectedID
		model.recordings = recs
		model.selectedClip = -1
		model.selectedID = ""
		if prevSelectedID != "" {
			for i := range recs {
				if recs[i].ID == prevSelectedID {
					model.selectedClip = i
					model.selectedID = recs[i].ID
					break
				}
			}
		}
	}
	selectedClip := model.selectedClip
	model.mu.Unlock()

	if hadErr {
		errLabel.SetText("")
		errLabel.Hide()
	}
	if dataChanged {
		setEnabled(playButton, selectedClip >= 0)
		setEnabled(deleteButton, selectedClip >= 0)
	}
	if changed {
		list.Refresh()
	}
	if dataChanged && selectedClip >= 0 {
		list.Select(selectedClip)
	}
}

func setEnabled(button *widget.Button, enabled bool) {
	if button == nil {
		return
	}
	if enabled {
		button.Enable()
		return
	}
	button.Disable()
}

func applyRecordingButtonState(button *widget.Button, state RuntimeState, pending bool, pendingStop bool, at time.Time) {
	if button == nil {
		return
	}
	label, enabled := recordingButtonPresentation(state, pending, pendingStop, at)
	button.SetText(label)
	setEnabled(button, enabled)
}

func recordingButtonPresentation(state RuntimeState, pending bool, pendingStop bool, at time.Time) (string, bool) {
	if pending {
		if pendingStop {
			return "Stopping...", false
		}
		return "Starting...", false
	}
	if state.Recording.Active {
		elapsed := formatRecordingElapsed(state.Recording.StartedAt, at)
		if state.Recording.Manual {
			return "Stop (" + elapsed + ")", true
		}
		return "Recording (" + elapsed + ")", false
	}
	if state.Scanner.Connected {
		return "Start Recording", true
	}
	return "Start Recording", false
}

func formatRecordingElapsed(startedAt, at time.Time) string {
	if startedAt.IsZero() {
		return "00:00"
	}
	if at.IsZero() {
		at = time.Now()
	}
	if at.Before(startedAt) {
		at = startedAt
	}
	total := int(at.Sub(startedAt) / time.Second)
	hours := total / 3600
	minutes := (total % 3600) / 60
	seconds := total % 60
	if hours > 0 {
		return fmt.Sprintf("%02d:%02d:%02d", hours, minutes, seconds)
	}
	return fmt.Sprintf("%02d:%02d", minutes, seconds)
}
