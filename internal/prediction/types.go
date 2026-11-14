package prediction

import "time"

// CommandSequence tracks command-after-command patterns.
type CommandSequence struct {
	ID          int64
	PrevCommand string
	NextCommand string
	CwdPattern  string // Optional: directory pattern
	Count       int
	LastUsed    time.Time
}

// Config holds prediction configuration.
type Config struct {
	Enabled             bool
	AcceptKeys          []string
	ConfidenceThreshold float64
	RecencyWindowHours  int // a sequence last used this long ago gets half the recency score
}

// DefaultConfig returns default prediction configuration.
func DefaultConfig() Config {
	return Config{
		Enabled:             true,
		AcceptKeys:          []string{"right", "tab"},
		ConfidenceThreshold: 0.6,
		RecencyWindowHours:  24,
	}
}
