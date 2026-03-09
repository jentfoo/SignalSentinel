//go:build !headless

package gui

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

func Run(ctx context.Context, deps Dependencies) error {
	if deps.SubscribeState == nil {
		return errors.New("gui subscribe callback is required")
	}
	if deps.ExecuteControl == nil {
		return errors.New("gui control callback is required")
	}
	if deps.StartRecording == nil {
		return errors.New("gui recording start callback is required")
	}
	if deps.StopRecording == nil {
		return errors.New("gui recording stop callback is required")
	}
	if deps.LoadRecordings == nil {
		return errors.New("gui recordings callback is required")
	}
	if deps.DeleteRecordings == nil {
		return errors.New("gui recordings delete callback is required")
	}
	if deps.SaveSettings == nil {
		return errors.New("gui save settings callback is required")
	}
	if strings.TrimSpace(deps.Title) == "" {
		deps.Title = "SignalSentinel"
	}

	fyneApp := app.NewWithID("github.com.jentfoo.sigsentinel")
	window := fyneApp.NewWindow(deps.Title)
	window.Resize(fyne.NewSize(1220, 780))

	initialRecordings, loadErr := deps.LoadRecordings()
	model := &uiModel{
		state:                  deps.InitialState,
		recordings:             initialRecordings,
		selectedClip:           -1,
		selectedIDs:            make(map[string]struct{}),
		selectionAnchor:        -1,
		activity:               normalizeActivitySettings(deps.InitialSettings.Activity),
		monitorListenAvailable: deps.SetMonitorListen != nil,
		monitorMuteAvailable:   deps.SetMonitorMute != nil,
		monitorApplyAvailable:  deps.SetMonitorGain != nil || deps.SetMonitorOutputDevice != nil,
	}
	if loadErr != nil {
		model.recordingsErr = loadErr.Error()
	}

	ui, stopPlayback := buildUI(model, deps, window)
	defer stopPlayback()
	window.SetContent(ui.content)

	stateCtx, cancelState := context.WithCancel(ctx)
	defer cancelState()

	var closeWindowOnce sync.Once
	closeWindow := func() {
		closeWindowOnce.Do(func() {
			window.SetCloseIntercept(nil)
			window.Close()
		})
	}
	requestShutdown := func() {
		cancelState()
		fyne.Do(closeWindow)
	}

	window.SetCloseIntercept(func() {
		cancelState()
		closeWindow()
	})

	go watchState(stateCtx, deps, ui, model)
	go pollRecordings(stateCtx, deps, ui, model)
	go watchRecordingDuration(stateCtx, ui, model)

	fatalErr := make(chan error, 1)
	if deps.Fatal != nil {
		go watchFatal(stateCtx, deps.Fatal, window, ui, model, fatalErr)
	}

	go func() {
		<-stateCtx.Done()
		requestShutdown()
	}()

	window.ShowAndRun()
	// Stop background watchers before reading fatalErr; deferred cancel handles early returns.
	cancelState()

	select {
	case err := <-fatalErr:
		return err
	default:
		return nil
	}
}

func buildUI(model *uiModel, deps Dependencies, window fyne.Window) (uiViews, func()) {
	var views uiViews

	statusPanel, runScanControl, applyControlResult := buildStatusPanel(model, deps, window, &views)
	scopePanel := buildScopePanel(model, deps, window, runScanControl, applyControlResult)
	views.activityList, views.suppressedList = buildActivityLists(model)
	recordingsPanel, stopPlayback := buildRecordingsPanel(model, deps, window, &views)
	expertPanel := buildExpertPanel(model, deps, window, runScanControl, &views)
	settingsPanel := buildSettingsPanel(model, deps, window, func(enabled bool) {
		setExpertTabVisible(&views, enabled)
	})

	tabs := container.NewAppTabs(
		container.NewTabItem("Status", statusPanel),
		container.NewTabItem("Scope", container.NewVScroll(scopePanel)),
		container.NewTabItem("Activity", container.NewAppTabs(
			container.NewTabItem("Sessions", views.activityList),
			container.NewTabItem("Suppressed (Diagnostics)", views.suppressedList),
		)),
		container.NewTabItem("Recordings", recordingsPanel),
		container.NewTabItem("Settings", settingsPanel),
	)
	expertTab := container.NewTabItem("Expert", container.NewVScroll(expertPanel))
	views.appTabs = tabs
	views.expertTab = expertTab

	model.mu.Lock()
	initialState := model.state
	pendingRecordingAction := model.pendingRecordingAction
	pendingRecordingStop := model.pendingRecordingStop
	model.mu.Unlock()

	setExpertTabVisible(&views, initialState.Expert.Enabled)
	views.content = tabs

	applyRecordingButtonState(views.startRecButton, initialState, pendingRecordingAction, pendingRecordingStop, time.Now())

	return views, stopPlayback
}

func setExpertTabVisible(views *uiViews, visible bool) {
	if views == nil || views.appTabs == nil || views.expertTab == nil {
		return
	}
	hasTab := false
	for _, item := range views.appTabs.Items {
		if item == views.expertTab {
			hasTab = true
			break
		}
	}
	switch {
	case visible && !hasTab:
		views.appTabs.Append(views.expertTab)
	case !visible && hasTab:
		views.appTabs.Remove(views.expertTab)
	}
}

