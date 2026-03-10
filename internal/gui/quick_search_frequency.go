package gui

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

const quickSearchHzThreshold = 25_000_000

func parseQuickSearchFrequency(value string) (int, error) {
	trimmed := strings.TrimSpace(strings.ToLower(value))
	if trimmed == "" {
		return 0, errors.New("quick search frequency is required")
	}
	trimmed = strings.ReplaceAll(trimmed, ",", "")

	switch {
	case strings.HasSuffix(trimmed, "mhz"):
		return parseQuickSearchFrequencyMHz(trimmed[:len(trimmed)-len("mhz")])
	case strings.HasSuffix(trimmed, "hz"):
		return parseQuickSearchFrequencyHz(trimmed[:len(trimmed)-len("hz")])
	case strings.Contains(trimmed, "."):
		return parseQuickSearchFrequencyMHz(trimmed)
	default:
		units, err := strconv.ParseInt(trimmed, 10, 64)
		if err != nil {
			return 0, errors.New("quick search frequency must be numeric")
		}
		if units <= 0 {
			return 0, errors.New("quick search frequency must be > 0")
		}
		if units >= quickSearchHzThreshold {
			units = (units + 50) / 100
		}
		return normalizeQuickSearchFrequency(units)
	}
}

func parseQuickSearchFrequencyMHz(value string) (int, error) {
	mhz, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil {
		return 0, errors.New("quick search frequency must be numeric")
	}
	if mhz <= 0 {
		return 0, errors.New("quick search frequency must be > 0")
	}
	units := int64(math.Round(mhz * 10_000))
	return normalizeQuickSearchFrequency(units)
}

func parseQuickSearchFrequencyHz(value string) (int, error) {
	hz, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return 0, errors.New("quick search frequency must be numeric")
	}
	if hz <= 0 {
		return 0, errors.New("quick search frequency must be > 0")
	}
	units := (hz + 50) / 100
	return normalizeQuickSearchFrequency(units)
}

func normalizeQuickSearchFrequency(units int64) (int, error) {
	if units <= 0 {
		return 0, errors.New("quick search frequency must be > 0")
	}
	maxInt := int64(^uint(0) >> 1)
	if units > maxInt {
		return 0, fmt.Errorf("quick search frequency is too large (%d)", units)
	}
	return int(units), nil
}
