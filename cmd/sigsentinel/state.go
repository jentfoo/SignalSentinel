package main

import (
	"context"
	"sync"

	"github.com/jentfoo/SignalSentinel/internal/sds200"
)

type RuntimeState struct {
	Scanner sds200.RuntimeStatus
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
	h.state = state
	subs := make([]chan RuntimeState, 0, len(h.subscribers))
	for _, ch := range h.subscribers {
		subs = append(subs, ch)
	}
	h.mu.Unlock()

	for _, ch := range subs {
		select {
		case ch <- state:
		default:
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- state:
			default:
			}
		}
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
		h.mu.Unlock()
	}()

	return ch
}