func buildStatusPanel(model *uiModel, deps Dependencies, window fyne.Window, views *uiViews) (fyne.CanvasObject, func(ControlRequest), func(ControlResult)) {
	connectionLabel := widget.NewLabel("Connecting...")
	modeLabel := widget.NewLabel("-")
	lifecycleLabel := widget.NewLabel("Disconnected")
	sourceLabel := widget.NewLabel("-")
	freqLabel := widget.NewLabel("-")
	systemLabel := widget.NewLabel("-")
	deptLabel := widget.NewLabel("-")
	channelLabel := widget.NewLabel("-")
	tgidLabel := widget.NewLabel("-")
	signalLabel := widget.NewLabel("-")
	squelchLabel := widget.NewLabel("-")
	squelchLvlLabel := widget.NewLabel("-")
	muteLabel := widget.NewLabel("-")
	volumeLabel := widget.NewLabel("-")
	updatedLabel := widget.NewLabel("-")
	sourceLabel.Wrapping = fyne.TextWrapWord
	systemLabel.Wrapping = fyne.TextWrapWord
	deptLabel.Wrapping = fyne.TextWrapWord
	channelLabel.Wrapping = fyne.TextWrapWord
	tgidLabel.Wrapping = fyne.TextWrapWord

	holdButton := widget.NewButton("Hold", nil)
	nextButton := widget.NewButton("Next", nil)
	previousButton := widget.NewButton("Previous", nil)
	jumpTagButton := widget.NewButton("Jump Tag", nil)
	qshButton := widget.NewButton("Quick Search Hold", nil)
	jumpScanButton := widget.NewButton("Jump Scan", nil)
	jumpWXButton := widget.NewButton("Jump WX", nil)
	avoidButton := widget.NewButton("Avoid", nil)
	setVolumeButton := widget.NewButton("Set Volume", nil)
	setSquelchButton := widget.NewButton("Set Squelch", nil)
	commandAction := widget.NewLabel("-")
	commandStatus := widget.NewLabel("-")
	commandMessage := widget.NewLabel("-")
	commandRawReason := widget.NewLabel("-")
	commandRetryHint := widget.NewLabel("-")
	controlStatusSummary := widget.NewLabel("-")
	controlStatusSummary.Wrapping = fyne.TextWrapWord
	commandMessage.Wrapping = fyne.TextWrapWord
	commandRawReason.Wrapping = fyne.TextWrapWord
	commandRetryHint.Wrapping = fyne.TextWrapWord
	tagFavEntry := widget.NewEntry()
	tagFavEntry.SetText("0")
	tagSysEntry := widget.NewEntry()
	tagSysEntry.SetText("0")
	tagChanEntry := widget.NewEntry()
	tagChanEntry.SetText("0")
	qshFreqEntry := widget.NewEntry()
	qshFreqEntry.SetText("4600000")
	volumeEntry := widget.NewEntry()
	volumeEntry.SetText("10")
	volumeEntry.SetPlaceHolder("0-29")
	squelchEntry := widget.NewEntry()
	squelchEntry.SetText("5")
	squelchEntry.SetPlaceHolder("0-19")
	qshFreqField := container.NewGridWrap(fyne.NewSize(140, qshFreqEntry.MinSize().Height), qshFreqEntry)
	startRecButton := widget.NewButton("Start Recording", nil)
	monitorListenButton := widget.NewButton("Listen", nil)
	monitorMuteButton := widget.NewButton("Mute", nil)
	monitorGainEntry := widget.NewEntry()
	monitorGainEntry.SetText(fmt.Sprintf("%.1f", model.state.Monitor.GainDB))
	monitorOutputOptions := []string{"system-default"}
	if deps.ListMonitorOutputDevices != nil {
		if options, err := deps.ListMonitorOutputDevices(); err == nil && len(options) > 0 {
			monitorOutputOptions = dedupeMonitorOptions(options)
		}
	}
	monitorOutputSelect := widget.NewSelect(monitorOutputOptions, nil)
	monitorOutputDevice := strings.TrimSpace(model.state.Monitor.OutputDevice)
	if monitorOutputDevice == "" {
		monitorOutputDevice = "system-default"
	}
	if !containsString(monitorOutputOptions, monitorOutputDevice) {
		monitorOutputOptions = append(monitorOutputOptions, monitorOutputDevice)
		monitorOutputSelect.Options = dedupeMonitorOptions(monitorOutputOptions)
	}
	monitorOutputSelect.SetSelected(monitorOutputDevice)
	monitorApplyButton := widget.NewButton("Apply Monitor Settings", nil)
	monitorStatusLabel := widget.NewLabel("-")
	monitorErrorLabel := widget.NewLabel("-")
	monitorErrorLabel.Wrapping = fyne.TextWrapWord
	monitorListenAvailable := deps.SetMonitorListen != nil
	monitorMuteAvailable := deps.SetMonitorMute != nil
	monitorApplyAvailable := deps.SetMonitorGain != nil || deps.SetMonitorOutputDevice != nil
	holdButton.Disable()
	nextButton.Disable()
	previousButton.Disable()
	jumpTagButton.Disable()
	qshButton.Disable()
	jumpScanButton.Disable()
	jumpWXButton.Disable()
	avoidButton.Disable()
	setVolumeButton.Disable()
	setSquelchButton.Disable()
	startRecButton.Disable()
	monitorListenButton.Disable()
	monitorMuteButton.Disable()
	monitorApplyButton.Disable()

	applyControlResult := func(result ControlResult) {
		commandAction.SetText(orDash(result.Action) + " (" + orDash(result.Command) + ")")
		if result.Success {
			commandStatus.SetText("OK")
		} else if result.Unsupported {
			commandStatus.SetText("Unsupported")
		} else {
			commandStatus.SetText("Failed")
		}
		commandMessage.SetText(orDash(result.Message))
		commandRawReason.SetText(orDash(result.RawReason))
		commandRetryHint.SetText(orDash(result.RetryHint))
	}
	controlButtons := []*widget.Button{
		holdButton, nextButton, previousButton, avoidButton,
		jumpTagButton, qshButton, jumpScanButton, jumpWXButton,
		setVolumeButton, setSquelchButton,
	}
	setControlButtonsEnabled := func(enabled bool) {
		for _, btn := range controlButtons {
			if enabled {
				btn.Enable()
			} else {
				btn.Disable()
			}
		}
	}
	refreshControlButtonsFromModel := func() {
		model.mu.Lock()
		scanner := model.state.Scanner
		pending := model.pendingControlAction
		model.mu.Unlock()

		holdIntent := IntentHoldCurrent
		if scanner.Hold {
			holdIntent = IntentReleaseHold
		}
		avoidIntent := IntentAvoid
		if scanner.AvoidKnown && scanner.Avoided {
			avoidIntent = IntentUnavoid
		}
		applyControlState(holdButton, capabilityFor(scanner, holdIntent))
		applyControlState(nextButton, capabilityFor(scanner, IntentNext))
		applyControlState(previousButton, capabilityFor(scanner, IntentPrevious))
		applyControlState(jumpTagButton, capabilityFor(scanner, IntentJumpNumberTag))
		applyControlState(qshButton, capabilityFor(scanner, IntentQuickSearchHold))
		applyControlState(jumpScanButton, capabilityFor(scanner, IntentJumpMode))
		applyControlState(jumpWXButton, capabilityFor(scanner, IntentJumpMode))
		applyControlState(avoidButton, capabilityFor(scanner, avoidIntent))
		applyControlState(setVolumeButton, capabilityFor(scanner, IntentSetVolume))
		applyControlState(setSquelchButton, capabilityFor(scanner, IntentSetSquelch))
		if pending {
			setControlButtonsEnabled(false)
		}
	}
	runScanControl := func(req ControlRequest) {
		model.mu.Lock()
		if model.pendingControlAction {
			model.mu.Unlock()
			return
		}
		model.pendingControlAction = true
		model.mu.Unlock()
		setControlButtonsEnabled(false)
		go func() {
			result := deps.ExecuteControl(req)
			fyne.Do(func() {
				model.mu.Lock()
				model.pendingControlAction = false
				model.mu.Unlock()
				refreshControlButtonsFromModel()
				if !result.Success {
					summary := strings.TrimSpace(result.Message)
					if summary == "" || summary == "-" {
						summary = "command failed"
					}
					raw := strings.TrimSpace(result.RawReason)
					if raw != "" && raw != "-" {
						summary = summary + ": " + raw
					}
					dialog.ShowInformation("Scanner Control", summary, window)
				}
				applyControlResult(result)
			})
		}()
	}
	holdButton.OnTapped = func() {
		model.mu.Lock()
		holding := model.state.Scanner.Hold
		model.mu.Unlock()
		intent := IntentHoldCurrent
		if holding {
			intent = IntentReleaseHold
		}
		runScanControl(ControlRequest{Intent: intent})
	}
	nextButton.OnTapped = func() {
		runScanControl(ControlRequest{Intent: IntentNext})
	}
	previousButton.OnTapped = func() {
		runScanControl(ControlRequest{Intent: IntentPrevious})
	}
	jumpTagButton.OnTapped = func() {
		fav, err := parseIntEntry("Favorites tag", tagFavEntry)
		if err != nil {
			dialog.ShowError(err, window)
			return
		}
		sys, err := parseIntEntry("System tag", tagSysEntry)
		if err != nil {
			dialog.ShowError(err, window)
			return
		}
		chanTag, err := parseIntEntry("Channel tag", tagChanEntry)
		if err != nil {
			dialog.ShowError(err, window)
			return
		}
		if fav < 0 || fav > 99 {
			dialog.ShowError(errors.New("favorites tag must be in range 0-99"), window)
			return
		}
		if sys < 0 || sys > 99 {
			dialog.ShowError(errors.New("system tag must be in range 0-99"), window)
			return
		}
		if chanTag < 0 || chanTag > 999 {
			dialog.ShowError(errors.New("channel tag must be in range 0-999"), window)
			return
		}
		runScanControl(ControlRequest{
			Intent:    IntentJumpNumberTag,
			NumberTag: NumberTag{Favorites: fav, System: sys, Channel: chanTag},
		})
	}
	qshButton.OnTapped = func() {
		freq, err := parseIntEntry("Quick search frequency", qshFreqEntry)
		if err != nil {
			dialog.ShowError(err, window)
			return
		}
		runScanControl(ControlRequest{
			Intent:      IntentQuickSearchHold,
			FrequencyHz: freq,
		})
	}
	jumpScanButton.OnTapped = func() {
		runScanControl(ControlRequest{
			Intent:    IntentJumpMode,
			JumpMode:  "SCN_MODE",
			JumpIndex: "0xFFFFFFFF",
		})
	}
	jumpWXButton.OnTapped = func() {
		runScanControl(ControlRequest{
			Intent:    IntentJumpMode,
			JumpMode:  "WX_MODE",
			JumpIndex: "NORMAL",
		})
	}
	avoidButton.OnTapped = func() {
		model.mu.Lock()
		state := model.state.Scanner
		model.mu.Unlock()
		intent := IntentAvoid
		if state.AvoidKnown && state.Avoided {
			intent = IntentUnavoid
		} else if !state.AvoidKnown {
			// Unknown state — toggle toward avoid
			intent = IntentAvoid
		}
		runScanControl(ControlRequest{Intent: intent})
	}
	setVolumeButton.OnTapped = func() {
		level, err := parseIntEntry("Volume", volumeEntry)
		if err != nil {
			dialog.ShowError(err, window)
			return
		}
		runScanControl(ControlRequest{
			Intent: IntentSetVolume,
			Volume: level,
		})
	}
	setSquelchButton.OnTapped = func() {
		level, err := parseIntEntry("Squelch", squelchEntry)
		if err != nil {
			dialog.ShowError(err, window)
			return
		}
		runScanControl(ControlRequest{
			Intent:  IntentSetSquelch,
			Squelch: level,
		})
	}
	startRecButton.OnTapped = func() {
		model.mu.Lock()
		if model.pendingRecordingAction {
			model.mu.Unlock()
			return
		}
		state := model.state
		if state.Recording.Active && !state.Recording.Manual {
			model.mu.Unlock()
			return
		}
		if !state.Recording.Active && !state.Scanner.Connected {
			model.mu.Unlock()
			return
		}
		model.pendingRecordingAction = true
		model.pendingRecordingStop = state.Recording.Active
		model.mu.Unlock()

		applyRecordingButtonState(startRecButton, state, true, state.Recording.Active, time.Now())

		go func() {
			var err error
			if state.Recording.Active {
				err = deps.StopRecording()
			} else {
				err = deps.StartRecording()
			}
			fyne.Do(func() {
				model.mu.Lock()
				model.pendingRecordingAction = false
				model.pendingRecordingStop = false
				latest := model.state
				pending := model.pendingRecordingAction
				pendingStop := model.pendingRecordingStop
				model.mu.Unlock()
				if err != nil {
					dialog.ShowError(err, window)
				}
				applyRecordingButtonState(startRecButton, latest, pending, pendingStop, time.Now())
			})
		}()
	}
	setMonitorButtonsEnabled := func(enabled bool) {
		setEnabled(monitorListenButton, enabled && monitorListenAvailable)
		setEnabled(monitorMuteButton, enabled && monitorMuteAvailable)
		setEnabled(monitorApplyButton, enabled && monitorApplyAvailable)
	}
	runMonitorAction := func(action func() error) {
		model.mu.Lock()
		if model.pendingMonitorAction {
			model.mu.Unlock()
			return
		}
		model.pendingMonitorAction = true
		model.mu.Unlock()
		setMonitorButtonsEnabled(false)
		go func() {
			err := action()
			fyne.Do(func() {
				model.mu.Lock()
				model.pendingMonitorAction = false
				model.mu.Unlock()
				setMonitorButtonsEnabled(true)
				if err != nil {
					dialog.ShowError(err, window)
				}
			})
		}()
	}
	monitorListenButton.OnTapped = func() {
		if !monitorListenAvailable {
			return
		}
		model.mu.Lock()
		next := !model.state.Monitor.Enabled
		model.mu.Unlock()
		runMonitorAction(func() error {
			return deps.SetMonitorListen(next)
		})
	}
	monitorMuteButton.OnTapped = func() {
		if !monitorMuteAvailable {
			return
		}
		model.mu.Lock()
		next := !model.state.Monitor.Muted
		model.mu.Unlock()
		runMonitorAction(func() error {
			return deps.SetMonitorMute(next)
		})
	}
	monitorApplyButton.OnTapped = func() {
		if !monitorApplyAvailable {
			return
		}
		gainText := strings.TrimSpace(monitorGainEntry.Text)
		if gainText == "" {
			gainText = "0"
		}
		gainValue, err := strconv.ParseFloat(gainText, 64)
		if err != nil {
			dialog.ShowError(errors.New("monitor gain must be a number"), window)
			return
		}
		outputDevice := strings.TrimSpace(monitorOutputSelect.Selected)
		if outputDevice == "" {
			outputDevice = "system-default"
		}
		runMonitorAction(func() error {
			var applyErr error
			if deps.SetMonitorGain != nil {
				applyErr = deps.SetMonitorGain(gainValue)
			}
			if applyErr == nil && deps.SetMonitorOutputDevice != nil {
				applyErr = deps.SetMonitorOutputDevice(outputDevice)
			}
			return applyErr
		})
	}
	setMonitorButtonsEnabled(true)

	views.connectionLabel = connectionLabel
	views.modeLabel = modeLabel
	views.lifecycleLabel = lifecycleLabel
	views.sourceLabel = sourceLabel
	views.freqLabel = freqLabel
	views.systemLabel = systemLabel
	views.deptLabel = deptLabel
	views.channelLabel = channelLabel
	views.tgidLabel = tgidLabel
	views.signalLabel = signalLabel
	views.squelchLabel = squelchLabel
	views.squelchLvlLabel = squelchLvlLabel
	views.muteLabel = muteLabel
	views.volumeLabel = volumeLabel
	views.updatedLabel = updatedLabel
	views.holdButton = holdButton
	views.nextButton = nextButton
	views.previousButton = previousButton
	views.jumpTagButton = jumpTagButton
	views.qshButton = qshButton
	views.jumpScanButton = jumpScanButton
	views.jumpWXButton = jumpWXButton
	views.avoidButton = avoidButton
	views.setVolumeButton = setVolumeButton
	views.setSquelchButton = setSquelchButton
	views.commandAction = commandAction
	views.commandStatus = commandStatus
	views.commandMessage = commandMessage
	views.commandRawReason = commandRawReason
	views.commandRetryHint = commandRetryHint
	views.controlStatus = controlStatusSummary
	views.tagFavEntry = tagFavEntry
	views.tagSysEntry = tagSysEntry
	views.tagChanEntry = tagChanEntry
	views.qshFreqEntry = qshFreqEntry
	views.volumeEntry = volumeEntry
	views.squelchEntry = squelchEntry
	views.startRecButton = startRecButton
	views.monitorListenButton = monitorListenButton
	views.monitorMuteButton = monitorMuteButton
	views.monitorGainEntry = monitorGainEntry
	views.monitorOutputSelect = monitorOutputSelect
	views.monitorApplyButton = monitorApplyButton
	views.monitorStatusLabel = monitorStatusLabel
	views.monitorErrorLabel = monitorErrorLabel

	commandField := func(label string, value *widget.Label) fyne.CanvasObject {
		labelWidget := widget.NewLabel(label)
		labelSlot := container.NewGridWrap(fyne.NewSize(84, labelWidget.MinSize().Height), labelWidget)
		return container.NewBorder(nil, nil, labelSlot, nil, value)
	}
	statusField := func(label string, value *widget.Label) *widget.FormItem {
		return widget.NewFormItem(label, value)
	}
	statusSummary := widget.NewCard("Current Scanner", "", container.NewVBox(
		widget.NewLabelWithStyle("Session", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewForm(
			statusField("Connection", connectionLabel),
			statusField("State", lifecycleLabel),
			statusField("Mode", modeLabel),
			statusField("View", sourceLabel),
			statusField("Updated", updatedLabel),
			statusField("Signal", signalLabel),
		),
		widget.NewSeparator(),
		widget.NewLabelWithStyle("Channel", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewForm(
			statusField("Frequency", freqLabel),
			statusField("System", systemLabel),
			statusField("Department", deptLabel),
			statusField("Channel", channelLabel),
			statusField("Talkgroup", tgidLabel),
		),
		widget.NewSeparator(),
		widget.NewLabelWithStyle("Audio", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewForm(
			statusField("Squelch", squelchLabel),
			statusField("SQL Level", squelchLvlLabel),
			statusField("Mute", muteLabel),
			statusField("Volume", volumeLabel),
		),
	))

	availabilityPanel := widget.NewCard("Control Availability", "", controlStatusSummary)
	commandPanel := widget.NewCard("Last Command", "", container.NewVBox(
		commandField("Action", commandAction),
		commandField("Status", commandStatus),
		commandField("Message", commandMessage),
		commandField("Raw", commandRawReason),
		commandField("Retry", commandRetryHint),
	))

	controls := container.NewVBox(
		widget.NewLabel("Live Monitor"),
		container.NewHBox(monitorListenButton, monitorMuteButton, startRecButton),
		container.NewHBox(
			widget.NewLabel("Gain dB"),
			container.NewGridWrap(fyne.NewSize(100, monitorGainEntry.MinSize().Height), monitorGainEntry),
			widget.NewLabel("Output"),
			container.NewGridWrap(fyne.NewSize(220, monitorOutputSelect.MinSize().Height), monitorOutputSelect),
			monitorApplyButton,
		),
		widget.NewForm(
			statusField("Monitor", monitorStatusLabel),
			statusField("Last Error", monitorErrorLabel),
		),
		widget.NewSeparator(),
		widget.NewLabel("Scanner Control"),
		container.NewHBox(holdButton, nextButton, previousButton, avoidButton),
		container.NewHBox(
			widget.NewLabel("Fav"),
			tagFavEntry,
			widget.NewLabel("Sys"),
			tagSysEntry,
			widget.NewLabel("Chan"),
			tagChanEntry,
			jumpTagButton,
		),
		container.NewHBox(
			widget.NewLabel("Freq Hz"),
			qshFreqField,
			qshButton,
		),
		container.NewHBox(jumpScanButton, jumpWXButton),
		container.NewHBox(
			widget.NewLabel("Volume"),
			container.NewGridWrap(fyne.NewSize(100, volumeEntry.MinSize().Height), volumeEntry),
			setVolumeButton,
			widget.NewLabel("Squelch"),
			container.NewGridWrap(fyne.NewSize(100, squelchEntry.MinSize().Height), squelchEntry),
			setSquelchButton,
		),
		widget.NewSeparator(),
		availabilityPanel,
		commandPanel,
	)
	controlsScroll := container.NewVScroll(controls)
	statusSplit := container.NewHSplit(container.NewPadded(statusSummary), controlsScroll)
	statusSplit.SetOffset(0.40)

	return statusSplit, runScanControl, applyControlResult
}

func buildScopePanel(model *uiModel, deps Dependencies, window fyne.Window, runScanControl func(ControlRequest), applyControlResult func(ControlResult)) fyne.CanvasObject {
	scopeFavEntry := widget.NewEntry()
	scopeFavEntry.SetText("0")
	scopeSysEntry := widget.NewEntry()
	scopeSysEntry.SetText("0")
	fqkEntry := widget.NewMultiLineEntry()
	sqkEntry := widget.NewMultiLineEntry()
	dqkEntry := widget.NewMultiLineEntry()
	svcEntry := widget.NewMultiLineEntry()
	filterEntry := widget.NewEntry()
	previewLabel := widget.NewLabel("-")
	scopeTargetSelect := widget.NewSelect([]string{"Favorites", "Systems", "Departments", "Service Types"}, nil)
	scopeTargetSelect.SetSelected("Favorites")
	loadScopeButton := widget.NewButton("Load Scope", nil)
	applyFQKButton := widget.NewButton("Apply Favorites", nil)
	applySQKButton := widget.NewButton("Apply Systems", nil)
	applyDQKButton := widget.NewButton("Apply Departments", nil)
	applySVCButton := widget.NewButton("Apply Service Types", nil)
	selectAllButton := widget.NewButton("Select All", nil)
	selectNoneButton := widget.NewButton("Select None", nil)
	svcDefaultsButton := widget.NewButton("Service Type Defaults", nil)
	svcResetButton := widget.NewButton("Reset Service Types", nil)
	profileNameEntry := widget.NewEntry()
	profileNameEntry.SetPlaceHolder("profile name")
	profileSelect := widget.NewSelect([]string{}, nil)
	saveProfileButton := widget.NewButton("Save Profile", nil)
	loadProfileButton := widget.NewButton("Edit Profile", nil)
	applyProfileButton := widget.NewButton("Apply Profile", nil)
	deleteProfileButton := widget.NewButton("Delete Profile", nil)
	refreshProfilesButton := widget.NewButton("Refresh Profiles", nil)

	stagedFQK := make([]int, 100)
	stagedSQK := make([]int, 100)
	stagedDQK := make([]int, 100)
	stagedSVC := allEnabledState(47)
	lastLoadedSVC := copyInts(stagedSVC)
	fqkEntry.SetText(encodeIndexList(binaryStateToIndexes(stagedFQK)))
	sqkEntry.SetText(encodeIndexList(binaryStateToIndexes(stagedSQK)))
	dqkEntry.SetText(encodeIndexList(binaryStateToIndexes(stagedDQK)))
	svcEntry.SetText(encodeIndexList(binaryStateToIndexes(stagedSVC)))

	activeScopeEntry := func() *widget.Entry {
		switch scopeTargetSelect.Selected {
		case "Systems":
			return sqkEntry
		case "Departments":
			return dqkEntry
		case "Service Types":
			return svcEntry
		default:
			return fqkEntry
		}
	}
	refreshScopePreview := func() {
		entry := activeScopeEntry()
		maxIndex := 99
		if entry == svcEntry {
			maxIndex = 46
		}
		indexes, err := parseIndexList(entry.Text, maxIndex)
		if err != nil {
			previewLabel.SetText("preview unavailable: " + err.Error())
			return
		}
		filtered := filterIndexes(indexes, filterEntry.Text)
		previewLabel.SetText(fmt.Sprintf("staged %s: %d selected (%s)", scopeTargetSelect.Selected, len(indexes), encodeIndexList(filtered)))
	}
	filterEntry.OnChanged = func(_ string) {
		refreshScopePreview()
	}
	scopeTargetSelect.OnChanged = func(_ string) {
		refreshScopePreview()
	}

	parseScopeState := func(name string, entry *widget.Entry, maxIndex int, length int) ([]int, error) {
		indexes, err := parseIndexList(entry.Text, maxIndex)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		return indexesToBinaryState(indexes, length), nil
	}
	loadScopeContext := func() (int, int, error) {
		fav, err := parseIntEntry("Scope favorites quick key", scopeFavEntry)
		if err != nil {
			return 0, 0, err
		}
		sys, err := parseIntEntry("Scope system quick key", scopeSysEntry)
		if err != nil {
			return 0, 0, err
		}
		if fav < 0 || fav > 99 {
			return 0, 0, errors.New("scope favorites quick key must be in range 0-99")
		}
		if sys < 0 || sys > 99 {
			return 0, 0, errors.New("scope system quick key must be in range 0-99")
		}
		return fav, sys, nil
	}

	applyFQKButton.OnTapped = func() {
		state, err := parseScopeState("FQK", fqkEntry, 99, 100)
		if err != nil {
			dialog.ShowError(err, window)
			return
		}
		stagedFQK = state
		runScanControl(ControlRequest{
			Intent:         IntentSetFQK,
			QuickKeyValues: copyInts(state),
		})
		refreshScopePreview()
	}
	applySQKButton.OnTapped = func() {
		fav, _, err := loadScopeContext()
		if err != nil {
			dialog.ShowError(err, window)
			return
		}
		state, err := parseScopeState("SQK", sqkEntry, 99, 100)
		if err != nil {
			dialog.ShowError(err, window)
			return
		}
		stagedSQK = state
		runScanControl(ControlRequest{
			Intent:            IntentSetSQK,
			ScopeFavoritesTag: fav,
			QuickKeyValues:    copyInts(state),
		})
		refreshScopePreview()
	}
	applyDQKButton.OnTapped = func() {
		fav, sys, err := loadScopeContext()
		if err != nil {
			dialog.ShowError(err, window)
			return
		}
		state, err := parseScopeState("DQK", dqkEntry, 99, 100)
		if err != nil {
			dialog.ShowError(err, window)
			return
		}
		stagedDQK = state
		runScanControl(ControlRequest{
			Intent:            IntentSetDQK,
			ScopeFavoritesTag: fav,
			ScopeSystemTag:    sys,
			QuickKeyValues:    copyInts(state),
		})
		refreshScopePreview()
	}
	applySVCButton.OnTapped = func() {
		state, err := parseScopeState("SVC", svcEntry, 46, 47)
		if err != nil {
			dialog.ShowError(err, window)
			return
		}
		stagedSVC = state
		runScanControl(ControlRequest{
			Intent:       IntentSetServiceTypes,
			ServiceTypes: copyInts(state),
		})
		refreshScopePreview()
	}
	selectAllButton.OnTapped = func() {
		entry := activeScopeEntry()
		maxIndex := 99
		if entry == svcEntry {
			maxIndex = 46
		}
		all := make([]int, maxIndex+1)
		for i := 0; i <= maxIndex; i++ {
			all[i] = i
		}
		entry.SetText(encodeIndexList(all))
		refreshScopePreview()
	}
	selectNoneButton.OnTapped = func() {
		entry := activeScopeEntry()
		entry.SetText("")
		refreshScopePreview()
	}
	svcDefaultsButton.OnTapped = func() {
		defaultState := allEnabledState(47)
		stagedSVC = defaultState
		svcEntry.SetText(encodeIndexList(binaryStateToIndexes(defaultState)))
		refreshScopePreview()
	}
	svcResetButton.OnTapped = func() {
		stagedSVC = copyInts(lastLoadedSVC)
		svcEntry.SetText(encodeIndexList(binaryStateToIndexes(stagedSVC)))
		refreshScopePreview()
	}

	if deps.LoadScanScope != nil {
		loadScopeButton.OnTapped = func() {
			fav, sys, err := loadScopeContext()
			if err != nil {
				dialog.ShowError(err, window)
				return
			}
			go func() {
				scope, loadErr := deps.LoadScanScope(fav, sys)
				fyne.Do(func() {
					if loadErr != nil {
						dialog.ShowError(loadErr, window)
						return
					}
					stagedFQK = copyInts(scope.FavoritesQuickKeys)
					stagedSQK = copyInts(scope.SystemQuickKeys)
					stagedDQK = copyInts(scope.DepartmentQuickKeys)
					stagedSVC = copyInts(scope.ServiceTypes)
					lastLoadedSVC = copyInts(scope.ServiceTypes)
					fqkEntry.SetText(encodeIndexList(binaryStateToIndexes(stagedFQK)))
					sqkEntry.SetText(encodeIndexList(binaryStateToIndexes(stagedSQK)))
					dqkEntry.SetText(encodeIndexList(binaryStateToIndexes(stagedDQK)))
					svcEntry.SetText(encodeIndexList(binaryStateToIndexes(stagedSVC)))
					refreshScopePreview()
				})
			}()
		}
	} else {
		loadScopeButton.Disable()
	}

	refreshProfiles := func() {
		if deps.LoadScanProfiles == nil {
			profileSelect.Options = nil
			profileSelect.SetSelected("")
			saveProfileButton.Disable()
			loadProfileButton.Disable()
			applyProfileButton.Disable()
			deleteProfileButton.Disable()
			refreshProfilesButton.Disable()
			return
		}
		go func() {
			profiles, err := deps.LoadScanProfiles()
			fyne.Do(func() {
				if err != nil {
					dialog.ShowError(err, window)
					return
				}
				names := make([]string, 0, len(profiles))
				for _, profile := range profiles {
					names = append(names, profile.Name)
				}
				profileSelect.Options = names
				profileSelect.Refresh()
				if len(names) > 0 {
					if profileSelect.Selected == "" {
						profileSelect.SetSelected(names[0])
					}
				} else {
					profileSelect.SetSelected("")
				}
			})
		}()
	}
	refreshProfilesButton.OnTapped = refreshProfiles

	if deps.SaveScanProfile != nil {
		saveProfileButton.OnTapped = func() {
			name := strings.TrimSpace(profileNameEntry.Text)
			if name == "" {
				dialog.ShowError(errors.New("profile name is required"), window)
				return
			}
			fav, sys, err := loadScopeContext()
			if err != nil {
				dialog.ShowError(err, window)
				return
			}
			fqkState, err := parseScopeState("FQK", fqkEntry, 99, 100)
			if err != nil {
				dialog.ShowError(err, window)
				return
			}
			sqkState, err := parseScopeState("SQK", sqkEntry, 99, 100)
			if err != nil {
				dialog.ShowError(err, window)
				return
			}
			dqkState, err := parseScopeState("DQK", dqkEntry, 99, 100)
			if err != nil {
				dialog.ShowError(err, window)
				return
			}
			svcState, err := parseScopeState("SVC", svcEntry, 46, 47)
			if err != nil {
				dialog.ShowError(err, window)
				return
			}
			profile := ScanProfile{
				Name:               name,
				FavoritesQuickKeys: fqkState,
				ServiceTypes:       svcState,
				SystemQuickKeys: map[string][]int{
					strconv.Itoa(fav): sqkState,
				},
				DepartmentQuickKeys: map[string][]int{
					fmt.Sprintf("%d:%d", fav, sys): dqkState,
				},
			}
			go func() {
				err := deps.SaveScanProfile(profile)
				fyne.Do(func() {
					if err != nil {
						dialog.ShowError(err, window)
						return
					}
					profileNameEntry.SetText(name)
					refreshProfiles()
				})
			}()
		}
	} else {
		saveProfileButton.Disable()
	}
	if deps.DeleteScanProfile != nil {
		deleteProfileButton.OnTapped = func() {
			name := strings.TrimSpace(profileSelect.Selected)
			if name == "" {
				dialog.ShowError(errors.New("select a profile first"), window)
				return
			}
			go func() {
				err := deps.DeleteScanProfile(name)
				fyne.Do(func() {
					if err != nil {
						dialog.ShowError(err, window)
						return
					}
					refreshProfiles()
				})
			}()
		}
	} else {
		deleteProfileButton.Disable()
	}

	loadProfileToStaged := func(profileName string) {
		if deps.LoadScanProfiles == nil {
			return
		}
		fav, sys, err := loadScopeContext()
		if err != nil {
			dialog.ShowError(err, window)
			return
		}
		go func() {
			profiles, loadErr := deps.LoadScanProfiles()
			fyne.Do(func() {
				if loadErr != nil {
					dialog.ShowError(loadErr, window)
					return
				}
				for _, profile := range profiles {
					if !strings.EqualFold(strings.TrimSpace(profile.Name), strings.TrimSpace(profileName)) {
						continue
					}
					stagedFQK = copyInts(profile.FavoritesQuickKeys)
					profileNameEntry.SetText(profileName)
					stagedSQK = make([]int, 100)
					stagedDQK = make([]int, 100)
					stagedSVC = copyInts(profile.ServiceTypes)
					lastLoadedSVC = copyInts(profile.ServiceTypes)
					for key, values := range profile.SystemQuickKeys {
						tag, convErr := strconv.Atoi(strings.TrimSpace(key))
						if convErr == nil && tag == fav {
							stagedSQK = copyInts(values)
							break
						}
					}
					for key, values := range profile.DepartmentQuickKeys {
						parts := strings.Split(strings.TrimSpace(key), ":")
						if len(parts) != 2 {
							continue
						}
						favTag, favErr := strconv.Atoi(strings.TrimSpace(parts[0]))
						sysTag, sysErr := strconv.Atoi(strings.TrimSpace(parts[1]))
						if favErr == nil && sysErr == nil && favTag == fav && sysTag == sys {
							stagedDQK = copyInts(values)
							break
						}
					}
					fqkEntry.SetText(encodeIndexList(binaryStateToIndexes(stagedFQK)))
					sqkEntry.SetText(encodeIndexList(binaryStateToIndexes(stagedSQK)))
					dqkEntry.SetText(encodeIndexList(binaryStateToIndexes(stagedDQK)))
					svcEntry.SetText(encodeIndexList(binaryStateToIndexes(stagedSVC)))
					refreshScopePreview()
					return
				}
				dialog.ShowError(fmt.Errorf("scan profile not found: %s", profileName), window)
			})
		}()
	}
	loadProfileButton.OnTapped = func() {
		name := strings.TrimSpace(profileSelect.Selected)
		if name == "" {
			dialog.ShowError(errors.New("select a profile first"), window)
			return
		}
		loadProfileToStaged(name)
	}

	if deps.ApplyScanProfile != nil {
		applyProfileButton.OnTapped = func() {
			name := strings.TrimSpace(profileSelect.Selected)
			if name == "" {
				dialog.ShowError(errors.New("select a profile first"), window)
				return
			}
			fav, sys, err := loadScopeContext()
			if err != nil {
				dialog.ShowError(err, window)
				return
			}
			go func() {
				err := deps.ApplyScanProfile(name, fav, sys)
				fyne.Do(func() {
					if err != nil {
						dialog.ShowError(err, window)
						return
					}
					applyControlResult(ControlResult{
						Action:    "Apply Profile",
						Command:   "FQK/SQK/DQK/SVC",
						Success:   true,
						Message:   "profile applied",
						RawReason: "-",
						RetryHint: "-",
					})
				})
			}()
		}
	} else {
		applyProfileButton.Disable()
	}
	refreshProfiles()
	refreshScopePreview()

	return container.NewVBox(
		widget.NewLabel("Scan Scope Control"),
		container.NewHBox(
			widget.NewLabel("Fav QK"),
			container.NewGridWrap(fyne.NewSize(80, scopeFavEntry.MinSize().Height), scopeFavEntry),
			widget.NewLabel("Sys QK"),
			container.NewGridWrap(fyne.NewSize(80, scopeSysEntry.MinSize().Height), scopeSysEntry),
			loadScopeButton,
		),
		widget.NewForm(
			widget.NewFormItem("Favorites Quick Keys \u2014 On Indexes (0-99)", fqkEntry),
			widget.NewFormItem("System Quick Keys \u2014 On Indexes (0-99)", sqkEntry),
			widget.NewFormItem("Department Quick Keys \u2014 On Indexes (0-99)", dqkEntry),
			widget.NewFormItem("Service Types \u2014 On Indexes (0-46)", svcEntry),
		),
		container.NewHBox(applyFQKButton, applySQKButton, applyDQKButton, applySVCButton),
		container.NewHBox(scopeTargetSelect, filterEntry, selectAllButton, selectNoneButton),
		container.NewHBox(svcDefaultsButton, svcResetButton),
		previewLabel,
		widget.NewSeparator(),
		widget.NewLabel("Scan Profiles"),
		container.NewHBox(profileNameEntry, saveProfileButton, refreshProfilesButton),
		container.NewHBox(profileSelect, loadProfileButton, applyProfileButton, deleteProfileButton),
	)
}

func buildExpertPanel(model *uiModel, deps Dependencies, window fyne.Window, runScanControl func(ControlRequest), views *uiViews) fyne.CanvasObject {
	menuStatusLabel := widget.NewLabel("-")
	analyzeLabel := widget.NewLabel("-")
	waterfallLabel := widget.NewLabel("-")
	dateTimeLabel := widget.NewLabel("-")
	locationLabel := widget.NewLabel("-")
	modelLabel := widget.NewLabel("-")
	firmwareLabel := widget.NewLabel("-")
	chargeLabel := widget.NewLabel("-")
	keepAliveLabel := widget.NewLabel("-")
	menuStatusLabel.Wrapping = fyne.TextWrapWord
	analyzeLabel.Wrapping = fyne.TextWrapWord
	waterfallLabel.Wrapping = fyne.TextWrapWord
	dateTimeLabel.Wrapping = fyne.TextWrapWord
	locationLabel.Wrapping = fyne.TextWrapWord
	modelLabel.Wrapping = fyne.TextWrapWord
	firmwareLabel.Wrapping = fyne.TextWrapWord
	chargeLabel.Wrapping = fyne.TextWrapWord
	keepAliveLabel.Wrapping = fyne.TextWrapWord

	menuIDEntry := widget.NewEntry()
	menuIDEntry.SetText("TOP")
	menuIndexEntry := widget.NewEntry()
	menuValueEntry := widget.NewEntry()
	menuBackEntry := widget.NewEntry()
	menuBackEntry.SetText("RETURN_PREVOUS_MODE")

	analyzeModeEntry := widget.NewEntry()
	analyzeModeEntry.SetText("SYSTEM_STATUS")
	analyzeParamsEntry := widget.NewEntry()
	analyzeParamsEntry.SetPlaceHolder("param1,param2")

	fftTypeEntry := widget.NewEntry()
	fftTypeEntry.SetText("1")
	fftEnabled := widget.NewCheck("Enable stream", nil)
	fftEnabled.SetChecked(true)

	model.mu.Lock()
	initialExpert := model.state.Expert
	model.mu.Unlock()

	dateTimeEntry := widget.NewEntry()
	if initialExpert.HasDateTime && !initialExpert.DateTimeValue.IsZero() {
		dateTimeEntry.SetText(initialExpert.DateTimeValue.UTC().Format("2006-01-02 15:04:05"))
	} else {
		dateTimeEntry.SetText(time.Now().UTC().Format("2006-01-02 15:04:05"))
	}
	dstCheck := widget.NewCheck("DST Enabled", nil)
	if initialExpert.HasDateTime {
		dstCheck.SetChecked(initialExpert.DaylightSaving == 1)
	}
	latEntry := widget.NewEntry()
	if strings.TrimSpace(initialExpert.Latitude) != "" {
		latEntry.SetText(strings.TrimSpace(initialExpert.Latitude))
	}
	lonEntry := widget.NewEntry()
	if strings.TrimSpace(initialExpert.Longitude) != "" {
		lonEntry.SetText(strings.TrimSpace(initialExpert.Longitude))
	}
	rangeEntry := widget.NewEntry()
	if strings.TrimSpace(initialExpert.Range) != "" {
		rangeEntry.SetText(strings.TrimSpace(initialExpert.Range))
	}

	menuEnterButton := widget.NewButton("Menu Enter", nil)
	menuStatusButton := widget.NewButton("Menu Status", nil)
	menuSetButton := widget.NewButton("Menu Set", nil)
	menuBackButton := widget.NewButton("Menu Back", nil)
	analyzeStartButton := widget.NewButton("Analyze Start", nil)
	analyzePauseButton := widget.NewButton("Analyze Pause/Resume", nil)
	pushWaterfallButton := widget.NewButton("Push FFT", nil)
	getWaterfallButton := widget.NewButton("Get FFT", nil)
	setDateTimeButton := widget.NewButton("Set Date/Time", nil)
	getDateTimeButton := widget.NewButton("Read Date/Time", nil)
	syncDateTimeButton := widget.NewButton("Sync Date/Time", nil)
	setLocationButton := widget.NewButton("Set Location", nil)
	getLocationButton := widget.NewButton("Read Location", nil)
	deviceInfoButton := widget.NewButton("Read Model/Firmware", nil)
	chargeButton := widget.NewButton("Read Charge", nil)
	keepAliveButton := widget.NewButton("Send KeepAlive", nil)
	powerOffButton := widget.NewButton("Power Off", nil)

	menuEnterButton.OnTapped = func() {
		runScanControl(ControlRequest{
			Intent:    IntentMenuEnter,
			MenuID:    strings.TrimSpace(menuIDEntry.Text),
			MenuIndex: strings.TrimSpace(menuIndexEntry.Text),
		})
	}
	menuStatusButton.OnTapped = func() {
		runScanControl(ControlRequest{Intent: IntentMenuStatus})
	}
	menuSetButton.OnTapped = func() {
		runScanControl(ControlRequest{
			Intent:    IntentMenuSetValue,
			MenuValue: strings.TrimSpace(menuValueEntry.Text),
		})
	}
	menuBackButton.OnTapped = func() {
		runScanControl(ControlRequest{
			Intent:        IntentMenuBack,
			MenuBackLevel: strings.TrimSpace(menuBackEntry.Text),
		})
	}
	analyzeStartButton.OnTapped = func() {
		params := parseCSVValues(analyzeParamsEntry.Text)
		runScanControl(ControlRequest{
			Intent:        IntentAnalyzeStart,
			AnalyzeMode:   strings.TrimSpace(analyzeModeEntry.Text),
			AnalyzeParams: params,
		})
	}
	analyzePauseButton.OnTapped = func() {
		runScanControl(ControlRequest{
			Intent:      IntentAnalyzePause,
			AnalyzeMode: strings.TrimSpace(analyzeModeEntry.Text),
		})
	}
	pushWaterfallButton.OnTapped = func() {
		fftType, err := parseIntEntry("FFT type", fftTypeEntry)
		if err != nil {
			dialog.ShowError(err, window)
			return
		}
		runScanControl(ControlRequest{
			Intent:     IntentPushWaterfall,
			FFTType:    fftType,
			FFTEnabled: fftEnabled.Checked,
		})
	}
	getWaterfallButton.OnTapped = func() {
		fftType, err := parseIntEntry("FFT type", fftTypeEntry)
		if err != nil {
			dialog.ShowError(err, window)
			return
		}
		runScanControl(ControlRequest{
			Intent:     IntentGetWaterfall,
			FFTType:    fftType,
			FFTEnabled: fftEnabled.Checked,
		})
	}
	setDateTimeButton.OnTapped = func() {
		when, err := parseDateTimeInput(dateTimeEntry.Text)
		if err != nil {
			dialog.ShowError(err, window)
			return
		}
		daylightSaving := 0
		if dstCheck.Checked {
			daylightSaving = 1
		}
		runScanControl(ControlRequest{
			Intent:         IntentSetDateTime,
			DaylightSaving: daylightSaving,
			DateTime:       when,
		})
	}
	getDateTimeButton.OnTapped = func() {
		runScanControl(ControlRequest{Intent: IntentGetDateTime})
	}
	syncDateTimeButton.OnTapped = func() {
		now := time.Now()
		dateTimeEntry.SetText(now.Format("2006-01-02 15:04:05"))
		daylightSaving := 0
		if dstCheck.Checked {
			daylightSaving = 1
		}
		runScanControl(ControlRequest{
			Intent:         IntentSetDateTime,
			DaylightSaving: daylightSaving,
			DateTime:       now,
		})
	}
	setLocationButton.OnTapped = func() {
		runScanControl(ControlRequest{
			Intent:    IntentSetLocationRange,
			Latitude:  strings.TrimSpace(latEntry.Text),
			Longitude: strings.TrimSpace(lonEntry.Text),
			Range:     strings.TrimSpace(rangeEntry.Text),
		})
	}
	getLocationButton.OnTapped = func() {
		runScanControl(ControlRequest{Intent: IntentGetLocationRange})
	}
	deviceInfoButton.OnTapped = func() {
		runScanControl(ControlRequest{Intent: IntentGetDeviceInfo})
	}
	chargeButton.OnTapped = func() {
		runScanControl(ControlRequest{Intent: IntentGetChargeStatus})
	}
	keepAliveButton.OnTapped = func() {
		runScanControl(ControlRequest{Intent: IntentKeepAlive})
	}
	powerOffButton.OnTapped = func() {
		dialog.ShowConfirm("Power Off", "Power off the unit now?", func(ok bool) {
			if !ok {
				return
			}
			runScanControl(ControlRequest{
				Intent:    IntentPowerOff,
				Confirmed: true,
			})
		}, window)
	}

	views.expertMenuStatus = menuStatusLabel
	views.expertAnalyze = analyzeLabel
	views.expertWaterfall = waterfallLabel
	views.expertDateTime = dateTimeLabel
	views.expertLocation = locationLabel
	views.expertModel = modelLabel
	views.expertFirmware = firmwareLabel
	views.expertCharge = chargeLabel
	views.expertKeepAlive = keepAliveLabel
	views.expertDateTimeEntry = dateTimeEntry
	views.expertDSTCheck = dstCheck
	views.expertLatEntry = latEntry
	views.expertLonEntry = lonEntry
	views.expertRangeEntry = rangeEntry
	views.menuEnterButton = menuEnterButton
	views.menuStatusButton = menuStatusButton
	views.menuSetButton = menuSetButton
	views.menuBackButton = menuBackButton
	views.analyzeStartButton = analyzeStartButton
	views.analyzePauseButton = analyzePauseButton
	views.pushWaterfallButton = pushWaterfallButton
	views.getWaterfallButton = getWaterfallButton
	views.setDateTimeButton = setDateTimeButton
	views.getDateTimeButton = getDateTimeButton
	views.syncDateTimeButton = syncDateTimeButton
	views.setLocationButton = setLocationButton
	views.getLocationButton = getLocationButton
	views.deviceInfoButton = deviceInfoButton
	views.chargeButton = chargeButton
	views.keepAliveButton = keepAliveButton
	views.powerOffButton = powerOffButton

	return container.NewVBox(
		widget.NewCard("Menu Operations", "", container.NewVBox(
			container.NewHBox(
				widget.NewLabel("Menu ID"),
				container.NewGridWrap(fyne.NewSize(160, menuIDEntry.MinSize().Height), menuIDEntry),
				widget.NewLabel("Index"),
				container.NewGridWrap(fyne.NewSize(160, menuIndexEntry.MinSize().Height), menuIndexEntry),
				menuEnterButton,
				menuStatusButton,
			),
			container.NewHBox(
				widget.NewLabel("Value"),
				container.NewGridWrap(fyne.NewSize(280, menuValueEntry.MinSize().Height), menuValueEntry),
				menuSetButton,
			),
			container.NewHBox(
				widget.NewLabel("Back Mode"),
				container.NewGridWrap(fyne.NewSize(220, menuBackEntry.MinSize().Height), menuBackEntry),
				menuBackButton,
			),
			widget.NewForm(widget.NewFormItem("Status", menuStatusLabel)),
		)),
		widget.NewCard("Analyze and Waterfall", "", container.NewVBox(
			container.NewHBox(
				widget.NewLabel("Mode"),
				container.NewGridWrap(fyne.NewSize(200, analyzeModeEntry.MinSize().Height), analyzeModeEntry),
				widget.NewLabel("Params"),
				container.NewGridWrap(fyne.NewSize(260, analyzeParamsEntry.MinSize().Height), analyzeParamsEntry),
				analyzeStartButton,
				analyzePauseButton,
			),
			container.NewHBox(
				widget.NewLabel("FFT Type"),
				container.NewGridWrap(fyne.NewSize(80, fftTypeEntry.MinSize().Height), fftTypeEntry),
				fftEnabled,
				pushWaterfallButton,
				getWaterfallButton,
			),
			widget.NewForm(
				widget.NewFormItem("Analyze", analyzeLabel),
				widget.NewFormItem("Waterfall", waterfallLabel),
			),
		)),
		widget.NewCard("Time and Location", "", container.NewVBox(
			container.NewHBox(
				widget.NewLabel("Date/Time"),
				container.NewGridWrap(fyne.NewSize(220, dateTimeEntry.MinSize().Height), dateTimeEntry),
				dstCheck,
				getDateTimeButton,
				setDateTimeButton,
				syncDateTimeButton,
			),
			container.NewHBox(
				widget.NewLabel("Lat"),
				container.NewGridWrap(fyne.NewSize(130, latEntry.MinSize().Height), latEntry),
				widget.NewLabel("Lon"),
				container.NewGridWrap(fyne.NewSize(130, lonEntry.MinSize().Height), lonEntry),
				widget.NewLabel("Range"),
				container.NewGridWrap(fyne.NewSize(100, rangeEntry.MinSize().Height), rangeEntry),
				setLocationButton,
				getLocationButton,
			),
			widget.NewForm(
				widget.NewFormItem("Date/Time", dateTimeLabel),
				widget.NewFormItem("Location", locationLabel),
			),
		)),
		widget.NewCard("Device Health", "", container.NewVBox(
			container.NewHBox(deviceInfoButton, chargeButton, keepAliveButton),
			widget.NewForm(
				widget.NewFormItem("Model", modelLabel),
				widget.NewFormItem("Firmware", firmwareLabel),
				widget.NewFormItem("Charge", chargeLabel),
				widget.NewFormItem("KeepAlive", keepAliveLabel),
			),
		)),
		widget.NewCard("Danger Zone", "", container.NewVBox(
			powerOffButton,
		)),
	)
}

func parseCSVValues(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		trim := strings.TrimSpace(part)
		if trim == "" {
			continue
		}
		out = append(out, trim)
	}
	return out
}

func parseDateTimeInput(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, errors.New("date/time is required")
	}
	layouts := []string{
		"2006-01-02 15:04:05",
		time.RFC3339,
		"2006-01-02T15:04:05",
	}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, errors.New("date/time must be RFC3339 or YYYY-MM-DD HH:MM:SS")
}

func buildActivityLists(model *uiModel) (*widget.List, *widget.List) {
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
			label := obj.(*widget.Label)
			model.mu.Lock()
			defer model.mu.Unlock()
			if id < 0 || id >= len(model.activities) {
				label.SetText("")
				return
			}
			label.SetText(model.activities[id])
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
			label := obj.(*widget.Label)
			model.mu.Lock()
			defer model.mu.Unlock()
			if id < 0 || id >= len(model.suppressed) {
				label.SetText("")
				return
			}
			label.SetText(model.suppressed[id])
		},
	)
	return activityList, suppressedList
}

