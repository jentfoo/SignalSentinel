package gui

import "strings"

func DeriveLifecycleMode(connected, hold bool, mode string) string {
	if !connected {
		return "Disconnected"
	}
	if hold {
		return "Hold"
	}
	normalizedMode := strings.ToLower(strings.TrimSpace(mode))
	if strings.Contains(normalizedMode, "analy") || strings.Contains(normalizedMode, "pause") {
		return "Paused/Analyze"
	}
	return "Scanning"
}
