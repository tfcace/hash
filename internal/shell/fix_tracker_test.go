package shell

import (
	"bytes"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tfcace/hash/internal/executor"
	"github.com/tfcace/hash/internal/learning"
)

func newTestFixStore(t *testing.T) *learning.FixStore {
	t.Helper()
	store, err := learning.NewFixStore(filepath.Join(t.TempDir(), "learning.db"))
	if err != nil {
		t.Fatalf("NewFixStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestFixTracker_LearnsFixFromNextSuccessfulCommand(t *testing.T) {
	store := newTestFixStore(t)
	tr := newFixTracker(store)

	if _, found := tr.Observe("./deploy.sh", "permission denied", 126); found {
		t.Fatal("empty store should produce no suggestion")
	}

	// A different command succeeding right after the failure is the fix.
	tr.Observe("chmod +x deploy.sh", "", 0)

	fix, found := tr.Observe("./deploy.sh", "permission denied", 126)
	if !found {
		t.Fatal("expected a learned fix after observing the successful fix command")
	}
	if fix.Fix != "chmod +x deploy.sh" {
		t.Errorf("suggested fix = %q, want %q", fix.Fix, "chmod +x deploy.sh")
	}
}

func TestFixTracker_DoesNotLearnFromRetry(t *testing.T) {
	store := newTestFixStore(t)
	tr := newFixTracker(store)

	tr.Observe("make build", "undefined: Foo", 2)
	tr.Observe("make build", "", 0) // same command retried, not a fix

	count, err := store.PatternCount()
	if err != nil {
		t.Fatalf("PatternCount() error = %v", err)
	}
	if count != 0 {
		t.Errorf("retrying the same command should not record a fix, got %d patterns", count)
	}
}

func TestFixTracker_DoesNotLearnWithoutPriorFailure(t *testing.T) {
	store := newTestFixStore(t)
	tr := newFixTracker(store)

	tr.Observe("ls", "", 0)

	count, err := store.PatternCount()
	if err != nil {
		t.Fatalf("PatternCount() error = %v", err)
	}
	if count != 0 {
		t.Errorf("success without prior failure should record nothing, got %d patterns", count)
	}
}

func TestFixTracker_RecordsFailureWhenSuggestedFixFails(t *testing.T) {
	store := newTestFixStore(t)
	pattern := learning.ExtractPattern("./deploy.sh", "permission denied", 126)
	if err := store.RecordFix(pattern, "chmod +x deploy.sh", true); err != nil {
		t.Fatalf("RecordFix() error = %v", err)
	}

	tr := newFixTracker(store)
	if _, found := tr.Observe("./deploy.sh", "permission denied", 126); !found {
		t.Fatal("expected suggestion from prerecorded fix")
	}

	// The suggested fix itself fails: that must count against it.
	tr.Observe("chmod +x deploy.sh", "chmod: deploy.sh: Operation not permitted", 1)

	fix, found := store.GetFix(pattern)
	if !found {
		t.Fatal("fix should still exist in store")
	}
	if fix.FailureCount != 1 {
		t.Errorf("FailureCount = %d, want 1 after suggested fix failed", fix.FailureCount)
	}
}

func TestFixTracker_SuggestedFixLifecycle(t *testing.T) {
	store := newTestFixStore(t)
	pattern := learning.ExtractPattern("./deploy.sh", "permission denied", 126)
	if err := store.RecordFix(pattern, "chmod +x deploy.sh", true); err != nil {
		t.Fatalf("RecordFix() error = %v", err)
	}

	tr := newFixTracker(store)
	if got := tr.SuggestedFix(); got != "" {
		t.Errorf("SuggestedFix() before any failure = %q, want empty", got)
	}

	tr.Observe("./deploy.sh", "permission denied", 126)
	if got := tr.SuggestedFix(); got != "chmod +x deploy.sh" {
		t.Errorf("SuggestedFix() after failure = %q, want %q", got, "chmod +x deploy.sh")
	}

	tr.Observe("chmod +x deploy.sh", "", 0)
	if got := tr.SuggestedFix(); got != "" {
		t.Errorf("SuggestedFix() after success = %q, want empty", got)
	}
}

func TestShell_LearnedFixShownAndGhostedAfterFailure(t *testing.T) {
	store := newTestFixStore(t)
	pattern := learning.ExtractPattern("./deploy.sh", "permission denied", 126)
	if err := store.RecordFix(pattern, "chmod +x deploy.sh", true); err != nil {
		t.Fatalf("RecordFix() error = %v", err)
	}

	var banner bytes.Buffer
	s := &Shell{
		fixes:  newFixTracker(store),
		errors: &ErrorHandler{out: &banner},
	}

	cap1 := newStderrCapture(io.Discard)
	_, _ = cap1.Write([]byte("permission denied"))
	s.handleExecutionResult("./deploy.sh", &executor.Result{ExitCode: 126}, nil, cap1)

	if !strings.Contains(banner.String(), "chmod +x deploy.sh") {
		t.Errorf("expected learned-fix banner after failure, got: %q", banner.String())
	}
	if got := s.promptGhost(); got != "chmod +x deploy.sh" {
		t.Errorf("promptGhost() = %q, want the learned fix", got)
	}

	// Accepting the fix and succeeding clears the ghost and reinforces the fix.
	cap2 := newStderrCapture(io.Discard)
	s.handleExecutionResult("chmod +x deploy.sh", &executor.Result{ExitCode: 0}, nil, cap2)

	if got := s.promptGhost(); got != "" {
		t.Errorf("promptGhost() after success = %q, want empty", got)
	}
	fix, found := store.GetFix(pattern)
	if !found {
		t.Fatal("fix should exist in store")
	}
	if fix.SuccessCount != 2 {
		t.Errorf("SuccessCount = %d, want 2 (prerecorded + reinforced)", fix.SuccessCount)
	}
}

func TestShell_LowConfidenceFixShownTentatively(t *testing.T) {
	store := newTestFixStore(t)
	pattern := learning.ExtractPattern("./deploy.sh", "permission denied", 126)
	// 1 success + 1 failure scores in the 0.5-0.69 band: shown, but tentatively.
	if err := store.RecordFix(pattern, "chmod +x deploy.sh", true); err != nil {
		t.Fatalf("RecordFix() error = %v", err)
	}
	if err := store.RecordFix(pattern, "chmod +x deploy.sh", false); err != nil {
		t.Fatalf("RecordFix() error = %v", err)
	}

	var banner bytes.Buffer
	s := &Shell{
		fixes:  newFixTracker(store),
		errors: &ErrorHandler{out: &banner},
	}

	cap1 := newStderrCapture(io.Discard)
	_, _ = cap1.Write([]byte("permission denied"))
	s.handleExecutionResult("./deploy.sh", &executor.Result{ExitCode: 126}, nil, cap1)

	out := banner.String()
	if !strings.Contains(out, "chmod +x deploy.sh") {
		t.Fatalf("expected low-confidence fix banner, got: %q", out)
	}
	if !strings.Contains(out, "tried") {
		t.Errorf("low-confidence banner should show attempt count, got: %q", out)
	}
}

func TestErrorHandler_ShowLearnedFixAdvertisesRealKeys(t *testing.T) {
	var buf bytes.Buffer
	h := &ErrorHandler{out: &buf}

	h.showLearnedFix(learning.Fix{Fix: "chmod +x deploy.sh", SuccessCount: 3}, true)
	out := buf.String()

	if strings.Contains(out, "[Enter: run]") || strings.Contains(out, "[Tab: edit]") {
		t.Errorf("banner advertises key bindings that do not exist: %q", out)
	}
	if !strings.Contains(out, "accept") || !strings.Contains(out, "esc") {
		t.Errorf("banner should explain the ghost-text keys (accept/esc), got: %q", out)
	}
}

func TestShell_PromptGhostIsBareForLearnedFix(t *testing.T) {
	// The banner above the prompt already teaches the keys; the input line
	// carries only the suggested command, fish-style.
	store := newTestFixStore(t)
	pattern := learning.ExtractPattern("./deploy.sh", "permission denied", 126)
	if err := store.RecordFix(pattern, "chmod +x deploy.sh", true); err != nil {
		t.Fatalf("RecordFix() error = %v", err)
	}

	s := &Shell{
		fixes:  newFixTracker(store),
		errors: &ErrorHandler{out: io.Discard},
	}
	cap1 := newStderrCapture(io.Discard)
	_, _ = cap1.Write([]byte("permission denied"))
	s.handleExecutionResult("./deploy.sh", &executor.Result{ExitCode: 126}, nil, cap1)

	if text := s.promptGhost(); text != "chmod +x deploy.sh" {
		t.Fatalf("promptGhost() = %q, want the learned fix", text)
	}
}

func TestShell_PromptGhostEmptyWithNothingPending(t *testing.T) {
	s := &Shell{fixes: newFixTracker(nil)}
	if text := s.promptGhost(); text != "" {
		t.Errorf("promptGhost() with nothing pending = %q, want empty", text)
	}
}
