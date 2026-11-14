package shell

import (
	"testing"

	"github.com/tfcace/hash/internal/learning"
)

// An interrupted command (Ctrl+C: exit 130) did not fail; it was stopped.
// Learning must not remember it as a failure whose "fix" is whatever
// related command runs next.
func TestFixTracker_InterruptIsNotAFailure(t *testing.T) {
	store := newTestFixStore(t)
	tr := newFixTracker(store)

	tr.Observe("make build", "", 130) // the user hit Ctrl+C
	tr.Observe("make build -j1", "", 0)

	if _, found := store.GetFix(learning.ExtractPattern("make build", "", 130)); found {
		t.Error("an interrupted command was learned as a failure with a fix")
	}
}

// An interrupt between a failure and its fix leaves the failure pending.
func TestFixTracker_InterruptKeepsThePendingFailure(t *testing.T) {
	store := newTestFixStore(t)
	tr := newFixTracker(store)

	tr.Observe("./deploy.sh", "permission denied", 126)
	tr.Observe("sleep 100", "", 130)
	tr.Observe("chmod +x deploy.sh", "", 0)

	if _, found := store.GetFix(learning.ExtractPattern("./deploy.sh", "permission denied", 126)); !found {
		t.Error("the fix after an interrupted command was not learned")
	}
}

// Decision: the line after a failure resolves it, builtins included. A cd
// is the user moving on; a fix learned across it would be a guess.
func TestFixTracker_AnyNextCommandResolvesTheFailure(t *testing.T) {
	store := newTestFixStore(t)
	tr := newFixTracker(store)

	tr.Observe("./deploy.sh", "permission denied", 126)
	tr.Observe("cd scripts", "", 0)
	tr.Observe("chmod +x deploy.sh", "", 0)

	if _, found := store.GetFix(learning.ExtractPattern("./deploy.sh", "permission denied", 126)); found {
		t.Error("a fix was learned across an intervening command")
	}
}

// A tentative fix (score under the ghost bar) shows in the banner but is
// never pre-typed; once its prompt is answered, nothing is offered either.
// Both used to be the same ghost=false, so the tracker could not tell a
// fix it never offered from one it retired.
func TestPromptOffer_TentativeFixIsNotOffered(t *testing.T) {
	store := newTestFixStore(t)
	pattern := learning.ExtractPattern("./deploy.sh", "permission denied", 126)
	recordSuccesses(t, store, pattern, "chmod +x deploy.sh", 1) // tentative

	s := &Shell{fixes: newFixTracker(store), errors: &ErrorHandler{out: &lockedBuffer{}}}
	s.last = commandOutcome{command: "./deploy.sh", stderr: "permission denied", exitCode: 126}
	s.observeCommandOutcome("./deploy.sh")
	if got := s.promptGhost(); got != "" {
		t.Errorf("tentative fix offered as ghost text %q", got)
	}
}
