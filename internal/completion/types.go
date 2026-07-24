package completion

import "context"

const completionItemLimit = 200

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

func limitCompletionItems(items []Item) []Item {
	if len(items) <= completionItemLimit {
		return items
	}
	return items[:completionItemLimit]
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
	PriorityAlias      Priority = 125 // User-defined functions/aliases (before executables)
	PriorityEnv        Priority = 130 // Environment variables ($VAR)
	PriorityExecutable Priority = 150 // Executable names from PATH (command position only)
	PriorityVCS        Priority = 175 // Context-aware VCS args (git/jj refs)
	PriorityPlugin     Priority = 180 // Declarative completion plugins (user-extensible)
	PrioritySemantic   Priority = 185 // Semantic completions for common commands
	PriorityFilesystem Priority = 200 // Fallback for file arguments
	PriorityAgent      Priority = 300 // AI-powered fallback
)

// Kind represents the type of completion item for icon display.
type Kind string

const (
	KindFile       Kind = "file"
	KindDirectory  Kind = "directory"
	KindExecutable Kind = "executable"
	KindAlias      Kind = "alias" // User-defined functions and aliases (ƒ icon)
	KindEnv        Kind = "env"   // Environment variables ($ icon)
)
