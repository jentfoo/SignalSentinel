package gui

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	maxActivityRows        = 200
	maxSuppressedActivity  = 200
	defaultStartDebounceMS = 150
	defaultEndDebounceMS   = 600
	defaultMinActivityMS   = 300
)

type uiModel struct {
	mu                     sync.Mutex
	state                  RuntimeState
	recordings             []Recording
	recordingsErr          string
	activities             []string
	suppressed             []string
	selectedClip           int
	selectedID             string
	pendingRecordingAction bool
	pendingRecordingStop   bool
	pendingControlAction   bool
	lastConnected          bool
	everConnected          bool
	fatalReceived          bool
	initialized            bool
	activity               ActivitySettings

	pendingStart        bool
	pendingStartAt      time.Time
	pendingStartStatus  ScannerStatus
	pendingStartSamples int

	sessionActive     bool
	sessionRowIndex   int
	sessionStartAt    time.Time
	sessionStatus     ScannerStatus
	sessionPendingEnd bool
	sessionPendingAt  time.Time
}

func appendActivity(model *uiModel, state RuntimeState) {
	if model == nil {
		return
	}

	scanner := state.Scanner
	if scanner.Connected {
		model.everConnected = true
	}

	if !model.initialized {
		model.lastConnected = scanner.Connected
		model.sessionRowIndex = -1
		model.activity = normalizeActivitySettings(model.activity)
		model.initialized = true
	}

	now := scanner.UpdatedAt
	if now.IsZero() {
		now = time.Now()
	}

	if !scanner.Connected {
		if model.pendingStart {
			if model.canPromotePending(now) {
				model.promotePendingToSession(now)
			} else {
				reason, compareDebounce := model.pendingSuppressionReason(now, true)
				model.suppressPendingStart(now, reason, compareDebounce)
			}
		}
		if model.sessionActive {
			endAt := now
			if model.sessionPendingEnd && !model.sessionPendingAt.IsZero() {
				endAt = model.sessionPendingAt
			}
			model.finalizeSession(endAt)
		}
		if model.lastConnected {
			model.prependActivity(formatActivityTimestamp(now) + " scanner disconnected")
		}
		model.lastConnected = false
		return
	}

	if scanner.Active {
		model.handleActiveSample(scanner, now)
	} else {
		model.handleInactiveSample(now)
	}
	model.lastConnected = true
}

func (m *uiModel) handleActiveSample(scanner ScannerStatus, at time.Time) {
	if m.sessionActive {
		m.sessionStatus = mergeActivityStatus(m.sessionStatus, scanner)
		m.sessionPendingEnd = false
		m.sessionPendingAt = time.Time{}
		m.updateSessionRow(at, true)
		return
	}

	if !m.pendingStart {
		m.pendingStart = true
		m.pendingStartAt = at
		m.pendingStartStatus = scanner
		m.pendingStartSamples = 1
	} else {
		m.pendingStartStatus = mergeActivityStatus(m.pendingStartStatus, scanner)
		m.pendingStartSamples++
	}

	if m.canPromotePending(at) {
		m.promotePendingToSession(at)
	}
}

func (m *uiModel) handleInactiveSample(at time.Time) {
	if m.pendingStart {
		if m.canPromotePending(at) {
			m.promotePendingToSession(at)
		} else {
			reason, compareDebounce := m.pendingSuppressionReason(at, false)
			m.suppressPendingStart(at, reason, compareDebounce)
		}
	}
	if !m.sessionActive {
		return
	}

	if !m.sessionPendingEnd {
		m.sessionPendingEnd = true
		m.sessionPendingAt = at
		m.updateSessionRow(at, true)
		return
	}
	if at.Before(m.sessionPendingAt.Add(m.endDebounce())) {
		return
	}
	m.finalizeSession(m.sessionPendingAt)
}

