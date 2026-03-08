//go:build !headless

package gui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"
)

type uiViews struct {
	content         fyne.CanvasObject
	connLabel       *widget.Label
	reconnectLabel  *widget.Label
	fatalReason     *widget.Label
	modeLabel       *widget.Label
	sourceLabel     *widget.Label
	freqLabel       *widget.Label
	systemLabel     *widget.Label
	deptLabel       *widget.Label
	channelLabel    *widget.Label
	tgidLabel       *widget.Label
	signalLabel     *widget.Label
	squelchLabel    *widget.Label
	squelchLvlLabel *widget.Label
	muteLabel       *widget.Label
	volumeLabel     *widget.Label
	updatedLabel    *widget.Label
	holdButton      *widget.Button
	resumeButton    *widget.Button
	startRecButton  *widget.Button
	stopRecButton   *widget.Button
	playButton      *widget.Button
	stopButton      *widget.Button
	deleteButton    *widget.Button
	activityList    *widget.List
	recordingsList  *widget.List
	recordingsErr   *widget.Label
	recordingsNote  *widget.Label
	spinner         *widget.ProgressBarInfinite
}
