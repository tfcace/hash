package config

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// ParseSize parses a human-friendly size string (e.g. "1MB", "512KB").
// Returns -1 for unlimited values like "unlimited".
func ParseSize(value string) (int64, error) {
	s := strings.TrimSpace(strings.ToLower(value))
	if s == "" {
		return 0, fmt.Errorf("empty size")
	}

	switch s {
	case "unlimited", "infinite", "inf":
		return -1, nil
	}

	numPart, unitPart := splitSize(s)
	if numPart == "" {
		return 0, fmt.Errorf("invalid size %q", value)
	}

	num, err := strconv.ParseFloat(numPart, 64)
	if err != nil || num < 0 {
		return 0, fmt.Errorf("invalid size %q", value)
	}

	multiplier, ok := sizeMultiplier(unitPart)
	if !ok {
		return 0, fmt.Errorf("invalid size unit %q", unitPart)
	}

	size := num * float64(multiplier)
	if size > math.MaxInt64 {
		return 0, fmt.Errorf("size too large")
	}

	return int64(size), nil
}

func splitSize(value string) (string, string) {
	for i := 0; i < len(value); i++ {
		ch := value[i]
		if (ch >= '0' && ch <= '9') || ch == '.' {
			continue
		}
		num := strings.TrimSpace(value[:i])
		unit := strings.TrimSpace(value[i:])
		return num, unit
	}
	return strings.TrimSpace(value), ""
}

func sizeMultiplier(unit string) (int64, bool) {
	switch unit {
	case "", "b", "byte", "bytes":
		return 1, true
	case "k", "kb", "kib":
		return 1024, true
	case "m", "mb", "mib":
		return 1024 * 1024, true
	case "g", "gb", "gib":
		return 1024 * 1024 * 1024, true
	case "t", "tb", "tib":
		return 1024 * 1024 * 1024 * 1024, true
	default:
		return 0, false
	}
}
