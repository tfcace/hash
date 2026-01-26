package prediction

import (
	"sort"
	"time"
)

// CommandSequence tracks command-after-command patterns.
type CommandSequence struct {
	ID          int64
	PrevCommand string
	NextCommand string
	CwdPattern  string // Optional: directory pattern
	Count       int
	LastUsed    time.Time
}

// PathUsage tracks which paths are used with which commands.
type PathUsage struct {
	ID       int64
	Command  string // Normalized command (e.g., "vim", "cd")
	Path     string // The path used
	Cwd      string // Directory where it was used
	Count    int
	LastUsed time.Time
}

// PathSequence tracks paths used after specific commands.
type PathSequence struct {
	ID          int64
	PrevCommand string // Command that preceded path usage
	Path        string
	Count       int
	LastUsed    time.Time
}

// ScoredPrediction is a prediction with a confidence score.
type ScoredPrediction struct {
	Text        string
	Score       float64
	Source      string // "sequence", "path", "recent"
	IsPredicted bool   // For UI: mark as predicted vs filesystem
}

// SortPredictions sorts predictions by score descending.
func SortPredictions(predictions []ScoredPrediction) {
	sort.Slice(predictions, func(i, j int) bool {
		return predictions[i].Score > predictions[j].Score
	})
}

// Config holds prediction configuration.
type Config struct {
	Enabled             bool
	AcceptKeys          []string
	ConfidenceThreshold float64
	PathMinCount        int
	PathRecencyHours    int
}

// DefaultConfig returns default prediction configuration.
func DefaultConfig() Config {
	return Config{
		Enabled:             true,
		AcceptKeys:          []string{"right", "tab"},
		ConfidenceThreshold: 0.6,
		PathMinCount:        2,
		PathRecencyHours:    24,
	}
}
