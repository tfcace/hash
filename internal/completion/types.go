package completion

import "context"

const completionItemLimit = 200

// Item represents a single completion suggestion.
type Item struct {
	Value       string // The text the word should mean; the router writes it as shell text
	Display     string // What to show in the menu (may differ from Value)
	Description string // Optional description
	Icon        string // Nerd Font icon (optional)
	Score       int    // For fuzzy sorting (higher = better match)
}

// Outcome is what a completer's result means for the input it was asked
// about.
type Outcome int

const (
	// OutcomeNotMine: the completer does not handle this input; the router
	// tries the next one.
	OutcomeNotMine Outcome = iota
	// OutcomeAnswer: Items holds the completions.
	OutcomeAnswer
	// OutcomePending: the completer owns the input but its data has not
	// arrived yet. Callers say so rather than falling through to an
	// unrelated completer, and retry when the ready notification fires.
	OutcomePending
	// OutcomeNoMatches: the completer owns the input and nothing matches.
	// Falling through would answer with something unrelated, like filenames
	// for a container name.
	OutcomeNoMatches
	// OutcomeWithheld: the completer answered, but no item could be
	// written safely where it goes (the read-back check refused each).
	OutcomeWithheld
)

func (o Outcome) String() string {
	switch o {
	case OutcomeNotMine:
		return "not_mine"
	case OutcomeAnswer:
		return "answer"
	case OutcomePending:
		return "pending"
	case OutcomeNoMatches:
		return "no_matches"
	case OutcomeWithheld:
		return "withheld"
	default:
		return "unknown"
	}
}

// Result holds completion results from a completer. A result with Items is
// an answer; an empty one means "not mine" unless it was built by
// pendingResult or ownedResult.
type Result struct {
	Items []Item // List of completions
	// Prefix is text every item shares, before each Value: the directory
	// of the path being completed. The router folds it into the values it
	// writes as shell text.
	Prefix string
	// ReplaceFrom is the byte offset in the line where the text the
	// completion replaces starts: Prefix+Value replaces line[ReplaceFrom:pos].
	// It may be on an earlier row than the cursor. A completer leaves it 0
	// to replace the whole word being completed; the router never lets it
	// start before that word, so an operator glued to the word ("ls|gr")
	// or the head of an assignment ("PATH=$HO") the item does not hold is
	// kept.
	ReplaceFrom int
	// refs, when set, says the values are shell text already, written as
	// they are: references to the variables their Displays name (env's
	// $NAME), not text for the router to escape.
	refs *variableRefs
	// empty says what an item-less result means. Unexported so only this
	// package can claim an input without answering it, and only through the
	// constructors below.
	empty Outcome
	// hidden is what the router left out of the answer's items.
	hidden Hidden
}

// Hidden is what an answer leaves out of its items: they are not all the
// matches, so a lone item is not the only one.
type Hidden struct {
	// Withheld are matches hash could not write safely where they go.
	Withheld int
	// More are the matches past the menu's limit, or not checked before
	// the deadline, as far as they were counted.
	More int
	// Uncounted: a completer stopped listing at its limit, so more than
	// More were left out, how many unknown.
	Uncounted bool
}

// Any reports whether anything was left out.
func (h Hidden) Any() bool { return h.Withheld > 0 || h.NotShown() }

// NotShown reports whether matches past the menu's limit, or not checked
// in time, were left out.
func (h Hidden) NotShown() bool { return h.More > 0 || h.Uncounted }

// Hidden is what the router left out of the result's items.
func (r Result) Hidden() Hidden { return r.hidden }

// variableRefs are values that reference a variable: the sigil, "$" (or
// nothing inside ${ }), then the name.
type variableRefs struct{ sigil string }

// Outcome reports what the result means.
func (r Result) Outcome() Outcome {
	if len(r.Items) > 0 {
		return OutcomeAnswer
	}
	return r.empty
}

// pendingResult claims the input while its data is still loading.
func pendingResult() Result { return Result{empty: OutcomePending} }

// ownedResult is an authoritative answer: when items is empty the input is
// still claimed, as "no matches".
func ownedResult(items []Item) Result {
	return Result{Items: items, empty: OutcomeNoMatches}
}

// add adds item to the result's items, or reports false when they are at
// the menu's limit already, marking that more were not shown.
func (r *Result) add(item Item) bool {
	if len(r.Items) == completionItemLimit {
		r.hidden.Uncounted = true
		return false
	}
	r.Items = append(r.Items, item)
	return true
}

// limitCompletionItems keeps a menu's first completionItemLimit items,
// reporting how many it cut.
func limitCompletionItems(items []Item) (kept []Item, cut int) {
	if len(items) <= completionItemLimit {
		return items, 0
	}
	return items[:completionItemLimit], len(items) - completionItemLimit
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
	// Plugins outrank tool-native completion on purpose: a matched rule is
	// curated (descriptions, per-subcommand sources) where Cobra __complete
	// output is bare names, and which one answered would otherwise depend on
	// whose prefetch cache happened to be warm. Plugins only claim arguments
	// an explicit rule matches, so unmatched input still falls through.
	PriorityPlugin     Priority = 90  // Declarative completion plugins (user-extensible)
	PriorityToolNative Priority = 100 // Tool-native subcommand completion (Cobra __complete)
	PriorityEnv        Priority = 130 // Environment variables ($VAR)
	PriorityExecutable Priority = 150 // Executable names from PATH (command position only)
	PriorityVCS        Priority = 175 // Context-aware VCS args (git/jj refs)
	PrioritySemantic   Priority = 185 // Semantic completions for common commands
	PriorityFilesystem Priority = 200 // Fallback for file arguments
)
