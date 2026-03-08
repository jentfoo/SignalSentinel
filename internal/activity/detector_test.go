package activity

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewDetector(t *testing.T) {
	t.Parallel()

	t.Run("uses_default_hang", func(t *testing.T) {
		d := NewDetector(0)
		assert.Equal(t, StateIdle, d.State())
	})

	t.Run("uses_given_hang", func(t *testing.T) {
		d := NewDetector(3 * time.Second)
		assert.Equal(t, StateIdle, d.State())
	})
}

func TestDetectorState(t *testing.T) {
	t.Parallel()

	var d *Detector
	assert.Equal(t, StateIdle, d.State())
}

func TestDetectorEvaluate(t *testing.T) {
	t.Parallel()

	t.Run("basic_lifecycle", func(t *testing.T) {
		d := NewDetector(10 * time.Second)
		t0 := time.Date(2026, 3, 8, 10, 0, 0, 0, time.UTC)

		r := d.Evaluate(false, t0)
		assert.Equal(t, StateIdle, r.State)
		assert.False(t, r.BecameActive)

		r = d.Evaluate(true, t0.Add(time.Second))
		assert.Equal(t, StateActive, r.State)
		assert.True(t, r.BecameActive)

		r = d.Evaluate(false, t0.Add(2*time.Second))
		assert.Equal(t, StateHang, r.State)

		r = d.Evaluate(false, t0.Add(11*time.Second))
		assert.Equal(t, StateHang, r.State)
		assert.False(t, r.ShouldFinalize)

		r = d.Evaluate(false, t0.Add(12*time.Second))
		assert.Equal(t, StateIdle, r.State)
		assert.True(t, r.ShouldFinalize)
	})

	t.Run("hang_resumes_to_active", func(t *testing.T) {
		d := NewDetector(10 * time.Second)
		t0 := time.Date(2026, 3, 8, 10, 0, 0, 0, time.UTC)

		// IDLE -> ACTIVE
		d.Evaluate(true, t0)
		// ACTIVE -> HANG
		d.Evaluate(false, t0.Add(time.Second))
		// HANG -> ACTIVE (resume during hang window)
		r := d.Evaluate(true, t0.Add(5*time.Second))
		assert.Equal(t, StateActive, r.State)
		assert.False(t, r.ShouldFinalize)

		// ACTIVE -> HANG again
		d.Evaluate(false, t0.Add(6*time.Second))
		// Wait past hang expiry
		r = d.Evaluate(false, t0.Add(16*time.Second+time.Millisecond))
		assert.Equal(t, StateIdle, r.State)
		assert.True(t, r.ShouldFinalize)
	})

	t.Run("custom_hang_time_expires", func(t *testing.T) {
		d := NewDetector(3 * time.Second)
		t0 := time.Date(2026, 3, 8, 10, 0, 0, 0, time.UTC)

		d.Evaluate(true, t0)
		d.Evaluate(false, t0.Add(time.Second))

		// Still HANG at 2s
		r := d.Evaluate(false, t0.Add(3*time.Second))
		assert.Equal(t, StateHang, r.State)
		assert.False(t, r.ShouldFinalize)

		// ShouldFinalize at 4s (past 1s + 3s hang)
		r = d.Evaluate(false, t0.Add(4*time.Second+time.Millisecond))
		assert.Equal(t, StateIdle, r.State)
		assert.True(t, r.ShouldFinalize)
	})

	t.Run("rapid_cycling", func(t *testing.T) {
		d := NewDetector(10 * time.Second)
		t0 := time.Date(2026, 3, 8, 10, 0, 0, 0, time.UTC)

		d.Evaluate(true, t0)
		d.Evaluate(false, t0.Add(100*time.Millisecond))
		r := d.Evaluate(true, t0.Add(200*time.Millisecond))
		assert.Equal(t, StateActive, r.State)
		assert.False(t, r.ShouldFinalize)

		d.Evaluate(false, t0.Add(300*time.Millisecond))
		r = d.Evaluate(true, t0.Add(400*time.Millisecond))
		assert.Equal(t, StateActive, r.State)
		assert.False(t, r.ShouldFinalize)
	})

	t.Run("active_stays_active", func(t *testing.T) {
		d := NewDetector(10 * time.Second)
		t0 := time.Date(2026, 3, 8, 10, 0, 0, 0, time.UTC)

		r := d.Evaluate(true, t0)
		assert.True(t, r.BecameActive)

		r = d.Evaluate(true, t0.Add(time.Second))
		assert.Equal(t, StateActive, r.State)
		assert.False(t, r.BecameActive)

		r = d.Evaluate(true, t0.Add(2*time.Second))
		assert.Equal(t, StateActive, r.State)
		assert.False(t, r.BecameActive)
	})
}
