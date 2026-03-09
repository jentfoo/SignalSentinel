//go:build !headless

package gui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

type uiViews struct {
	content             fyne.CanvasObject
	appTabs             *container.AppTabs
	expertTab           *container.TabItem
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
	expertMenuStatus    *widget.Label
	expertAnalyze       *widget.Label
	expertWaterfall     *widget.Label
	expertDateTime      *widget.Label
	expertLocation      *widget.Label
	expertModel         *widget.Label
	expertFirmware      *widget.Label
	expertCharge        *widget.Label
	expertKeepAlive     *widget.Label
	expertDateTimeEntry *widget.Entry
	expertDSTCheck      *widget.Check
	expertLatEntry      *widget.Entry
	expertLonEntry      *widget.Entry
	expertRangeEntry    *widget.Entry
	menuEnterButton     *widget.Button
	menuStatusButton    *widget.Button
	menuSetButton       *widget.Button
	menuBackButton      *widget.Button
	analyzeStartButton  *widget.Button
	analyzePauseButton  *widget.Button
	pushWaterfallButton *widget.Button
	getWaterfallButton  *widget.Button
	setDateTimeButton   *widget.Button
	getDateTimeButton   *widget.Button
	syncDateTimeButton  *widget.Button
	setLocationButton   *widget.Button
	getLocationButton   *widget.Button
	deviceInfoButton    *widget.Button
	chargeButton        *widget.Button
	keepAliveButton     *widget.Button
	powerOffButton      *widget.Button
	playButton          *widget.Button
	stopButton          *widget.Button
	deleteButton        *widget.Button
	activityList        *widget.List
	suppressedList      *widget.List
	recordingsList      *widget.List
	recordingsErr       *widget.Label
}
