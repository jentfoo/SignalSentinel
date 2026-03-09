package main

import (
	"context"
	"sync"
	"time"

	"github.com/jentfoo/SignalSentinel/internal/chanutil"
	"github.com/jentfoo/SignalSentinel/internal/sds200"
)

type RuntimeState struct {
	Scanner sds200.RuntimeStatus
	Expert  ExpertRuntimeState
}

type ExpertRuntimeState struct {
	MenuStatusSummary   string
	AnalyzeSummary      string
	WaterfallSummary    string
	DateTimeSummary     string
	DateTimeValue       time.Time
	DaylightSaving      int
	HasDateTime         bool
	LocationSummary     string
	Latitude            string
	Longitude           string
	Range               string
	DeviceModel         string
	FirmwareVersion     string
	ChargeStatusSummary string
	KeepAliveStatus     string
	UpdatedAt           time.Time
}

type stateHub struct {
	mu          sync.RWMutex
	nextID      int
	subscribers map[int]chan RuntimeState
	state       RuntimeState
}

func newStateHub() *stateHub {
	return &stateHub{subscribers: make(map[int]chan RuntimeState)}
}

func (h *stateHub) snapshot() RuntimeState {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.state
}

func (h *stateHub) publish(state RuntimeState) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.state = state
	for _, ch := range h.subscribers {
		chanutil.PublishLatest(ch, state)
	}
}

func (h *stateHub) subscribe(ctx context.Context) <-chan RuntimeState {
	ch := make(chan RuntimeState, 1)

	h.mu.Lock()
	id := h.nextID
	h.nextID++
	current := h.state
	ch <- current
	h.subscribers[id] = ch
	h.mu.Unlock()

	go func() {
		<-ctx.Done()
		h.mu.Lock()
		delete(h.subscribers, id)
		close(ch)
		h.mu.Unlock()
	}()

	return ch
}
