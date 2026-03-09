package monitor

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	minGainDB         = -60.0
	maxGainDB         = 24.0
	defaultBufferSize = 128
	defaultJitterSize = 6
)

// Frame is one decoded PCM frame from RTP ingest.
type Frame struct {
	Samples      []int16
	ReceivedAt   time.Time
	RTPTimestamp uint32
}

// Status is the monitor runtime state exposed to callers/UI.
type Status struct {
	Enabled      bool
	Muted        bool
	GainDB       float64
	OutputDevice string
	LastError    string
	UpdatedAt    time.Time
}

// Sink writes PCM samples to a local output destination.
type Sink interface {
	WritePCM(samples []int16) error
	Close() error
}

// SinkFactory creates monitor sinks.
type SinkFactory interface {
	Open(outputDevice string) (Sink, error)
}

// SinkFactoryFunc adapts a function into a SinkFactory.
type SinkFactoryFunc func(outputDevice string) (Sink, error)

func (f SinkFactoryFunc) Open(outputDevice string) (Sink, error) {
	return f(outputDevice)
}

// Config controls monitor behavior.
type Config struct {
	OutputDevice   string
	GainDB         float64
	BufferFrames   int
	JitterFrames   int
	SinkFactory    SinkFactory
	Logger         *log.Logger
	OnStatusChange func(Status)
	OnError        func(error)
}

// Manager handles local live-audio monitor playback.
type Manager struct {
	mu sync.Mutex

	cfg Config

	enabled      bool
	muted        bool
	gainDB       float64
	outputDevice string
	lastError    string
	updatedAt    time.Time

	sink       Sink
	frameQueue chan []int16
	jitter     *jitterBuffer
	cancel     context.CancelFunc
	workerWG   sync.WaitGroup

	closed bool
}

func NewManager(cfg Config) *Manager {
	if cfg.BufferFrames <= 0 {
		cfg.BufferFrames = defaultBufferSize
	}
	if cfg.JitterFrames <= 0 {
		cfg.JitterFrames = defaultJitterSize
	}
	if cfg.SinkFactory == nil {
		cfg.SinkFactory = defaultSinkFactory{}
	}

	gain := cfg.GainDB
	if gain < minGainDB || gain > maxGainDB {
		gain = 0
	}

	m := &Manager{
		cfg:          cfg,
		gainDB:       gain,
		outputDevice: normalizeOutputDevice(cfg.OutputDevice),
		updatedAt:    time.Now().UTC(),
	}
	return m
}

func (m *Manager) Snapshot() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.snapshotLocked()
}

func (m *Manager) SetListen(enabled bool) error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return errors.New("monitor manager is closed")
	}
	if enabled {
		if m.enabled {
			status := m.snapshotLocked()
			m.mu.Unlock()
			m.emitStatus(status)
			return nil
		}
		err := m.startLocked()
		status := m.snapshotLocked()
		m.mu.Unlock()
		m.emitStatus(status)
		if err != nil {
			return err
		}
		return nil
	}
	m.stopLocked()
	status := m.snapshotLocked()
	m.mu.Unlock()
	m.emitStatus(status)
	return nil
}

func (m *Manager) SetMuted(muted bool) error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return errors.New("monitor manager is closed")
	}
	m.muted = muted
	m.updatedAt = time.Now().UTC()
	status := m.snapshotLocked()
	m.mu.Unlock()
	m.emitStatus(status)
	return nil
}

func (m *Manager) SetGainDB(gainDB float64) error {
	if gainDB < minGainDB || gainDB > maxGainDB {
		return fmt.Errorf("monitor gain must be between %.0f and %.0f dB", minGainDB, maxGainDB)
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return errors.New("monitor manager is closed")
	}
	m.gainDB = gainDB
	m.updatedAt = time.Now().UTC()
	status := m.snapshotLocked()
	m.mu.Unlock()
	m.emitStatus(status)
	return nil
}

