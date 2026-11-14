package shell

import (
	"os"

	"github.com/tfcace/hash/internal/learning"
)

// highConfidenceScore is the score at which a learned fix is presented as
// the answer (→, ghost text) rather than a possibility (?, banner only).
const highConfidenceScore = 0.7

// pendingFailure remembers the last failed command until the next command
// resolves it (a different success is recorded as its fix).
type pendingFailure struct {
	pattern   learning.Pattern
	command   string
	suggested string // fix offered for this failure, if any: judged when it runs
}

// fixTracker observes command outcomes to drive the learning loop: failures
// remember the error pattern, the next different successful command is
// recorded as its fix, and known patterns surface learned suggestions. What
// the prompt offers is the shell's (promptOffer), not the tracker's.
type fixTracker struct {
	store   *learning.FixStore
	pending *pendingFailure
}

// newFixTracker creates a tracker backed by the given fix store.
func newFixTracker(store *learning.FixStore) *fixTracker {
	return &fixTracker{store: store}
}

// interrupted reports whether exitCode says the user stopped the command
// (SIGINT, SIGQUIT, SIGTERM) rather than that it failed.
func interrupted(exitCode int) bool {
	switch exitCode {
	case 128 + 2, 128 + 3, 128 + 15:
		return true
	}
	return false
}

// Observe processes a finished command. For failures it returns the learned
// fix to suggest, if one exists with a usable score. A command the user
// interrupted neither fails nor resolves anything: it is ignored.
func (t *fixTracker) Observe(command, stderr string, exitCode int) (learning.Fix, bool) {
	if t == nil || t.store == nil || interrupted(exitCode) {
		return learning.Fix{}, false
	}

	if exitCode == 0 {
		if p := t.pending; p != nil && command != p.command && learning.Related(p.command, command) {
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

	fix, found := t.store.GetRelatedFix(pattern, command)
	if !found || fix.Score < 0.5 {
		return learning.Fix{}, false
	}
	t.pending.suggested = fix.Fix
	return fix, true
}

// active reports whether the tracker can offer suggestions at the prompt.
func (t *fixTracker) active() bool {
	return t != nil && t.store != nil
}

// SetSuggested replaces the suggestion for the pending failure, so outcome
// tracking follows a suggestion made outside the store (e.g. a did-you-mean
// correction).
func (t *fixTracker) SetSuggested(fix string) {
	if t == nil || t.pending == nil {
		return
	}
	t.pending.suggested = fix
}

// offerKind is what the next prompt offers as ghost text.
type offerKind int

const (
	offerNone   offerKind = iota // nothing: the predictor's guess, if any
	offerFix                     // a fix for the line that just failed
	offerUpdate                  // model update, for a newer adapter
)

// promptOffer is the one ghost-text offer the next prompt makes. It is set
// between prompts (a failed line's fix, an update notice) and dropped once
// that prompt is answered: an offer the user typed past is not made again.
type promptOffer struct {
	kind offerKind
	line string
}

// offerFix makes fix the next prompt's offer.
func (s *Shell) offerFix(fix string) {
	s.offer = promptOffer{kind: offerFix, line: fix}
}

// observeCommandOutcome feeds a finished command into the learning loop and
// shows the best suggestion for a failure: a deterministic did-you-mean
// correction when one exists, else the learned fix from the store. Only a
// fix that has earned confidence is offered as ghost text: one keystroke
// accepts it.
func (s *Shell) observeCommandOutcome(line string) {
	typoFix := s.last.typoFix
	s.offer = promptOffer{} // this line's outcome decides the next prompt's offer
	if s.fixes == nil {
		return
	}
	fix, found := s.fixes.Observe(line, s.last.stderr, s.last.exitCode)

	h := s.errors
	if h == nil {
		h = NewErrorHandler()
	}

	// A correction derived from the current repo state beats replaying a
	// learned command whose arguments came from a different context.
	if s.last.exitCode != 0 && !interrupted(s.last.exitCode) {
		lister := s.branchLister
		if lister == nil {
			lister = gitBranches
		}
		if suggestion := gitDidYouMean(line, s.last.stderr, lister); suggestion != "" {
			s.fixes.SetSuggested(suggestion)
			s.offerFix(suggestion)
			h.showDidYouMean(suggestion)
			return
		}
		// The command-not-found banners already named the corrections; the
		// corrected line is what the prompt offers.
		if typoFix != "" {
			s.fixes.SetSuggested(typoFix)
			s.offerFix(typoFix)
			return
		}
	}

	if !found {
		return
	}
	confident := fix.Score >= highConfidenceScore
	if confident {
		s.offerFix(fix.Fix)
	}
	h.showLearnedFix(fix, confident)
}

// retirePromptGhosts drops the prompt's offer once that prompt has been
// answered: a fix or update the user typed past is not offered again.
func (s *Shell) retirePromptGhosts() {
	s.offer = promptOffer{}
}

// promptGhost returns the ghost text for the next prompt: its offer (a fix
// for the last failure, or an adapter update), else the command predictor's
// guess. Either way the ghost renders bare (fish-style); the keys are taught
// by the banner above the prompt, not on the input line.
func (s *Shell) promptGhost() string {
	if s.offer.kind != offerNone {
		return s.offer.line
	}
	if last := s.last.shellCommand(); s.predictor != nil && last != "" {
		cwd, _ := os.Getwd()
		return s.predictor.PredictCommand(last, cwd)
	}
	return ""
}
