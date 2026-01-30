package trace

import (
	"encoding/json"
	"time"
)

// Level represents trace verbosity.
type Level string

const (
	LevelVerbose  Level = "verbose"
	LevelDetailed Level = "detailed"
	LevelHigh     Level = "high"
)

// Entry is a single trace event.
type Entry struct {
	Timestamp string  `json:"ts"`
	DeltaMs   float64 `json:"delta_ms"`
	Goroutine int     `json:"goroutine"`
	Subsystem string  `json:"sub"`
	Level     Level   `json:"level"`
	Event     string  `json:"event"`
	Data      any     `json:"data,omitempty"`
}

// MarshalJSON implements json.Marshaler.
func (e Entry) MarshalJSON() ([]byte, error) {
	type alias Entry
	return json.Marshal(alias(e))
}

// StartEntry creates the trace_start entry.
func StartEntry(version string, subsystems []string, level Level, pid int) Entry {
	return Entry{
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Event:     "trace_start",
		Data: map[string]any{
			"version":    version,
			"subsystems": subsystems,
			"level":      level,
			"pid":        pid,
		},
	}
}

// EndEntry creates the trace_end entry.
func EndEntry(durationMs, eventsWritten int64) Entry {
	return Entry{
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Event:     "trace_end",
		Data: map[string]any{
			"duration_ms":    durationMs,
			"events_written": eventsWritten,
		},
	}
}
