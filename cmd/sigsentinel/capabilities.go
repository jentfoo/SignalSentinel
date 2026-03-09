package main

import (
	"errors"
	"fmt"
)

type CapabilitySafetyClass string

const (
	CapabilitySafe        CapabilitySafetyClass = "safe"
	CapabilityDisruptive  CapabilitySafetyClass = "disruptive"
	CapabilityDestructive CapabilitySafetyClass = "destructive"
)

type CapabilitySpec struct {
	Command            string
	Safety             CapabilitySafetyClass
	DefaultEnabled     bool
	ExpertOnly         bool
	RequiresConnected  bool
	RequiresHold       bool
	RequiresNotHold    bool
	RequiresHoldTarget bool
}

type CapabilityAvailability struct {
	Intent         ControlIntent
	Command        string
	Safety         CapabilitySafetyClass
	Enabled        bool
	Available      bool
	DisabledReason string
}

func DefaultCapabilityRegistry() map[ControlIntent]CapabilitySpec {
	return map[ControlIntent]CapabilitySpec{
		IntentHold:                   {Command: "HLD", Safety: CapabilitySafe, DefaultEnabled: true, RequiresConnected: true, RequiresNotHold: true, RequiresHoldTarget: true},
		IntentResumeScan:             {Command: "JPM", Safety: CapabilitySafe, DefaultEnabled: true, RequiresConnected: true, RequiresHold: true},
		IntentNext:                   {Command: "NXT", Safety: CapabilitySafe, DefaultEnabled: true, RequiresConnected: true, RequiresHoldTarget: true},
		IntentPrevious:               {Command: "PRV", Safety: CapabilitySafe, DefaultEnabled: true, RequiresConnected: true, RequiresHoldTarget: true},
		IntentJumpNumberTag:          {Command: "JNT", Safety: CapabilitySafe, DefaultEnabled: true, RequiresConnected: true},
		IntentQuickSearchHold:        {Command: "QSH", Safety: CapabilitySafe, DefaultEnabled: true, RequiresConnected: true},
		IntentJumpMode:               {Command: "JPM", Safety: CapabilitySafe, DefaultEnabled: true, RequiresConnected: true},
		IntentSetFavoritesQuickKeys:  {Command: "FQK", Safety: CapabilitySafe, DefaultEnabled: true, RequiresConnected: true},
		IntentSetSystemQuickKeys:     {Command: "SQK", Safety: CapabilitySafe, DefaultEnabled: true, RequiresConnected: true},
		IntentSetDepartmentQuickKeys: {Command: "DQK", Safety: CapabilitySafe, DefaultEnabled: true, RequiresConnected: true},
		IntentSetServiceTypes:        {Command: "SVC", Safety: CapabilitySafe, DefaultEnabled: true, RequiresConnected: true},
		IntentSetRecordOn:            {Command: "URC", Safety: CapabilityDisruptive, DefaultEnabled: true, RequiresConnected: true},
		IntentSetRecordOff:           {Command: "URC", Safety: CapabilityDisruptive, DefaultEnabled: true, RequiresConnected: true},
		IntentAvoid:                  {Command: "AVD", Safety: CapabilityDisruptive, DefaultEnabled: true, RequiresConnected: true, RequiresHoldTarget: true},
		IntentUnavoid:                {Command: "AVD", Safety: CapabilityDisruptive, DefaultEnabled: true, RequiresConnected: true, RequiresHoldTarget: true},
		IntentSetVolume:              {Command: "VOL", Safety: CapabilitySafe, DefaultEnabled: true, RequiresConnected: true},
		IntentSetSquelch:             {Command: "SQL", Safety: CapabilitySafe, DefaultEnabled: true, RequiresConnected: true},
		IntentMenuEnter:              {Command: "MNU", Safety: CapabilityDisruptive, DefaultEnabled: false, ExpertOnly: true, RequiresConnected: true},
		IntentMenuSetValue:           {Command: "MSV", Safety: CapabilityDisruptive, DefaultEnabled: false, ExpertOnly: true, RequiresConnected: true},
		IntentMenuBack:               {Command: "MSB", Safety: CapabilitySafe, DefaultEnabled: false, ExpertOnly: true, RequiresConnected: true},
		IntentAnalyzeStart:           {Command: "AST", Safety: CapabilityDisruptive, DefaultEnabled: false, ExpertOnly: true, RequiresConnected: true},
		IntentAnalyzePauseResume:     {Command: "APR", Safety: CapabilityDisruptive, DefaultEnabled: false, ExpertOnly: true, RequiresConnected: true},
		IntentPushWaterfall:          {Command: "PWF", Safety: CapabilityDisruptive, DefaultEnabled: false, ExpertOnly: true, RequiresConnected: true},
		IntentGetWaterfall:           {Command: "GWF", Safety: CapabilityDisruptive, DefaultEnabled: false, ExpertOnly: true, RequiresConnected: true},
		IntentSetDateTime:            {Command: "DTM", Safety: CapabilityDisruptive, DefaultEnabled: false, ExpertOnly: true, RequiresConnected: true},
		IntentSetLocationRange:       {Command: "LCR", Safety: CapabilityDisruptive, DefaultEnabled: false, ExpertOnly: true, RequiresConnected: true},
		IntentKeepAlive:              {Command: "KAL", Safety: CapabilitySafe, DefaultEnabled: false, ExpertOnly: true, RequiresConnected: true},
		IntentPowerOff:               {Command: "POF", Safety: CapabilityDestructive, DefaultEnabled: false, ExpertOnly: true, RequiresConnected: true},
	}
}