func buildRecordingsPanel(model *uiModel, deps Dependencies, window fyne.Window, views *uiViews) (fyne.CanvasObject, func()) {
	var recordingsList *widget.List
	var stopButton *widget.Button
	var playButton *widget.Button
	var deleteButton *widget.Button
	selectRecording := func(id widget.ListItemID) {
		model.mu.Lock()
		if model.recordingsListSyncing {
			model.mu.Unlock()
			return
		}
		ensureRecordingSelectionLocked(model)
		modifier := fyne.KeyModifier(model.lastSelectionModifier)
		model.lastSelectionModifier = 0

		if id < 0 || id >= len(model.recordings) {
			clearRecordingSelectionLocked(model)
			model.mu.Unlock()
			playButton.Disable()
			deleteButton.Disable()
			recordingsList.Refresh()
			return
		}

		clicked := model.recordings[id]
		shift := modifier&fyne.KeyModifierShift != 0
		toggle := modifier&fyne.KeyModifierControl != 0 || modifier&fyne.KeyModifierSuper != 0

		switch {
		case shift:
			anchor := model.selectionAnchor
			if anchor < 0 || anchor >= len(model.recordings) {
				if model.selectedClip >= 0 && model.selectedClip < len(model.recordings) {
					anchor = model.selectedClip
				} else {
					anchor = id
				}
			}
			if !toggle {
				for current := range model.selectedIDs {
					delete(model.selectedIDs, current)
				}
			}
			start := anchor
			end := id
			if start > end {
				start, end = end, start
			}
			for i := start; i <= end; i++ {
				model.selectedIDs[model.recordings[i].ID] = struct{}{}
			}
			model.selectedClip = id
			model.selectedID = clicked.ID
			model.selectionAnchor = anchor
		case toggle:
			if _, ok := model.selectedIDs[clicked.ID]; ok {
				delete(model.selectedIDs, clicked.ID)
				if model.selectedID == clicked.ID || model.selectedClip == id {
					model.selectedClip = -1
					model.selectedID = ""
				}
			} else {
				model.selectedIDs[clicked.ID] = struct{}{}
				model.selectedClip = id
				model.selectedID = clicked.ID
			}
			model.selectionAnchor = id
		default:
			for current := range model.selectedIDs {
				delete(model.selectedIDs, current)
			}
			model.selectedIDs[clicked.ID] = struct{}{}
			model.selectedClip = id
			model.selectedID = clicked.ID
			model.selectionAnchor = id
		}

		syncPrimarySelectionLocked(model)
		primary := model.selectedClip
		selectedCount := len(model.selectedIDs)
		model.recordingsListSyncing = true
		model.mu.Unlock()

		recordingsList.Refresh()
		if selectedCount == 0 {
			recordingsList.UnselectAll()
		} else if primary >= 0 && primary != id {
			recordingsList.Select(primary)
		}

		model.mu.Lock()
		model.recordingsListSyncing = false
		model.mu.Unlock()

		setEnabled(playButton, primary >= 0)
		setEnabled(deleteButton, selectedCount > 0)
	}
	recordingsList = widget.NewList(
		func() int {
			model.mu.Lock()
			defer model.mu.Unlock()
			return len(model.recordings)
		},
		func() fyne.CanvasObject {
			return newRecordingsListItem(
				func(modifier fyne.KeyModifier) {
					model.mu.Lock()
					model.lastSelectionModifier = int(modifier)
					model.mu.Unlock()
				},
				func(id widget.ListItemID) {
					if recordingsList == nil {
						return
					}
					recordingsList.Select(id)
				},
			)
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			row := obj.(*recordingsListItem)
			row.SetID(id)
			model.mu.Lock()
			defer model.mu.Unlock()
			if id < 0 || id >= len(model.recordings) {
				row.SetText("")
				row.SetSelected(false)
				return
			}
			rec := model.recordings[id]
			row.SetText(formatRecording(rec))
			row.SetSelected(isRecordingSelectedLocked(model, id, rec.ID))
		},
	)
	recordingsList.OnSelected = selectRecording
	recordingsErrLabel := widget.NewLabel("")
	recordingsErrLabel.Hide()
	if model.recordingsErr != "" {
		recordingsErrLabel.SetText("Recordings load error: " + model.recordingsErr)
		recordingsErrLabel.Show()
	}

	var playerMu sync.Mutex
	var player *exec.Cmd
	stopCurrentPlayback := func() {
		playerMu.Lock()
		defer playerMu.Unlock()
		if player != nil && player.Process != nil {
			_ = player.Process.Kill()
		}
		player = nil
	}

	playButton = widget.NewButton("Play", func() {
		model.mu.Lock()
		idx := model.selectedClip
		if idx < 0 || idx >= len(model.recordings) {
			idx = -1
		}
		var rec Recording
		if idx >= 0 {
			rec = model.recordings[idx]
		}
		model.mu.Unlock()
		if idx < 0 {
			dialog.ShowInformation("Playback", "Select a recording first.", window)
			return
		}
		if strings.TrimSpace(rec.FilePath) == "" {
			dialog.ShowInformation("Playback", "Selected recording does not have a file path.", window)
			return
		}

		stopCurrentPlayback()
		cmd, controllable, err := startPlayback(rec.FilePath)
		if err != nil {
			dialog.ShowError(err, window)
			return
		}
		playerMu.Lock()
		player = cmd
		playerMu.Unlock()
		if controllable {
			stopButton.Enable()
			return
		}
		stopButton.Disable()
		dialog.ShowInformation("Playback", "Opened in external player. Stop is unavailable without ffplay.", window)
	})
	playButton.Disable()

	stopButton = widget.NewButton("Stop", func() {
		stopCurrentPlayback()
		stopButton.Disable()
	})
	stopButton.Disable()

	deleteButton = widget.NewButton("Delete Selected", func() {
		model.mu.Lock()
		targetIDs := orderedSelectedRecordingIDsLocked(model)
		if len(targetIDs) == 0 && model.selectedClip >= 0 && model.selectedClip < len(model.recordings) {
			targetIDs = []string{model.recordings[model.selectedClip].ID}
		}
		var primaryRec Recording
		if len(targetIDs) == 1 {
			targetID := targetIDs[0]
			for i := range model.recordings {
				if model.recordings[i].ID == targetID {
					primaryRec = model.recordings[i]
					break
				}
			}
		}
		model.mu.Unlock()
		if len(targetIDs) == 0 {
			dialog.ShowInformation("Delete", "Select one or more recordings first.", window)
			return
		}

		title := "Delete Recording"
		var message string
		if len(targetIDs) == 1 {
			name := filepath.Base(primaryRec.FilePath)
			if strings.TrimSpace(name) == "." || strings.TrimSpace(name) == "" {
				name = primaryRec.ID
			}
			if strings.TrimSpace(name) == "" {
				name = targetIDs[0]
			}
			message = fmt.Sprintf("Delete recording %q?", name)
		} else {
			title = "Delete Recordings"
			message = fmt.Sprintf("Delete %d selected recordings?", len(targetIDs))
		}
		dialog.ShowConfirm(title, message, func(ok bool) {
			if !ok {
				return
			}
			deleteButton.Disable()
			model.mu.Lock()
			model.pendingRecordingAction = true
			model.mu.Unlock()
			go func(ids []string) {
				report, err := deps.DeleteRecordings(ids)
				recs, loadErr := deps.LoadRecordings()
				fyne.Do(func() {
					model.mu.Lock()
					model.pendingRecordingAction = false
					model.mu.Unlock()
					applyRecordingsLoadResult(model, recordingsList, recordingsErrLabel, playButton, deleteButton, recs, loadErr, true)
					if err != nil {
						dialog.ShowError(err, window)
						return
					}
					if len(report.Failed) > 0 {
						summary := fmt.Sprintf("Deleted %d of %d recordings. %d failed.", len(report.Deleted), len(ids), len(report.Failed))
						if strings.TrimSpace(report.Failed[0].Message) != "" {
							summary += " " + report.Failed[0].Message
						}
						dialog.ShowInformation("Delete", summary, window)
					}
				})
			}(append([]string(nil), targetIDs...))
		}, window)
	})
	deleteButton.Disable()

	views.playButton = playButton
	views.stopButton = stopButton
	views.deleteButton = deleteButton
	views.recordingsList = recordingsList
	views.recordingsErr = recordingsErrLabel

	panel := container.NewBorder(
		container.NewVBox(
			recordingsErrLabel,
			container.NewHBox(playButton, stopButton, deleteButton),
		),
		nil,
		nil,
		nil,
		recordingsList,
	)
	return panel, stopCurrentPlayback
}

