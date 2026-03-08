//go:build !headless

package gui

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
)

func Run(ctx context.Context, deps Dependencies) error {
	if deps.SubscribeState == nil {
		return errors.New("gui subscribe callback is required")
	}
	if deps.EnqueueControl == nil {
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
		state:        deps.InitialState,
		recordings:   initialRecordings,
		selectedClip: -1,
	}
	if loadErr != nil {
		model.recordingsErr = loadErr.Error()
	}

	ui, stopPlayback := buildUI(model, deps, window)
	defer stopPlayback()
	window.SetContent(ui.content)

	stateCtx, cancelState := context.WithCancel(ctx)
	defer cancelState()

	go watchState(stateCtx, deps, ui, model)
	go pollRecordings(stateCtx, deps, ui, model)

	fatalErr := make(chan error, 1)
	if deps.Fatal != nil {
		go watchFatal(stateCtx, deps.Fatal, window, ui, model, fatalErr)
	}

	go func() {
		<-stateCtx.Done()
		fyne.Do(func() {
			window.Close()
		})
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
	connLabel := widget.NewLabel("Disconnected")
	reconnectLabel := widget.NewLabel("Connecting...")
	reconnectLabel.Hide()
	fatalLabel := widget.NewLabel("")

	modeLabel := widget.NewLabel("-")
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
	spinner := widget.NewProgressBarInfinite()
	spinner.Hide()
	spinner.Stop()

	holdButton := widget.NewButton("Hold Current", func() {
		deps.EnqueueControl(IntentHoldCurrent)
	})
	resumeButton := widget.NewButton("Resume Scan", func() {
		deps.EnqueueControl(IntentResumeScan)
	})
	startRecButton := widget.NewButton("Start Recording", nil)
	stopRecButton := widget.NewButton("Stop Recording", nil)
	holdButton.Disable()
	resumeButton.Disable()
	startRecButton.Disable()
	stopRecButton.Disable()

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
			label := obj.(*widget.Label)
			model.mu.Lock()
			defer model.mu.Unlock()
			if id < 0 || id >= len(model.recordings) {
				label.SetText("")
				return
			}
			rec := model.recordings[id]
			label.SetText(formatRecording(rec))
		},
	)
	var stopButton *widget.Button
	var playButton *widget.Button
	var deleteButton *widget.Button
	recordingsList.OnSelected = func(id widget.ListItemID) {
		model.mu.Lock()
		model.selectedClip = id
		model.selectedID = ""
		if id >= 0 && id < len(model.recordings) {
			model.selectedID = model.recordings[id].ID
		}
		model.mu.Unlock()
		playButton.Enable()
		deleteButton.Enable()
	}
	recordingsList.OnUnselected = func(id widget.ListItemID) {
		_ = id
		model.mu.Lock()
		model.selectedClip = -1
		model.selectedID = ""
		model.mu.Unlock()
		playButton.Disable()
		deleteButton.Disable()
	}
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

	startRecButton.OnTapped = func() {
		startRecButton.Disable()
		go func() {
			err := deps.StartRecording()
			fyne.Do(func() {
				startRecButton.Enable()
				if err != nil {
					dialog.ShowError(err, window)
					return
				}
				model.mu.Lock()
				model.recordingOn = true
				model.mu.Unlock()
				startRecButton.Disable()
				stopRecButton.Enable()
			})
		}()
	}

	stopRecButton.OnTapped = func() {
		stopRecButton.Disable()
		go func() {
			err := deps.StopRecording()
			fyne.Do(func() {
				if err != nil {
					stopRecButton.Enable()
					dialog.ShowError(err, window)
					return
				}
				model.mu.Lock()
				model.recordingOn = false
				model.mu.Unlock()
				startRecButton.Enable()
			})
		}()
	}

	deleteButton = widget.NewButton("Delete Selected", func() {
		model.mu.Lock()
		idx := model.selectedClip
		var rec Recording
		if idx >= 0 && idx < len(model.recordings) {
			rec = model.recordings[idx]
		} else {
			idx = -1
		}
		model.mu.Unlock()
		if idx < 0 {
			dialog.ShowInformation("Delete", "Select a recording first.", window)
			return
		}
		name := filepath.Base(rec.FilePath)
		if strings.TrimSpace(name) == "." || strings.TrimSpace(name) == "" {
			name = rec.ID
		}
		dialog.ShowConfirm("Delete Recording", fmt.Sprintf("Delete recording %q?", name), func(ok bool) {
			if !ok {
				return
			}
			deleteButton.Disable()
			go func(target Recording) {
				report, err := deps.DeleteRecordings([]string{target.ID})
				recs, loadErr := deps.LoadRecordings()
				fyne.Do(func() {
					deleteButton.Enable()
					applyRecordingsLoadResult(model, recordingsList, recordingsErrLabel, playButton, deleteButton, recs, loadErr, true)
					if err != nil {
						dialog.ShowError(err, window)
						return
					}
					if len(report.Failed) > 0 {
						dialog.ShowInformation("Delete", report.Failed[0].Message, window)
						return
					}
					dialog.ShowInformation("Delete", "Recording deleted.", window)
				})
			}(rec)
		}, window)
	})
	deleteButton.Disable()

	ipEntry := widget.NewEntry()
	ipEntry.SetText(deps.InitialSettings.ScannerIP)
	pathEntry := widget.NewEntry()
	pathEntry.SetText(deps.InitialSettings.RecordingsPath)
	hangLabel := widget.NewLabel(fmt.Sprintf("%ds", deps.InitialSettings.HangTimeSeconds))
	currentScannerIP := strings.TrimSpace(deps.InitialSettings.ScannerIP)

	saveSettings := widget.NewButton("Save Settings", nil)
	saveSettings.OnTapped = func() {
		settings := Settings{
			ScannerIP:       strings.TrimSpace(ipEntry.Text),
			RecordingsPath:  strings.TrimSpace(pathEntry.Text),
			HangTimeSeconds: deps.InitialSettings.HangTimeSeconds,
			HangTimeChanged: false, // v1 settings view displays hang-time but does not edit it.
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
				currentScannerIP = settings.ScannerIP
				if restartRequired {
					dialog.ShowInformation("Settings", "Settings saved. Restart the app to apply scanner connection changes.", window)
				}
			})
		}()
	}

	statusForm := widget.NewForm(
		widget.NewFormItem("Mode", modeLabel),
		widget.NewFormItem("View", sourceLabel),
		widget.NewFormItem("Frequency", freqLabel),
		widget.NewFormItem("System", systemLabel),
		widget.NewFormItem("Department", deptLabel),
		widget.NewFormItem("Channel", channelLabel),
		widget.NewFormItem("Talkgroup", tgidLabel),
		widget.NewFormItem("Signal", signalLabel),
		widget.NewFormItem("Squelch", squelchLabel),
		widget.NewFormItem("Squelch Level", squelchLvlLabel),
		widget.NewFormItem("Mute", muteLabel),
		widget.NewFormItem("Volume", volumeLabel),
		widget.NewFormItem("Updated", updatedLabel),
	)

	controls := container.NewVBox(
		widget.NewLabel("Scanner Control"),
		container.NewHBox(holdButton, resumeButton),
		widget.NewSeparator(),
		widget.NewLabel("Recording Control"),
		container.NewHBox(startRecButton, stopRecButton),
	)

	recordingsNote := widget.NewLabel("Local recordings only in this release. Remote scanner-hosted file browsing is not available.")
	recordingsPanel := container.NewBorder(
		container.NewVBox(
			recordingsNote,
			recordingsErrLabel,
			container.NewHBox(playButton, stopButton, deleteButton),
		),
		nil,
		nil,
		nil,
		recordingsList,
	)

	settingsForm := widget.NewForm(
		widget.NewFormItem("Scanner IP", ipEntry),
		widget.NewFormItem("Recordings Path", pathEntry),
		widget.NewFormItem("Hang-Time", hangLabel),
	)

	tabs := container.NewAppTabs(
		container.NewTabItem("Status", container.NewBorder(nil, controls, nil, nil, statusForm)),
		container.NewTabItem("Activity", activityList),
		container.NewTabItem("Recordings", recordingsPanel),
		container.NewTabItem("Settings", container.NewVBox(settingsForm, saveSettings)),
	)

	header := container.NewVBox(
		container.NewHBox(widget.NewLabel("Connection:"), connLabel, reconnectLabel, layout.NewSpacer(), spinner),
		fatalLabel,
	)

	content := container.NewBorder(header, nil, nil, nil, tabs)
	views := uiViews{
		content:         content,
		connLabel:       connLabel,
		reconnectLabel:  reconnectLabel,
		fatalReason:     fatalLabel,
		modeLabel:       modeLabel,
		sourceLabel:     sourceLabel,
		freqLabel:       freqLabel,
		systemLabel:     systemLabel,
		deptLabel:       deptLabel,
		channelLabel:    channelLabel,
		tgidLabel:       tgidLabel,
		signalLabel:     signalLabel,
		squelchLabel:    squelchLabel,
		squelchLvlLabel: squelchLvlLabel,
		muteLabel:       muteLabel,
		volumeLabel:     volumeLabel,
		updatedLabel:    updatedLabel,
		holdButton:      holdButton,
		resumeButton:    resumeButton,
		startRecButton:  startRecButton,
		stopRecButton:   stopRecButton,
		playButton:      playButton,
		stopButton:      stopButton,
		deleteButton:    deleteButton,
		activityList:    activityList,
		recordingsList:  recordingsList,
		recordingsErr:   recordingsErrLabel,
		recordingsNote:  recordingsNote,
		spinner:         spinner,
	}
	return views, stopCurrentPlayback
}
