package executor

import (
	"context"
	"io"
	"testing"
)

// The keys a line's PTY command left unread wait for the next prompt
// across later lines that run no PTY command (a hook, an init command),
// and the prompt takes them once.
func TestTakeUnread_KeysWaitAcrossLinesAndAreTakenOnce(t *testing.T) {
	t.Parallel()
	e, _ := runTypingOn(t, `sleep 0.8`, "echo A\recho B")
	if _, err := e.Execute(context.Background(), "true", io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	if got := string(e.TakeUnread()); got != "echo A\necho B" {
		t.Errorf("unread %q, want every key the sleep left", got)
	}
	if got := e.TakeUnread(); got != nil {
		t.Errorf("unread %q taken twice", got)
	}
}
