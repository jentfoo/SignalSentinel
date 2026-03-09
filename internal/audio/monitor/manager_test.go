package monitor

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManagerSetListenAndPushFrame(t *testing.T) {
	t.Parallel()

	sink := newFakeSink()
	manager := NewManager(Config{
		GainDB: 6.0,
		SinkFactory: SinkFactoryFunc(func(outputDevice string) (Sink, error) {
			return sink, nil
		}),
	})

	require.NoError(t, manager.SetListen(true))
	t.Cleanup(func() { _ = manager.Close() })

	manager.PushFrame(Frame{
		Samples:      []int16{1000, -1000},
		RTPTimestamp: 1,
	})

	samples := sink.waitWrite(t)
	require.Len(t, samples, 2)
	assert.InDelta(t, 1995, samples[0], 4)
	assert.InDelta(t, -1995, samples[1], 4)
}

func TestManagerMuteZeroesSamples(t *testing.T) {
	t.Parallel()

	sink := newFakeSink()
	manager := NewManager(Config{
		SinkFactory: SinkFactoryFunc(func(outputDevice string) (Sink, error) {
			return sink, nil
		}),
	})
	require.NoError(t, manager.SetListen(true))
	require.NoError(t, manager.SetMuted(true))
	t.Cleanup(func() { _ = manager.Close() })

	manager.PushFrame(Frame{
		Samples:      []int16{1234, -999},
		RTPTimestamp: 1,
	})

	samples := sink.waitWrite(t)
	assert.Equal(t, []int16{0, 0}, samples)
}

func TestManagerJitterBufferReorders(t *testing.T) {
	t.Parallel()

	sink := newFakeSink()
	manager := NewManager(Config{
		SinkFactory: SinkFactoryFunc(func(outputDevice string) (Sink, error) {
			return sink, nil
		}),
	})
	require.NoError(t, manager.SetListen(true))
	t.Cleanup(func() { _ = manager.Close() })

	manager.PushFrame(Frame{Samples: []int16{1, 1}, RTPTimestamp: 2})
	first := sink.waitWrite(t)
	assert.Equal(t, []int16{1, 1}, first)

	manager.PushFrame(Frame{Samples: []int16{3, 3}, RTPTimestamp: 6}) // missing timestamp 4
	select {
	case <-sink.writes:
		require.FailNow(t, "unexpected write before missing frame arrived")
	case <-time.After(50 * time.Millisecond):
	}

	manager.PushFrame(Frame{Samples: []int16{2, 2}, RTPTimestamp: 4})
	second := sink.waitWrite(t)
	third := sink.waitWrite(t)
	assert.Equal(t, []int16{2, 2}, second)
	assert.Equal(t, []int16{3, 3}, third)
}