func (m *Manager) SetOutputDevice(outputDevice string) error {
	outputDevice = normalizeOutputDevice(outputDevice)
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return errors.New("monitor manager is closed")
	}
	if outputDevice == m.outputDevice {
		status := m.snapshotLocked()
		m.mu.Unlock()
		m.emitStatus(status)
		return nil
	}
	listening := m.enabled
	m.outputDevice = outputDevice
	m.updatedAt = time.Now().UTC()
	m.mu.Unlock()

	if !listening {
		m.emitStatus(m.Snapshot())
		return nil
	}

	// Restart with the new output device.
	if err := m.SetListen(false); err != nil {
		return err
	}
	if err := m.SetListen(true); err != nil {
		return err
	}
	return nil
}

func (m *Manager) PushFrame(frame Frame) {
	if len(frame.Samples) == 0 {
		return
	}
	m.mu.Lock()
	if m.closed || !m.enabled || m.frameQueue == nil || m.jitter == nil {
		m.mu.Unlock()
		return
	}
	ordered := m.jitter.Push(frame)
	muted := m.muted
	gainDB := m.gainDB
	frameQueue := m.frameQueue
	m.mu.Unlock()

	for _, item := range ordered {
		samples := applyMonitorAudio(item.Samples, muted, gainDB)
		if len(samples) == 0 {
			continue
		}
		select {
		case frameQueue <- samples:
		default:
			select {
			case <-frameQueue:
			default:
			}
			select {
			case frameQueue <- samples:
			default:
			}
		}
	}
}

func (m *Manager) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.stopLocked()
	m.closed = true
	status := m.snapshotLocked()
	m.mu.Unlock()
	m.emitStatus(status)
	return nil
}

func (m *Manager) startLocked() error {
	sink, err := m.cfg.SinkFactory.Open(m.outputDevice)
	if err != nil {
		wrapped := fmt.Errorf("monitor output start failed: %w", err)
		m.setErrorLocked(wrapped)
		return wrapped
	}
	ctx, cancel := context.WithCancel(context.Background())
	queue := make(chan []int16, m.cfg.BufferFrames)

	m.enabled = true
	m.sink = sink
	m.frameQueue = queue
	m.jitter = newJitterBuffer(m.cfg.JitterFrames)
	m.cancel = cancel
	m.lastError = ""
	m.updatedAt = time.Now().UTC()
	m.workerWG.Add(1)
	go m.runWorker(sink, queue, ctx)
	return nil
}

func (m *Manager) stopLocked() {
	cancel := m.cancel
	sink := m.sink
	m.enabled = false
	m.cancel = nil
	m.sink = nil
	m.frameQueue = nil
	m.jitter = nil
	m.updatedAt = time.Now().UTC()

	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	m.workerWG.Wait()
	if sink != nil {
		_ = sink.Close()
	}
	m.mu.Lock()
}

func (m *Manager) runWorker(sink Sink, queue <-chan []int16, ctx context.Context) {
	defer m.workerWG.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case samples := <-queue:
			if len(samples) == 0 {
				continue
			}
			if err := sink.WritePCM(samples); err != nil {
				m.handleWorkerError(err, sink)
				return
			}
		}
	}
}

func (m *Manager) handleWorkerError(err error, sink Sink) {
	wrapped := fmt.Errorf("monitor output failed: %w", err)
	var status Status
	m.mu.Lock()
	if sink != m.sink {
		m.mu.Unlock()
		return
	}
	m.enabled = false
	m.cancel = nil
	m.sink = nil
	m.frameQueue = nil
	m.jitter = nil
	m.setErrorLocked(wrapped)
	status = m.snapshotLocked()
	m.mu.Unlock()

	_ = sink.Close()
	m.emitStatus(status)
	m.emitError(wrapped)
}