func buildSettingsPanel(model *uiModel, deps Dependencies, window fyne.Window, onExpertModeChanged func(bool)) fyne.CanvasObject {
	model.mu.Lock()
	currentActivity := model.activity
	model.mu.Unlock()

	ipEntry := widget.NewEntry()
	ipEntry.SetText(deps.InitialSettings.ScannerIP)
	pathEntry := widget.NewEntry()
	pathEntry.SetText(deps.InitialSettings.RecordingsPath)
	hangEntry := widget.NewEntry()
	hangEntry.SetText(strconv.Itoa(deps.InitialSettings.HangTimeSeconds))
	minAutoDurationEntry := widget.NewEntry()
	minAutoDurationEntry.SetText(strconv.Itoa(deps.InitialSettings.MinAutoDurationSeconds))
	activityStartEntry := widget.NewEntry()
	activityStartEntry.SetText(strconv.Itoa(deps.InitialSettings.Activity.StartDebounceMS))
	activityEndEntry := widget.NewEntry()
	activityEndEntry.SetText(strconv.Itoa(deps.InitialSettings.Activity.EndDebounceMS))
	monitorDefaultCheck := widget.NewCheck("Enable listen on startup", nil)
	monitorDefaultCheck.SetChecked(deps.InitialSettings.AudioMonitorDefaultEnabled)
	expertModeCheck := widget.NewCheck("Enable expert operations panel", nil)
	expertModeCheck.SetChecked(deps.InitialSettings.ExpertModeEnabled)
	monitorGainEntry := widget.NewEntry()
	monitorGainEntry.SetText(fmt.Sprintf("%.1f", deps.InitialSettings.AudioMonitorGainDB))
	monitorOutputOptions := []string{"system-default"}
	if deps.ListMonitorOutputDevices != nil {
		if options, err := deps.ListMonitorOutputDevices(); err == nil && len(options) > 0 {
			monitorOutputOptions = dedupeMonitorOptions(options)
		}
	}
	monitorOutputSelect := widget.NewSelect(monitorOutputOptions, nil)
	currentOutputDevice := strings.TrimSpace(deps.InitialSettings.AudioMonitorOutputDevice)
	if currentOutputDevice == "" {
		currentOutputDevice = "system-default"
	}
	if !containsString(monitorOutputOptions, currentOutputDevice) {
		monitorOutputOptions = append(monitorOutputOptions, currentOutputDevice)
		monitorOutputSelect.Options = dedupeMonitorOptions(monitorOutputOptions)
	}
	monitorOutputSelect.SetSelected(currentOutputDevice)
	currentScannerIP := strings.TrimSpace(deps.InitialSettings.ScannerIP)

	saveSettings := widget.NewButton("Save Settings", nil)
	saveSettings.OnTapped = func() {
		hangSeconds, err := parseIntEntry("Hang-time", hangEntry)
		if err != nil {
			dialog.ShowError(err, window)
			return
		}
		minAutoDurationSeconds, err := parseIntEntry("Auto clip minimum duration", minAutoDurationEntry)
		if err != nil {
			dialog.ShowError(err, window)
			return
		}
		activityStartMS, err := parseIntEntry("Activity start debounce", activityStartEntry)
		if err != nil {
			dialog.ShowError(err, window)
			return
		}
		activityEndMS, err := parseIntEntry("Activity end debounce", activityEndEntry)
		if err != nil {
			dialog.ShowError(err, window)
			return
		}
		gainText := strings.TrimSpace(monitorGainEntry.Text)
		if gainText == "" {
			gainText = "0"
		}
		gainValue, parseErr := strconv.ParseFloat(gainText, 64)
		if parseErr != nil {
			dialog.ShowError(errors.New("audio monitor gain must be a number"), window)
			return
		}
		settings := Settings{
			ScannerIP:              strings.TrimSpace(ipEntry.Text),
			RecordingsPath:         strings.TrimSpace(pathEntry.Text),
			HangTimeSeconds:        hangSeconds,
			MinAutoDurationSeconds: minAutoDurationSeconds,
			Activity: ActivitySettings{
				StartDebounceMS: activityStartMS,
				EndDebounceMS:   activityEndMS,
				MinActivityMS:   currentActivity.MinActivityMS,
			},
			AudioMonitorDefaultEnabled: monitorDefaultCheck.Checked,
			AudioMonitorOutputDevice:   strings.TrimSpace(monitorOutputSelect.Selected),
			AudioMonitorGainDB:         gainValue,
			ExpertModeEnabled:          expertModeCheck.Checked,
		}
		restartRequired := settings.ScannerIP != currentScannerIP
		saveSettings.Disable()
		go func() {
			err := deps.SaveSettings(settings)
			fyne.Do(func() {
				saveSettings.Enable()
				if err != nil {
					dialog.ShowError(err, window)
					return
				}
				model.mu.Lock()
				model.activity = normalizeActivitySettings(settings.Activity)
				model.mu.Unlock()
				currentScannerIP = settings.ScannerIP
				if onExpertModeChanged != nil {
					onExpertModeChanged(settings.ExpertModeEnabled)
				}
				if restartRequired {
					dialog.ShowInformation("Settings", "Settings saved. Restart the app to apply scanner connection changes.", window)
				}
			})
		}()
	}

	settingsForm := widget.NewForm(
		widget.NewFormItem("Scanner IP", ipEntry),
		widget.NewFormItem("Recordings Path", pathEntry),
		widget.NewFormItem("Recording Wait / Hang-Time (s)", hangEntry),
		widget.NewFormItem("Auto Clip Minimum Duration (s)", minAutoDurationEntry),
		widget.NewFormItem("Activity Start Debounce (ms)", activityStartEntry),
		widget.NewFormItem("Activity End Debounce (ms)", activityEndEntry),
		widget.NewFormItem("Audio Monitor", monitorDefaultCheck),
		widget.NewFormItem("Monitor Gain (dB)", monitorGainEntry),
		widget.NewFormItem("Monitor Output Device", monitorOutputSelect),
		widget.NewFormItem("Expert Mode", expertModeCheck),
	)
	return container.NewVBox(settingsForm, saveSettings)
}

func dedupeMonitorOptions(options []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(options))
	for _, option := range options {
		option = strings.TrimSpace(option)
		if option == "" {
			continue
		}
		if _, ok := seen[option]; ok {
			continue
		}
		seen[option] = struct{}{}
		out = append(out, option)
	}
	if len(out) == 0 {
		return []string{"system-default"}
	}
	return out
}

func containsString(items []string, value string) bool {
	for _, item := range items {
		if item == value {
			return true
		}
	}
	return false
}

func parseIntEntry(name string, entry *widget.Entry) (int, error) {
	value := strings.TrimSpace(entry.Text)
	if value == "" {
		return 0, fmt.Errorf("%s is required", name)
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer", name)
	}
	return n, nil
}
