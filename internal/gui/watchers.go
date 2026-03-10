//go:build !headless

package gui

import (
	"context"
	"errors"
	"fmt"
	"image/color"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

func watchState(ctx context.Context, deps Dependencies, window fyne.Window, ui uiViews, model *uiModel) {
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
				showMonitorErrorDialog(window, model, state.Monitor.LastError)
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
				pendingControlAction := model.pendingControlAction
				model.mu.Unlock()
				applyRecordingButtonState(ui.startRecButton, state, pending, pendingStop, at)
				applyConnectionOverview(ui, model, state.Scanner, pendingControlAction, at)
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
			setConnectionIndicator(ui.connectionIndicator, false)
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
	pendingMonitorAction := model.pendingMonitorAction
	monitorListenAvailable := model.monitorListenAvailable
	monitorMuteAvailable := model.monitorMuteAvailable
	monitorApplyAvailable := model.monitorApplyAvailable
	model.mu.Unlock()

	scanner := state.Scanner
	if ui.connectionLabel != nil {
		ui.connectionLabel.SetText(connectionStatusText(scanner.Connected, fatalReceived, everConnected))
	}
	applyConnectionOverview(ui, model, scanner, pendingControlAction, time.Now())
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
	ui.tgidLabel.SetText(talkgroupOrDash(scanner.Talkgroup))
	ui.signalLabel.SetText(strconv.Itoa(scanner.Signal))
	ui.squelchLabel.SetText(fmt.Sprintf("%d (%s)", scanner.Squelch, boolWord(scanner.SquelchOpen, "open", "closed")))
	ui.volumeLabel.SetText(fmt.Sprintf("%d (%s)", scanner.Volume, boolWord(scanner.Mute, "muted", "unmuted")))

	holdIntent := IntentHoldCurrent
	holdLabel := "Hold"
	if scanner.Hold {
		holdIntent = IntentReleaseHold
		holdLabel = "Release Hold"
	}
	if ui.holdButton != nil {
		ui.holdButton.SetText(holdLabel)
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
	if ui.controlAvailability != nil {
		disabled := make([]string, 0, 10)
		addDisabled := func(name string, capability ControlCapability) {
			if capability.Available {
				return
			}
			reason := strings.TrimSpace(capability.DisabledReason)
			if reason == "" {
				reason = "unavailable"
			}
			disabled = append(disabled, fmt.Sprintf("%s (%s)", name, reason))
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
		ui.controlAvailability.SetText(controlAvailabilityText(disabled))
	}
	applyRecordingButtonState(ui.startRecButton, state, pendingRecordingAction, pendingRecordingStop, time.Now())
	if ui.monitorStatusLabel != nil {
		monitorState := "Stopped"
		if state.Monitor.Enabled {
			monitorState = "Listening"
		}
		if state.Monitor.Muted {
			monitorState += " (Muted)"
		}
		ui.monitorStatusLabel.SetText(monitorState)
	}
	if ui.monitorListenButton != nil {
		if state.Monitor.Enabled {
			ui.monitorListenButton.SetText("Stop Listening")
		} else {
			ui.monitorListenButton.SetText("Listen")
		}
		if !monitorListenAvailable || pendingMonitorAction {
			ui.monitorListenButton.Disable()
		} else {
			ui.monitorListenButton.Enable()
		}
	}
	if ui.monitorMuteButton != nil {
		if state.Monitor.Muted {
			ui.monitorMuteButton.SetText("Unmute")
		} else {
			ui.monitorMuteButton.SetText("Mute")
		}
		if !monitorMuteAvailable || pendingMonitorAction {
			ui.monitorMuteButton.Disable()
		} else {
			ui.monitorMuteButton.Enable()
		}
	}
	if ui.monitorApplyButton != nil {
		if !monitorApplyAvailable || pendingMonitorAction {
			ui.monitorApplyButton.Disable()
		} else {
			ui.monitorApplyButton.Enable()
		}
	}

	setExpertTabVisible(&ui, state.Expert.Enabled)
	if ui.listBrowseButton != nil {
		if !state.Expert.Enabled || !state.Scanner.Connected {
			ui.listBrowseButton.Disable()
		} else {
			ui.listBrowseButton.Enable()
		}
	}
	if ui.expertMenuStatus != nil {
		ui.expertMenuStatus.SetText(orDash(state.Expert.MenuStatusSummary))
	}
	if ui.expertAnalyze != nil {
		ui.expertAnalyze.SetText(orDash(state.Expert.AnalyzeSummary))
	}
	if ui.expertWaterfall != nil {
		ui.expertWaterfall.SetText(orDash(state.Expert.WaterfallSummary))
	}
	if ui.expertDateTime != nil {
		ui.expertDateTime.SetText(orDash(state.Expert.DateTimeSummary))
	}
	if ui.expertDateTimeEntry != nil && state.Expert.HasDateTime && !state.Expert.DateTimeValue.IsZero() {
		ui.expertDateTimeEntry.SetText(state.Expert.DateTimeValue.UTC().Format("2006-01-02 15:04:05"))
	}
	if ui.expertDSTCheck != nil && state.Expert.HasDateTime {
		ui.expertDSTCheck.SetChecked(state.Expert.DaylightSaving == 1)
	}
	if ui.expertLocation != nil {
		ui.expertLocation.SetText(orDash(state.Expert.LocationSummary))
	}
	if ui.expertLatEntry != nil && strings.TrimSpace(state.Expert.Latitude) != "" {
		ui.expertLatEntry.SetText(strings.TrimSpace(state.Expert.Latitude))
	}
	if ui.expertLonEntry != nil && strings.TrimSpace(state.Expert.Longitude) != "" {
		ui.expertLonEntry.SetText(strings.TrimSpace(state.Expert.Longitude))
	}
	if ui.expertRangeEntry != nil && strings.TrimSpace(state.Expert.Range) != "" {
		ui.expertRangeEntry.SetText(strings.TrimSpace(state.Expert.Range))
	}
	if ui.expertModel != nil {
		ui.expertModel.SetText(orDash(state.Expert.DeviceModel))
	}
	if ui.expertFirmware != nil {
		ui.expertFirmware.SetText(orDash(state.Expert.FirmwareVersion))
	}
	if ui.expertCharge != nil {
		ui.expertCharge.SetText(orDash(state.Expert.ChargeStatusSummary))
	}
	if ui.expertKeepAlive != nil {
		ui.expertKeepAlive.SetText(orDash(state.Expert.KeepAliveStatus))
	}

	applyExpertControlState := func(button *widget.Button, intent ControlIntent) {
		if button == nil {
			return
		}
		cap := capabilityFor(scanner, intent)
		if !state.Expert.Enabled {
			button.Disable()
			return
		}
		applyControlState(button, cap)
		if pendingControlAction {
			button.Disable()
		}
	}
	applyExpertControlState(ui.menuEnterButton, IntentMenuEnter)
	applyExpertControlState(ui.menuStatusButton, IntentMenuStatus)
	applyExpertControlState(ui.menuSetButton, IntentMenuSetValue)
	applyExpertControlState(ui.menuBackButton, IntentMenuBack)
	applyExpertControlState(ui.analyzeStartButton, IntentAnalyzeStart)
	applyExpertControlState(ui.analyzePauseButton, IntentAnalyzePause)
	applyExpertControlState(ui.pushWaterfallButton, IntentPushWaterfall)
	applyExpertControlState(ui.getWaterfallButton, IntentGetWaterfall)
	applyExpertControlState(ui.setDateTimeButton, IntentSetDateTime)
	applyExpertControlState(ui.getDateTimeButton, IntentGetDateTime)
	applyExpertControlState(ui.syncDateTimeButton, IntentSetDateTime)
	applyExpertControlState(ui.setLocationButton, IntentSetLocationRange)
	applyExpertControlState(ui.getLocationButton, IntentGetLocationRange)
	applyExpertControlState(ui.deviceInfoButton, IntentGetDeviceInfo)
	applyExpertControlState(ui.chargeButton, IntentGetChargeStatus)
	applyExpertControlState(ui.keepAliveButton, IntentKeepAlive)
	applyExpertControlState(ui.powerOffButton, IntentPowerOff)

	ui.activityList.Refresh()
	if ui.suppressedList != nil {
		ui.suppressedList.Refresh()
	}
}

func showMonitorErrorDialog(window fyne.Window, model *uiModel, monitorErr string) {
	if window == nil || model == nil {
		return
	}

	trimmed := strings.TrimSpace(monitorErr)
	if trimmed == "-" {
		trimmed = ""
	}

	model.mu.Lock()
	previous := model.lastMonitorError
	model.lastMonitorError = trimmed
	model.mu.Unlock()

	if trimmed == "" || trimmed == previous {
		return
	}

	dialog.ShowError(errors.New(trimmed), window)
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

func applyConnectionOverview(ui uiViews, model *uiModel, scanner ScannerStatus, pendingControlAction bool, at time.Time) {
	setConnectionIndicator(ui.connectionIndicator, scanner.Connected)
	if ui.connectionMetric != nil {
		ui.connectionMetric.SetText(connectionMetricText(model, scanner.UpdatedAt, pendingControlAction, at))
	}
}

func setConnectionIndicator(indicator *canvas.Circle, connected bool) {
	if indicator == nil {
		return
	}
	indicator.FillColor = connectionIndicatorColor(connected)
	indicator.Refresh()
}

func connectionIndicatorColor(connected bool) color.Color {
	if connected {
		return color.NRGBA{R: 46, G: 184, B: 74, A: 255}
	}
	return color.NRGBA{R: 212, G: 60, B: 60, A: 255}
}

func connectionMetricText(model *uiModel, updatedAt time.Time, pendingControlAction bool, at time.Time) string {
	queueDepth := 0
	if pendingControlAction {
		queueDepth = 1
	}
	latency := windowedTelemetryLatency(model, updatedAt, at)
	if latency < 0 {
		return fmt.Sprintf("Q%d | -", queueDepth)
	}
	return fmt.Sprintf("Q%d | %dms", queueDepth, latency.Milliseconds())
}

func controlAvailabilityText(disabled []string) string {
	switch len(disabled) {
	case 0:
		return ""
	case 1:
		return "Blocked: " + disabled[0]
	default:
		return fmt.Sprintf("Blocked: %s +%d", disabled[0], len(disabled)-1)
	}
}

func windowedTelemetryLatency(model *uiModel, updatedAt, at time.Time) time.Duration {
	if updatedAt.IsZero() {
		return -1
	}
	if at.IsZero() {
		at = time.Now()
	}
	sample := at.Sub(updatedAt)
	if sample < 0 {
		sample = 0
	}
	if model == nil {
		return sample
	}
	model.mu.Lock()
	defer model.mu.Unlock()

	if !model.metricInitialized {
		model.metricInitialized = true
		model.metricLastCommitAt = at
		model.metricWindowMax = sample
		model.metricWindowSamples = 1
		model.metricCommitted = sample
		return model.metricCommitted
	}

	if model.metricWindowSamples == 0 || sample > model.metricWindowMax {
		model.metricWindowMax = sample
	}
	model.metricWindowSamples++

	if at.Sub(model.metricLastCommitAt) >= 10*time.Second {
		model.metricCommitted = model.metricWindowMax
		model.metricLastCommitAt = at
		model.metricWindowMax = 0
		model.metricWindowSamples = 0
	}

	return model.metricCommitted
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
	if !forceRefresh {
		model.mu.Lock()
		pending := model.pendingRecordingAction
		model.mu.Unlock()
		if pending {
			return
		}
	}
	if loadErr != nil {
		model.mu.Lock()
		model.recordingsErr = loadErr.Error()
		ensureRecordingSelectionLocked(model)
		selectedClip := model.selectedClip
		hasSelection := len(model.selectedIDs) > 0
		model.mu.Unlock()
		errLabel.SetText("Recordings load error: " + loadErr.Error())
		errLabel.Show()
		setEnabled(playButton, selectedClip >= 0)
		setEnabled(deleteButton, hasSelection)
		return
	}

	model.mu.Lock()
	ensureRecordingSelectionLocked(model)
	dataChanged := !recordingsEqual(model.recordings, recs)
	changed := forceRefresh || dataChanged
	hadErr := model.recordingsErr != ""
	model.recordingsErr = ""
	if dataChanged {
		prevSelectedID := model.selectedID
		prevSelectedIDs := make(map[string]struct{}, len(model.selectedIDs))
		for id := range model.selectedIDs {
			prevSelectedIDs[id] = struct{}{}
		}
		model.recordings = recs
		clearRecordingSelectionLocked(model)
		for i := range recs {
			recID := recs[i].ID
			if _, ok := prevSelectedIDs[recID]; ok {
				model.selectedIDs[recID] = struct{}{}
				if recID == prevSelectedID {
					model.selectedClip = i
					model.selectedID = recID
				}
			}
		}
		syncPrimarySelectionLocked(model)
		if model.selectedClip >= 0 {
			model.selectionAnchor = model.selectedClip
		}
	}
	selectedClip := model.selectedClip
	hasSelection := len(model.selectedIDs) > 0
	model.mu.Unlock()

	if hadErr {
		errLabel.SetText("")
		errLabel.Hide()
	}
	if changed {
		setEnabled(playButton, selectedClip >= 0)
		setEnabled(deleteButton, hasSelection)
	}
	if changed {
		list.Refresh()
	}
	if dataChanged {
		model.mu.Lock()
		model.recordingsListSyncing = true
		model.lastSelectionModifier = 0
		model.mu.Unlock()
		if selectedClip >= 0 {
			list.Select(selectedClip)
		} else {
			list.UnselectAll()
		}
		model.mu.Lock()
		model.recordingsListSyncing = false
		model.mu.Unlock()
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
