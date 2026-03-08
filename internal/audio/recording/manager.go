package recording

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jentfoo/SignalSentinel/internal/activity"
	"github.com/jentfoo/SignalSentinel/internal/sds200"
)

const (
	recordTriggerTelemetry = "telemetry"
	recordTriggerManual    = "manual"
	recordTriggerMixed     = "mixed"
)

// Metadata captures persisted recording details.
type Metadata struct {
	ID        string
	StartedAt time.Time
	EndedAt   time.Time
	Duration  time.Duration
	Frequency string
	System    string
	Channel   string
	Talkgroup string
	FilePath  string
	FileSize  int64
	Trigger   string
}

// Config controls recording manager behavior.
type Config struct {
	OutputDir     string
	HangTime      time.Duration
	Now           func() time.Time
	WriterFactory func(path string) (PCMWriter, error)
	OnFinalized   func(Metadata) error
}

// Manager handles activity-driven clip lifecycle.
type Manager struct {
	mu sync.Mutex

	cfg      Config
	detector *activity.Detector

	writer   PCMWriter
	path     string
	started  time.Time
	lastSeen time.Time
	status   sds200.RuntimeStatus
	clipInfo sds200.RuntimeStatus
	trigger  string
	manual   bool
	faulted  error
}

func NewManager(cfg Config) *Manager {
	if cfg.HangTime <= 0 {
		cfg.HangTime = 10 * time.Second
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.WriterFactory == nil {
		cfg.WriterFactory = func(path string) (PCMWriter, error) {
			return NewFLACWriter(path)
		}
	}
	return &Manager{cfg: cfg, detector: activity.NewDetector(cfg.HangTime)}
}

func (m *Manager) UpdateTelemetry(status sds200.RuntimeStatus, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.faulted != nil {
		return m.faulted
	}

	if at.IsZero() {
		at = m.cfg.Now()
	}
	m.status = status
	active := sds200.IsTransmissionActive(status)
	res := m.detector.Evaluate(active, at)
	if res.BecameActive && m.writer == nil {
		if err := m.begin(at, status, recordTriggerTelemetry); err != nil {
			return err
		}
	}
	if m.writer != nil && active && m.trigger == recordTriggerManual {
		m.trigger = recordTriggerMixed
	}
	if m.manual {
		return nil
	}
	if res.ShouldFinalize {
		if err := m.finalize(at); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) StartManual(status sds200.RuntimeStatus, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.faulted != nil {
		return m.faulted
	}
	if at.IsZero() {
		at = m.cfg.Now()
	}
	m.status = status

	if m.writer == nil {
		trigger := recordTriggerManual
		if sds200.IsTransmissionActive(status) {
			trigger = recordTriggerMixed
		}
		if err := m.begin(at, status, trigger); err != nil {
			return err
		}
		m.manual = true
		return nil
	}
	m.manual = true
	if m.trigger == recordTriggerTelemetry {
		m.trigger = recordTriggerMixed
	}
	if sds200.IsTransmissionActive(status) && m.trigger == recordTriggerManual {
		m.trigger = recordTriggerMixed
	}
	return nil
}

func (m *Manager) StopManual(at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.faulted != nil {
		return m.faulted
	}
	if at.IsZero() {
		at = m.cfg.Now()
	}
	m.manual = false
	if m.writer == nil {
		return nil
	}
	return m.finalize(at)
}

func (m *Manager) PushPCM(samples []int16, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.faulted != nil {
		return m.faulted
	}

	if at.IsZero() {
		at = m.cfg.Now()
	}
	if m.writer == nil {
		return nil
	}
	if err := m.writer.WritePCM(samples); err != nil {
		m.abortWriter()
		m.faulted = fmt.Errorf("recording write fault: %w", err)
		return m.faulted
	}
	m.lastSeen = at
	return nil
}

func (m *Manager) Tick(at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.faulted != nil {
		return m.faulted
	}
	if at.IsZero() {
		at = m.cfg.Now()
	}
	if m.writer == nil {
		return nil
	}
	if m.manual {
		return nil
	}
	if m.detector.State() == activity.StateHang {
		res := m.detector.Evaluate(false, at)
		if res.ShouldFinalize {
			return m.finalize(at)
		}
	}
	return nil
}

func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.faulted != nil {
		return m.faulted
	}
	if m.writer == nil {
		return nil
	}
	return m.finalize(m.cfg.Now())
}

func (m *Manager) UpdateOutputDir(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return errors.New("recording output directory is required")
	}
	m.mu.Lock()
	m.cfg.OutputDir = path
	m.mu.Unlock()
	return nil
}

func (m *Manager) begin(at time.Time, status sds200.RuntimeStatus, trigger string) error {
	if m.cfg.OutputDir == "" {
		return errors.New("recording output directory is required")
	}
	name := fmt.Sprintf("%s_%s_%s_%s.flac",
		at.Format("20060102_150405"),
		sanitizeSegment(status.Frequency, "unknown_frequency"),
		sanitizeSegment(status.System, "unknown_system"),
		sanitizeSegment(status.Channel, "unknown_channel"),
	)
	path := filepath.Join(m.cfg.OutputDir, name)
	w, err := m.cfg.WriterFactory(path)
	if err != nil {
		return err
	}
	m.writer = w
	m.path = path
	m.started = at
	m.lastSeen = at
	m.clipInfo = status
	if strings.TrimSpace(trigger) == "" {
		trigger = recordTriggerTelemetry
	}
	m.trigger = trigger
	return nil
}

func (m *Manager) finalize(at time.Time) error {
	if m.writer == nil {
		return nil
	}
	size, err := m.writer.Finalize()
	if err != nil {
		m.abortWriter()
		m.faulted = fmt.Errorf("recording finalize fault: %w", err)
		return m.faulted
	}
	trigger := m.trigger
	if strings.TrimSpace(trigger) == "" {
		trigger = recordTriggerTelemetry
	}
	meta := Metadata{
		ID:        strconv.FormatInt(m.started.UnixNano(), 10),
		StartedAt: m.started,
		EndedAt:   at,
		Duration:  at.Sub(m.started),
		Frequency: m.clipInfo.Frequency,
		System:    m.clipInfo.System,
		Channel:   m.clipInfo.Channel,
		Talkgroup: m.clipInfo.Talkgroup,
		FilePath:  m.path,
		FileSize:  size,
		Trigger:   trigger,
	}
	m.writer = nil
	m.path = ""
	m.started = time.Time{}
	m.lastSeen = time.Time{}
	m.clipInfo = sds200.RuntimeStatus{}
	m.trigger = ""
	m.manual = false
	if m.cfg.OnFinalized != nil {
		if err := m.cfg.OnFinalized(meta); err != nil {
			return fmt.Errorf("on finalized callback: %w", err)
		}
	}
	return nil
}

func (m *Manager) abortWriter() {
	if m.writer != nil {
		_ = m.writer.Abort()
	}
	m.writer = nil
	m.path = ""
	m.started = time.Time{}
	m.lastSeen = time.Time{}
	m.clipInfo = sds200.RuntimeStatus{}
	m.trigger = ""
	m.manual = false
}

func sanitizeSegment(s, fallback string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return fallback
	}
	repl := strings.NewReplacer(
		" ", "_",
		"/", "_",
		"\\", "_",
		":", "_",
		"*", "_",
		"?", "_",
		"\"", "_",
		"<", "_",
		">", "_",
		"|", "_",
		",", "_",
	)
	s = repl.Replace(s)
	s = strings.Trim(s, "._-")
	if s == "" {
		return fallback
	}
	return s
}
