package shell

import (
	"os"

	"github.com/tfcace/hash/internal/editor"
	"github.com/tfcace/hash/internal/learning"
)

// pendingFailure remembers the last failed command until the next command
// resolves it (a different success is recorded as its fix).
type pendingFailure struct {
	pattern   learning.Pattern
	command   string
	suggested string // learned fix offered for this failure, if any
}

// fixTracker observes command outcomes to drive the learning loop: failures
// remember the error pattern, the next different successful command is
// recorded as its fix, and known patterns surface learned suggestions.
type fixTracker struct {
	store   *learning.FixStore
	pending *pendingFailure
}

// newFixTracker creates a tracker backed by the given fix store.
func newFixTracker(store *learning.FixStore) *fixTracker {
	return &fixTracker{store: store}
}

// Observe processes a finished command. For failures it returns the learned
// fix to suggest, if one exists with a usable score.
func (t *fixTracker) Observe(command, stderr string, exitCode int) (learning.Fix, bool) {
	if t == nil || t.store == nil {
		return learning.Fix{}, false
	}

	if exitCode == 0 {
		if p := t.pending; p != nil && command != p.command {
			_ = t.store.RecordFix(p.pattern, command, true)
		}
		t.pending = nil
		return learning.Fix{}, false
	}

	// The fix we suggested was run and still failed: count that against it.
	if p := t.pending; p != nil && p.suggested != "" && command == p.suggested {
		_ = t.store.RecordFix(p.pattern, command, false)
	}

	pattern := learning.ExtractPattern(command, stderr, exitCode)
	t.pending = &pendingFailure{pattern: pattern, command: command}

	fix, found := t.store.GetFix(pattern)
	if !found || fix.Score < 0.5 {
		return learning.Fix{}, false
	}
	t.pending.suggested = fix.Fix
	return fix, true
}

// SuggestedFix returns the fix suggested for the pending failure, or "".
func (t *fixTracker) SuggestedFix() string {
	if t == nil || t.pending == nil {
		return ""
	}
	return t.pending.suggested
}

// SetSuggested replaces the suggestion for the pending failure, so ghost
// text and outcome tracking follow a suggestion made outside the store
// (e.g. a did-you-mean correction).
func (t *fixTracker) SetSuggested(fix string) {
	if t == nil || t.pending == nil {
		return
	}
	t.pending.suggested = fix
}

// observeCommandOutcome feeds a finished command into the learning loop and
// shows the best suggestion for a failure: a deterministic did-you-mean
// correction when one exists, else the learned fix from the store.
func (s *Shell) observeCommandOutcome(line string) {
	if s.fixes == nil {
		return
	}
	fix, found := s.fixes.Observe(line, s.lastStderr, s.lastExitCode)

	h := s.errors
	if h == nil {
		h = NewErrorHandler()
	}

	// A correction derived from the current repo state beats replaying a
	// learned command whose arguments came from a different context.
	if s.lastExitCode != 0 {
		lister := s.branchLister
		if lister == nil {
			lister = gitBranches
		}
		if suggestion := gitDidYouMean(line, s.lastStderr, lister); suggestion != "" {
			s.fixes.SetSuggested(suggestion)
			h.showDidYouMean(suggestion)
			return
		}
	}

	if !found {
		return
	}
	h.showLearnedFix(fix, fix.Score >= 0.7)
}

// promptGhost returns the ghost text for the next prompt: a learned fix for
// the last failure if available, otherwise the command predictor's guess.
// Either way the ghost renders bare (fish-style); the keys are taught by the
// banner above the prompt, not on the input line.
func (s *Shell) promptGhost() string {
	text, _ := s.promptGhostOwned()
	return text
}

func (s *Shell) promptGhostOwned() (string, editor.GhostSource) {
	if fix := s.fixes.SuggestedFix(); fix != "" {
		return fix, editor.GhostSourceLearnedFix
	}
	if s.predictor != nil && s.lastCommand != "" {
		cwd, _ := os.Getwd()
		if predicted := s.predictor.PredictCommand(s.lastCommand, cwd); predicted != "" {
			return predicted, editor.GhostSourcePrediction
		}
	}
	return "", editor.GhostSourceNone
}
