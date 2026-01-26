package completion

import "context"

// Item represents a single completion suggestion.
type Item struct {
	Value       string // The actual completion value
	Display     string // What to show in the menu (may differ from Value)
	Description string // Optional description
	Icon        string // Nerd Font icon (optional)
	Score       int    // For fuzzy sorting (higher = better match)
}

// Result holds completion results from a completer.
type Result struct {
	Items  []Item // List of completions
	Prefix string // Common prefix to preserve
}

// Completer provides completions for a given input.
type Completer interface {
	// Complete returns completions for the given line and cursor position.
	Complete(ctx context.Context, line string, pos int) (Result, error)

	// Name returns the completer name for logging.
	Name() string
}

// Priority defines completer ordering (lower = higher priority).
type Priority int

const (
	PriorityToolNative Priority = 100 // Try first for subcommand completion
	PriorityExecutable Priority = 150 // Executable names from PATH (command position only)
	PriorityFilesystem Priority = 200 // Fallback for file arguments
	PriorityAgent      Priority = 300 // AI-powered fallback
)