func ValidateCapabilityRegistry(registry map[ControlIntent]CapabilitySpec) error {
	if len(registry) == 0 {
		return errors.New("capability registry is empty")
	}
	for intent, spec := range registry {
		if intent == "" {
			return errors.New("capability intent is empty")
		}
		if spec.Command == "" {
			return fmt.Errorf("capability %s has empty command", intent)
		}
		switch spec.Safety {
		case CapabilitySafe, CapabilityDisruptive, CapabilityDestructive:
		default:
			return fmt.Errorf("capability %s has invalid safety class %q", intent, spec.Safety)
		}
	}
	return nil
}

func EvaluateCapabilities(registry map[ControlIntent]CapabilitySpec, state RuntimeState, expertEnabled bool) map[ControlIntent]CapabilityAvailability {
	out := make(map[ControlIntent]CapabilityAvailability, len(registry))
	for intent, spec := range registry {
		item := CapabilityAvailability{
			Intent:    intent,
			Command:   spec.Command,
			Safety:    spec.Safety,
			Enabled:   spec.DefaultEnabled,
			Available: true,
		}
		disable := func(reason string) {
			if item.Available {
				item.Available = false
				item.DisabledReason = reason
			}
		}
		if spec.ExpertOnly && !expertEnabled {
			disable("available in expert mode only")
		}
		if !spec.DefaultEnabled {
			disable("disabled by default")
		}
		if spec.RequiresConnected && !state.Scanner.Connected {
			disable("scanner is disconnected")
		}
		if spec.RequiresHold && !state.Scanner.Hold {
			disable("scanner is not in hold mode")
		}
		if spec.RequiresNotHold && state.Scanner.Hold {
			disable("scanner is already in hold mode")
		}
		if spec.RequiresHoldTarget {
			target := state.Scanner.HoldTarget
			if target.Keyword == "" || target.Arg1 == "" {
				disable("hold target unavailable")
			}
		}
		out[intent] = item
	}
	return out
}

func ValidateCapabilityDefaults(registry map[ControlIntent]CapabilitySpec, state RuntimeState, expertEnabled bool) error {
	items := EvaluateCapabilities(registry, state, expertEnabled)
	for intent, item := range items {
		if item.Command == "" {
			return fmt.Errorf("capability %s has empty command", intent)
		}
		if !item.Available && item.DisabledReason == "" {
			return fmt.Errorf("capability %s is disabled without a reason", intent)
		}
	}
	return nil
}