func (m *uiModel) finalizeSession(endAt time.Time) {
	if !m.sessionActive {
		return
	}
	if endAt.IsZero() || endAt.Before(m.sessionStartAt) {
		endAt = m.sessionStartAt
	}

	duration := endAt.Sub(m.sessionStartAt)
	if duration < m.minActivity() {
		m.removeSessionRow()
		reason := fmt.Sprintf(
			"duration below minimum (%s < %s)",
			formatActivityDuration(duration),
			formatActivityDuration(m.minActivity()),
		)
		m.prependSuppressed(formatSuppressedRow(endAt, reason, m.sessionStatus))
	} else {
		m.updateSessionRow(endAt, false)
	}

	m.sessionActive = false
	m.sessionRowIndex = -1
	m.sessionStartAt = time.Time{}
	m.sessionStatus = ScannerStatus{}
	m.sessionPendingEnd = false
	m.sessionPendingAt = time.Time{}
}

func (m *uiModel) suppressPendingStart(at time.Time, reason string, compareDebounce bool) {
	duration := at.Sub(m.pendingStartAt)
	if duration < 0 {
		duration = 0
	}
	fullReason := fmt.Sprintf("%s (observed %s)", reason, formatActivityDuration(duration))
	if compareDebounce {
		fullReason = fmt.Sprintf(
			"%s (%s < %s)",
			reason,
			formatActivityDuration(duration),
			formatActivityDuration(m.startDebounce()),
		)
	}
	m.prependSuppressed(formatSuppressedRow(at, fullReason, m.pendingStartStatus))
	m.clearPendingStart()
}

func (m *uiModel) pendingSuppressionReason(at time.Time, disconnect bool) (string, bool) {
	if at.Before(m.pendingStartAt.Add(m.startDebounce())) {
		if disconnect {
			return "start debounce not met before disconnect", true
		}
		return "start debounce not met", true
	}
	if m.pendingStartSamples < 2 {
		if disconnect {
			return fmt.Sprintf("insufficient active samples before disconnect (%d)", m.pendingStartSamples), false
		}
		return fmt.Sprintf("insufficient active samples (%d)", m.pendingStartSamples), false
	}
	if disconnect {
		return "start debounce not met before disconnect", true
	}
	return "start debounce not met", true
}

func (m *uiModel) clearPendingStart() {
	m.pendingStart = false
	m.pendingStartAt = time.Time{}
	m.pendingStartStatus = ScannerStatus{}
	m.pendingStartSamples = 0
}

func (m *uiModel) canPromotePending(at time.Time) bool {
	if !m.pendingStart {
		return false
	}
	if m.pendingStartSamples < 2 {
		return false
	}
	return !at.Before(m.pendingStartAt.Add(m.startDebounce()))
}

func (m *uiModel) promotePendingToSession(markAt time.Time) {
	m.sessionActive = true
	m.sessionStartAt = m.pendingStartAt
	if m.sessionStartAt.IsZero() {
		m.sessionStartAt = markAt
	}
	m.sessionStatus = m.pendingStartStatus
	m.sessionPendingEnd = false
	m.sessionPendingAt = time.Time{}
	m.sessionRowIndex = 0
	m.clearPendingStart()
	m.prependActivity(formatSessionRow(m.sessionStartAt, markAt, m.sessionStatus, true))
	m.sessionRowIndex = 0
}

func (m *uiModel) updateSessionRow(at time.Time, active bool) {
	row := formatSessionRow(m.sessionStartAt, at, m.sessionStatus, active)
	if m.sessionRowIndex >= 0 && m.sessionRowIndex < len(m.activities) {
		m.activities[m.sessionRowIndex] = row
		return
	}
	m.prependActivity(row)
	m.sessionRowIndex = 0
}

func (m *uiModel) removeSessionRow() {
	if m.sessionRowIndex < 0 || m.sessionRowIndex >= len(m.activities) {
		return
	}
	m.activities = append(m.activities[:m.sessionRowIndex], m.activities[m.sessionRowIndex+1:]...)
	m.sessionRowIndex = -1
}

func (m *uiModel) prependActivity(row string) {
	m.activities = prependWithCap(m.activities, row, maxActivityRows)
}

func (m *uiModel) prependSuppressed(row string) {
	m.suppressed = prependWithCap(m.suppressed, row, maxSuppressedActivity)
}

func (m *uiModel) startDebounce() time.Duration {
	return time.Duration(m.activity.StartDebounceMS) * time.Millisecond
}

