package main

import (
	"testing"

	"github.com/jentfoo/SignalSentinel/internal/sds200"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateCapabilityRegistry(t *testing.T) {
	t.Parallel()

	t.Run("accepts_default_registry", func(t *testing.T) {
		err := ValidateCapabilityRegistry(DefaultCapabilityRegistry())
		require.NoError(t, err)
	})

	t.Run("rejects_empty_registry", func(t *testing.T) {
		err := ValidateCapabilityRegistry(nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "empty")
	})

	t.Run("rejects_missing_command", func(t *testing.T) {
		err := ValidateCapabilityRegistry(map[ControlIntent]CapabilitySpec{
			IntentHold: {Safety: CapabilitySafe, DefaultEnabled: true},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "empty command")
	})
}

func TestEvaluateCapabilities(t *testing.T) {
	t.Parallel()

	registry := map[ControlIntent]CapabilitySpec{
		IntentHold:       {Command: "HLD", Safety: CapabilitySafe, DefaultEnabled: true, RequiresConnected: true, RequiresHoldTarget: true},
		IntentResumeScan: {Command: "JPM", Safety: CapabilitySafe, DefaultEnabled: true, RequiresConnected: true, RequiresHold: true},
		IntentPowerOff:   {Command: "POF", Safety: CapabilityDestructive, DefaultEnabled: false, ExpertOnly: true, RequiresConnected: true},
	}

	t.Run("disables_when_disconnected", func(t *testing.T) {
		state := RuntimeState{}
		caps := EvaluateCapabilities(registry, state, false)

		assert.False(t, caps[IntentHold].Available)
		assert.Equal(t, "scanner is disconnected", caps[IntentHold].DisabledReason)
	})

	t.Run("reports_hold_target_requirements", func(t *testing.T) {
		state := RuntimeState{Scanner: sds200.RuntimeStatus{Connected: true}}
		caps := EvaluateCapabilities(registry, state, false)

		assert.False(t, caps[IntentHold].Available)
		assert.Equal(t, "hold target unavailable", caps[IntentHold].DisabledReason)
	})

	t.Run("enables_hold_when_target_present", func(t *testing.T) {
		state := RuntimeState{Scanner: sds200.RuntimeStatus{Connected: true, HoldTarget: sds200.HoldTarget{Keyword: "TGID", Arg1: "100"}}}
		caps := EvaluateCapabilities(registry, state, false)

		assert.True(t, caps[IntentHold].Available)
		assert.Empty(t, caps[IntentHold].DisabledReason)
	})

	t.Run("expert_and_default_disabled_reason", func(t *testing.T) {
		state := RuntimeState{Scanner: sds200.RuntimeStatus{Connected: true}}
		caps := EvaluateCapabilities(registry, state, false)

		assert.False(t, caps[IntentPowerOff].Available)
		assert.Equal(t, "available in expert mode only", caps[IntentPowerOff].DisabledReason)
	})
}

func TestValidateCapabilityDefaults(t *testing.T) {
	t.Parallel()

	err := ValidateCapabilityDefaults(DefaultCapabilityRegistry(), RuntimeState{}, false)
	require.NoError(t, err)
}
