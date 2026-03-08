package activity

import "time"

// State models the activity detector state machine.
type State string

const (
	StateIdle   State = "IDLE"
	StateActive State = "ACTIVE"
	StateHang   State = "HANG"
)

// Result is emitted after evaluating one detector input sample.
type Result struct {
	State          State
	BecameActive   bool
	ShouldFinalize bool
}

// Detector tracks telemetry activity transitions with a fixed hang time.
type Detector struct {
	hangTime  time.Duration
	state     State
	hangUntil time.Time
}

// NewDetector creates an activity detector with a fixed hang time.
func NewDetector(hangTime time.Duration) *Detector {
	if hangTime <= 0 {
		hangTime = 10 * time.Second
	}
	return &Detector{hangTime: hangTime, state: StateIdle}
}

// State returns the detector's current state.
func (d *Detector) State() State {
	if d == nil {
		return StateIdle
	}
	return d.state
}

// Evaluate advances the state machine for the given activity flag at the provided timestamp.
func (d *Detector) Evaluate(active bool, at time.Time) Result {
	if d == nil {
		return Result{State: StateIdle}
	}
	if at.IsZero() {
		at = time.Now()
	}

	switch d.state {
	case StateIdle:
		if active {
			d.state = StateActive
			return Result{State: d.state, BecameActive: true}
		}
	case StateActive:
		if !active {
			d.state = StateHang
			d.hangUntil = at.Add(d.hangTime)
		}
	case StateHang:
		if active {
			d.state = StateActive
			return Result{State: d.state, BecameActive: true}
		}
		if !at.Before(d.hangUntil) {
			d.state = StateIdle
			d.hangUntil = time.Time{}
			return Result{State: d.state, ShouldFinalize: true}
		}
	}

	return Result{State: d.state}
}