func (m *uiModel) endDebounce() time.Duration {
	return time.Duration(m.activity.EndDebounceMS) * time.Millisecond
}

func (m *uiModel) minActivity() time.Duration {
	return time.Duration(m.activity.MinActivityMS) * time.Millisecond
}

func prependWithCap(rows []string, row string, max int) []string {
	rows = append([]string{row}, rows...)
	if len(rows) > max {
		rows = rows[:max]
	}
	return rows
}

func formatSessionRow(startAt, markAt time.Time, status ScannerStatus, active bool) string {
	if markAt.IsZero() || markAt.Before(startAt) {
		markAt = startAt
	}
	if active {
		return fmt.Sprintf(
			"%s | active %s | %s | %s",
			formatActivityTimestamp(startAt),
			formatActivityDuration(markAt.Sub(startAt)),
			formatFrequency(status.Frequency),
			formatSystemChannel(status.System, status.Channel),
		)
	}
	return fmt.Sprintf(
		"%s -> %s | %s | %s | %s",
		formatActivityTimestamp(startAt),
		formatActivityTimestamp(markAt),
		formatActivityDuration(markAt.Sub(startAt)),
		formatFrequency(status.Frequency),
		formatSystemChannel(status.System, status.Channel),
	)
}

func formatSuppressedRow(at time.Time, reason string, status ScannerStatus) string {
	return fmt.Sprintf(
		"%s | suppressed (%s) | %s | %s",
		formatActivityTimestamp(at),
		reason,
		formatFrequency(status.Frequency),
		formatSystemChannel(status.System, status.Channel),
	)
}

func formatActivityTimestamp(at time.Time) string {
	if at.IsZero() {
		return "-"
	}
	return at.UTC().Format("2006-01-02 15:04:05")
}

func formatActivityDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	return d.Round(10 * time.Millisecond).String()
}

func mergeActivityStatus(base, update ScannerStatus) ScannerStatus {
	if frequency := strings.TrimSpace(update.Frequency); frequency != "" {
		base.Frequency = frequency
	}
	if system := strings.TrimSpace(update.System); system != "" {
		base.System = system
	}
	if channel := strings.TrimSpace(update.Channel); channel != "" {
		base.Channel = channel
	}
	if talkgroup := strings.TrimSpace(update.Talkgroup); talkgroup != "" {
		base.Talkgroup = talkgroup
	}
	return base
}

func normalizeActivitySettings(settings ActivitySettings) ActivitySettings {
	if settings.StartDebounceMS == 0 {
		settings.StartDebounceMS = defaultStartDebounceMS
	}
	if settings.EndDebounceMS == 0 {
		settings.EndDebounceMS = defaultEndDebounceMS
	}
	if settings.MinActivityMS == 0 {
		settings.MinActivityMS = defaultMinActivityMS
	}
	if settings.StartDebounceMS < 0 {
		settings.StartDebounceMS = 0
	}
	if settings.EndDebounceMS < 0 {
		settings.EndDebounceMS = 0
	}
	if settings.MinActivityMS < 0 {
		settings.MinActivityMS = 0
	}
	return settings
}

func recordingsEqual(a, b []Recording) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func formatRecording(rec Recording) string {
	name := "(no file)"
	if strings.TrimSpace(rec.FilePath) != "" {
		name = filepath.Base(rec.FilePath)
	}
	parts := []string{
		orDash(rec.StartedAt),
		orDash(rec.Duration),
		formatFrequency(rec.Frequency),
		formatSystemChannel(rec.System, rec.Channel),
	}
	return strings.Join(parts, " | ") + " | " + name
}

func boolWord(v bool, yes, no string) string {
	if v {
		return yes
	}
	return no
}

func orDash(v string) string {
	if strings.TrimSpace(v) == "" {
		return "-"
	}
	return v
}

func formatFrequency(freq string) string {
	freq = strings.TrimSpace(freq)
	if freq == "" {
		return "-"
	}
	if strings.Contains(strings.ToLower(freq), "mhz") {
		return freq
	}
	return freq + " MHz"
}

func formatSystemChannel(system, channel string) string {
	return orDash(system) + " / " + orDash(channel)
}