func TestManagerJitterBufferDropsLateStaleFrames(t *testing.T) {
	t.Parallel()

	sink := newFakeSink()
	manager := NewManager(Config{
		SinkFactory: SinkFactoryFunc(func(outputDevice string) (Sink, error) {
			return sink, nil
		}),
	})
	require.NoError(t, manager.SetListen(true))
	t.Cleanup(func() { _ = manager.Close() })

	manager.PushFrame(Frame{Samples: []int16{1, 1}, RTPTimestamp: 2})
	manager.PushFrame(Frame{Samples: []int16{2, 2}, RTPTimestamp: 4})
	assert.Equal(t, []int16{1, 1}, sink.waitWrite(t))
	assert.Equal(t, []int16{2, 2}, sink.waitWrite(t))

	// Already-played timestamp should be dropped and never emitted.
	manager.PushFrame(Frame{Samples: []int16{9, 9}, RTPTimestamp: 2})
	select {
	case got := <-sink.writes:
		require.FailNowf(t, "stale frame should not be emitted", "unexpected samples: %v", got)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestJitterBufferOverflowDoesNotRewindOnStalePackets(t *testing.T) {
	t.Parallel()

	frame := func(ts uint32, v int16) Frame {
		return Frame{Samples: []int16{v, v}, RTPTimestamp: ts}
	}

	buf := newJitterBuffer(2)
	assert.Equal(t, []Frame{frame(100, 1)}, buf.Push(frame(100, 1)))
	assert.Equal(t, uint32(102), buf.expected)

	assert.Empty(t, buf.Push(frame(104, 2)))
	assert.Empty(t, buf.Push(frame(106, 3)))
	droppedGap := buf.Push(frame(108, 4))
	require.Len(t, droppedGap, 1)
	assert.Equal(t, uint32(104), droppedGap[0].RTPTimestamp)
	assert.Equal(t, uint32(106), buf.expected)

	// Missing earlier packet arrives after we already skipped ahead; it must be dropped.
	assert.Nil(t, buf.Push(frame(102, 9)))
	assert.Equal(t, uint32(106), buf.expected)

	out := buf.Push(frame(110, 5))
	require.Len(t, out, 3)
	assert.Equal(t, uint32(106), out[0].RTPTimestamp)
	assert.Equal(t, uint32(108), out[1].RTPTimestamp)
	assert.Equal(t, uint32(110), out[2].RTPTimestamp)
}

func TestManagerWorkerErrorIsNonFatal(t *testing.T) {
	t.Parallel()

	onErr := make(chan error, 1)
	failing := newFakeSink()
	failing.writeErr = errors.New("speaker offline")

	manager := NewManager(Config{
		SinkFactory: SinkFactoryFunc(func(outputDevice string) (Sink, error) {
			return failing, nil
		}),
		OnError: func(err error) {
			select {
			case onErr <- err:
			default:
			}
		},
	})
	require.NoError(t, manager.SetListen(true))
	t.Cleanup(func() { _ = manager.Close() })

	manager.PushFrame(Frame{Samples: []int16{1, 1}, RTPTimestamp: 1})

	select {
	case err := <-onErr:
		require.Error(t, err)
		assert.Contains(t, err.Error(), "monitor output failed")
	case <-time.After(time.Second):
		require.FailNow(t, "expected monitor error callback")
	}

	snapshot := manager.Snapshot()
	assert.False(t, snapshot.Enabled)
	assert.Contains(t, snapshot.LastError, "speaker offline")
}

func TestManagerSetOutputDeviceRestarts(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	opened := make([]string, 0, 2)
	manager := NewManager(Config{
		SinkFactory: SinkFactoryFunc(func(outputDevice string) (Sink, error) {
			mu.Lock()
			opened = append(opened, outputDevice)
			mu.Unlock()
			return newFakeSink(), nil
		}),
	})
	require.NoError(t, manager.SetListen(true))
	require.NoError(t, manager.SetOutputDevice("hw:1,0"))
	t.Cleanup(func() { _ = manager.Close() })

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, opened, 2)
	assert.Equal(t, "system-default", opened[0])
	assert.Equal(t, "hw:1,0", opened[1])
}

func TestManagerGainValidation(t *testing.T) {
	t.Parallel()

	manager := NewManager(Config{})
	err := manager.SetGainDB(99)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "between")
}

type fakeSink struct {
	mu       sync.Mutex
	writes   chan []int16
	writeErr error
	closed   bool
}

func newFakeSink() *fakeSink {
	return &fakeSink{
		writes: make(chan []int16, 16),
	}
}

func (f *fakeSink) WritePCM(samples []int16) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writeErr != nil {
		return f.writeErr
	}
	copySamples := append([]int16(nil), samples...)
	select {
	case f.writes <- copySamples:
	default:
	}
	return nil
}

func (f *fakeSink) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

func (f *fakeSink) waitWrite(t *testing.T) []int16 {
	t.Helper()
	select {
	case samples := <-f.writes:
		return samples
	case <-time.After(time.Second):
		require.FailNow(t, "timed out waiting for monitor write")
		return nil
	}
}
