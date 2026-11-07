package shell

import (
	"context"
	"testing"
	"time"

	"github.com/tfcace/hash/internal/agent"
)

// Keys typed while a follow-up streams are the next prompt's, as at a
// hint: when the follow-up asks back, the reply box does not open on them.
// It used to, and their Enter sent the typed-ahead line to the agent as
// the next reply.
func TestConversation_KeysTypedDuringAFollowUpAreNotTheNextReply(t *testing.T) {
	t.Parallel()
	var tt *testTerminal
	tr := &scriptedTransport{scripts: []turnScript{
		{events: []agent.StreamEvent{textEvent("Which directory?")}},
		{
			events: []agent.StreamEvent{textEvent("Found two. ")},
			during: func(context.Context) { tt.typeKeys("echo TYPED\r") },
			hold:   200 * time.Millisecond, // the watcher reads the keys
			after:  []agent.StreamEvent{textEvent("Which one?")},
		},
		{events: []agent.StreamEvent{textEvent("never asked")}},
	}}
	var sh *Shell
	sh, _, _, tt = newTurnShellOn(t, tr, keyReply+"internal/shell\r")

	var err error
	captureStdout(t, sh, func() { err = ask(t, sh, "?? find my config") })
	if err != nil {
		t.Fatal(err)
	}
	if got := tr.requestCount(); got != 2 {
		t.Errorf("requests = %d, want the turn and its follow-up: the typed line went to the agent", got)
	}
	if line := nextLine(t, sh); line != "echo TYPED" {
		t.Errorf("the next prompt got %q, want the line typed during the follow-up", line)
	}
}