func (m *Manager) setErrorLocked(err error) {
	if err == nil {
		m.lastError = ""
		return
	}
	m.lastError = err.Error()
	m.updatedAt = time.Now().UTC()
	if m.cfg.Logger != nil {
		m.cfg.Logger.Printf("monitor: %v", err)
	} else {
		log.Printf("monitor: %v", err)
	}
}

func (m *Manager) snapshotLocked() Status {
	return Status{
		Enabled:      m.enabled,
		Muted:        m.muted,
		GainDB:       m.gainDB,
		OutputDevice: m.outputDevice,
		LastError:    m.lastError,
		UpdatedAt:    m.updatedAt,
	}
}

func (m *Manager) emitStatus(status Status) {
	if m.cfg.OnStatusChange != nil {
		m.cfg.OnStatusChange(status)
	}
}

func (m *Manager) emitError(err error) {
	if err == nil {
		return
	}
	if m.cfg.OnError != nil {
		m.cfg.OnError(err)
	}
}

func normalizeOutputDevice(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "system-default"
	}
	return value
}

func applyMonitorAudio(samples []int16, muted bool, gainDB float64) []int16 {
	out := make([]int16, len(samples))
	if len(samples) == 0 {
		return out
	}
	if muted {
		return out
	}
	scale := math.Pow(10, gainDB/20.0)
	for i, sample := range samples {
		value := int(math.Round(float64(sample) * scale))
		if value > math.MaxInt16 {
			value = math.MaxInt16
		} else if value < math.MinInt16 {
			value = math.MinInt16
		}
		out[i] = int16(value)
	}
	return out
}

type jitterBuffer struct {
	maxPending int
	pending    map[uint32]Frame
	expected   uint32
	ready      bool
}

func newJitterBuffer(maxPending int) *jitterBuffer {
	if maxPending <= 0 {
		maxPending = defaultJitterSize
	}
	return &jitterBuffer{
		maxPending: maxPending,
		pending:    make(map[uint32]Frame),
	}
}

func (b *jitterBuffer) Push(frame Frame) []Frame {
	if len(frame.Samples) == 0 {
		return nil
	}
	if !b.ready {
		b.expected = frame.RTPTimestamp
		b.ready = true
	} else if rtpTimestampBefore(frame.RTPTimestamp, b.expected) {
		// Late/stale frame that has already been played (or skipped) - drop it.
		return nil
	}

	if _, exists := b.pending[frame.RTPTimestamp]; !exists {
		b.pending[frame.RTPTimestamp] = cloneFrame(frame)
	}

	out := make([]Frame, 0, 2)
	for {
		item, ok := b.pending[b.expected]
		if !ok {
			break
		}
		out = append(out, item)
		delete(b.pending, b.expected)
		step := uint32(len(item.Samples))
		if step == 0 {
			step = 1
		}
		b.expected += step
	}

	if len(b.pending) > b.maxPending {
		keys := make([]uint32, 0, len(b.pending))
		for ts := range b.pending {
			keys = append(keys, ts)
		}
		sort.Slice(keys, func(i, j int) bool { return rtpTimestampBefore(keys[i], keys[j]) })
		for len(b.pending) > b.maxPending && len(keys) > 0 {
			ts := keys[0]
			keys = keys[1:]
			if rtpTimestampBefore(ts, b.expected) {
				delete(b.pending, ts)
				continue
			}
			item, ok := b.pending[ts]
			if !ok {
				continue
			}
			out = append(out, item)
			delete(b.pending, ts)
			step := uint32(len(item.Samples))
			if step == 0 {
				step = 1
			}
			b.expected = ts + step
		}
	}

	return out
}

// RTP timestamps are uint32 and wrap around. This comparison preserves ordering
// as long as compared values are not more than 2^31 apart.
func rtpTimestampBefore(a, b uint32) bool {
	return int32(a-b) < 0
}

func cloneFrame(frame Frame) Frame {
	samples := make([]int16, len(frame.Samples))
	copy(samples, frame.Samples)
	frame.Samples = samples
	return frame
}
