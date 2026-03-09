//go:build !headless

package gui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

func parseIndexList(text string, maxInclusive int) ([]int, error) {
	clean := strings.TrimSpace(text)
	if clean == "" {
		return nil, nil
	}
	clean = strings.ReplaceAll(clean, "\n", ",")
	clean = strings.ReplaceAll(clean, "\t", ",")
	parts := strings.Split(clean, ",")
	seen := make(map[int]struct{}, len(parts))
	out := make([]int, 0, len(parts))
	addIndex := func(value int) error {
		if value < 0 || value > maxInclusive {
			return fmt.Errorf("index %d is out of range 0-%d", value, maxInclusive)
		}
		if _, exists := seen[value]; !exists {
			seen[value] = struct{}{}
			out = append(out, value)
		}
		return nil
	}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if strings.Contains(part, "-") {
			rangeParts := strings.SplitN(part, "-", 2)
			lo, errLo := strconv.Atoi(strings.TrimSpace(rangeParts[0]))
			hi, errHi := strconv.Atoi(strings.TrimSpace(rangeParts[1]))
			if errLo == nil && errHi == nil && lo <= hi {
				for i := lo; i <= hi; i++ {
					if err := addIndex(i); err != nil {
						return nil, err
					}
				}
				continue
			}
		}
		value, err := strconv.Atoi(part)
		if err != nil {
			return nil, fmt.Errorf("invalid index %q", part)
		}
		if err := addIndex(value); err != nil {
			return nil, err
		}
	}
	sort.Ints(out)
	return out, nil
}

func encodeIndexList(indexes []int) string {
	if len(indexes) == 0 {
		return ""
	}
	var parts []string
	i := 0
	for i < len(indexes) {
		start := indexes[i]
		end := start
		for i+1 < len(indexes) && indexes[i+1] == end+1 {
			i++
			end = indexes[i]
		}
		if end-start >= 2 {
			parts = append(parts, fmt.Sprintf("%d-%d", start, end))
		} else if end > start {
			parts = append(parts, strconv.Itoa(start), strconv.Itoa(end))
		} else {
			parts = append(parts, strconv.Itoa(start))
		}
		i++
	}
	return strings.Join(parts, ",")
}

func indexesToBinaryState(indexes []int, length int) []int {
	out := make([]int, length)
	for _, idx := range indexes {
		if idx >= 0 && idx < length {
			out[idx] = 1
		}
	}
	return out
}

func binaryStateToIndexes(values []int) []int {
	out := make([]int, 0, len(values))
	for i, value := range values {
		if value == 1 {
			out = append(out, i)
		}
	}
	return out
}

func allEnabledState(length int) []int {
	out := make([]int, length)
	for i := range out {
		out[i] = 1
	}
	return out
}

func copyInts(values []int) []int {
	return append([]int(nil), values...)
}

func filterIndexes(indexes []int, query string) []int {
	query = strings.TrimSpace(query)
	if query == "" {
		return append([]int(nil), indexes...)
	}
	filtered := make([]int, 0, len(indexes))
	for _, value := range indexes {
		text := strconv.Itoa(value)
		if strings.Contains(text, query) {
			filtered = append(filtered, value)
		}
	}
	return filtered
}
