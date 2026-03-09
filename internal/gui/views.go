//go:build !headless

package gui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"
)

type uiViews struct {
	content             fyne.CanvasObject
	connectionLabel     *widget.Label
	modeLabel           *widget.Label
	sourceLabel         *widget.Label
	freqLabel           *widget.Label
	systemLabel         *widget.Label
	deptLabel           *widget.Label
	channelLabel        *widget.Label
	tgidLabel           *widget.Label
	signalLabel         *widget.Label
	squelchLabel        *widget.Label
	squelchLvlLabel     *widget.Label
	muteLabel           *widget.Label
	volumeLabel         *widget.Label
	updatedLabel        *widget.Label
	holdButton          *widget.Button
	nextButton          *widget.Button
	previousButton      *widget.Button
	jumpTagButton       *widget.Button
	qshButton           *widget.Button
	jumpScanButton      *widget.Button
	jumpWXButton        *widget.Button
	avoidButton         *widget.Button
	setVolumeButton     *widget.Button
	setSquelchButton    *widget.Button
	commandAction       *widget.Label
	commandStatus       *widget.Label
	commandMessage      *widget.Label
	commandRawReason    *widget.Label
	commandRetryHint    *widget.Label
	controlStatus       *widget.Label
	lifecycleLabel      *widget.Label
	holdStatusLabel     *widget.Label
	tagFavEntry         *widget.Entry
	tagSysEntry         *widget.Entry
	tagChanEntry        *widget.Entry
	qshFreqEntry        *widget.Entry
	volumeEntry         *widget.Entry
	squelchEntry        *widget.Entry
	startRecButton      *widget.Button
	monitorListenButton *widget.Button
	monitorMuteButton   *widget.Button
	monitorGainEntry    *widget.Entry
	monitorOutputSelect *widget.Select
	monitorApplyButton  *widget.Button
	monitorStatusLabel  *widget.Label
	monitorErrorLabel   *widget.Label
	playButton          *widget.Button
	stopButton          *widget.Button
	deleteButton        *widget.Button
	activityList        *widget.List
	suppressedList      *widget.List
	recordingsList      *widget.List
	recordingsErr       *widget.Label
}
