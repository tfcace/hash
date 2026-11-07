package shell

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tfcace/hash/internal/compat"
)

// The first-run migration question is a question like any other: hash
// ending (SIGTERM) or the user pressing Ctrl+C ends it at once, and it is
// asked again on the next launch. It read the terminal byte by byte past
// the shell's keyboard, cooked, so neither reached it: it waited for a line.
func TestStartup_MigrationQuestionEndsOnCtrlCOrTermination(t *testing.T) {
	for _, tc := range []struct {
		name string
		end  func(cancel context.CancelFunc, typing *os.File)
	}{
		{"terminated", func(cancel context.CancelFunc, _ *os.File) { cancel() }},
		{"Ctrl+C", func(_ context.CancelFunc, typing *os.File) { _, _ = typing.WriteString("\x03") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sh, _, statePath := migrationShell(t, "/bin/bash", map[string]string{".bashrc": "alias ll='ls -la'\n"})
			term := newTestTerminal(t, sh)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan struct{})
			go func() {
				defer close(done)
				captureStdout(t, sh, func() { _ = sh.runStartup(ctx) })
			}()
			time.Sleep(100 * time.Millisecond) // the question is up
			tc.end(cancel, term.keys)
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				_, _ = term.keys.WriteString("n\r") // unblock the old read
				<-done
				t.Fatal("the migration question outlived it")
			}
			state, err := compat.ReadMigration(statePath)
			if err != nil || state.Status != compat.StatusFirstRun {
				t.Errorf("state = %+v, %v; want the offer kept for the next launch", state, err)
			}
		})
	}
}

// The question reads its answer through the keyboard, and keys typed
// before it appeared never answer it: its default (Enter) imports the
// user's config. They are the first prompt's, as at any question. It used
// to read typeahead first, so an Enter pressed while hash started imported.
func TestStartup_MigrationQuestionIgnoresTypeahead(t *testing.T) {
	for _, ahead := range []string{"\r", "echo first\r"} {
		t.Run(ahead, func(t *testing.T) { migrationWithTypeahead(t, ahead) })
	}
}

func migrationWithTypeahead(t *testing.T, ahead string) {
	sh, _, statePath := migrationShell(t, "/bin/bash", map[string]string{".bashrc": "alias ll='ls -la'\n"})
	term := newTestTerminal(t, sh)
	term.typeKeys(ahead)
	time.Sleep(20 * time.Millisecond)
	done := make(chan struct{})
	go func() {
		defer close(done)
		captureStdout(t, sh, func() { _ = sh.runStartup(questionCtx(t)) })
	}()
	if term.waitAsking(done) {
		term.typeKeys("n\r")
	}
	<-done
	if state, err := compat.ReadMigration(statePath); err != nil || state.Status != compat.StatusDeclined {
		t.Errorf("state = %+v, %v; want declined by the answer typed at the question", state, err)
	}
	if line := nextLine(t, sh); line != strings.TrimSuffix(ahead, "\r") {
		t.Errorf("the first prompt got %q, want the line typed before the question", line)
	}
}

// A quick "n⏎" typed the moment the migration question appears is
// dropped, both keys: the question says so and keeps waiting (until
// Ctrl+C here), nothing is imported or declined, and the first prompt holds only the line typed
// before the question, without an Enter. The burst rule this replaces
// sent the n⏎ on to the first prompt, which ran `echo hin`.
func TestStartup_MigrationQuestionDropsAQuickAnswer(t *testing.T) {
	sh, _, statePath := migrationShell(t, "/bin/bash", map[string]string{".bashrc": "alias ll='ls -la'\n"})
	term := newTestTerminal(t, sh)
	clock := typingAt(sh.editorCfg.Input)
	term.typeKeys("echo hi")
	time.Sleep(20 * time.Millisecond)
	done := make(chan string, 1)
	go func() {
		done <- captureStdout(t, sh, func() { _ = sh.runStartup(questionCtx(t)) })
	}()
	clock.waitReadings(t, int64(len("echo hi"))+1) // set aside, then the question drawn
	term.typeKeys("n\r")
	term.typeKeys("\x03") // then the user ends it: Ctrl+C acts at once
	out := <-done
	if !strings.Contains(out, lineNote) {
		t.Errorf("the question drew %q, want its note %q", out, lineNote)
	}
	if state, err := compat.ReadMigration(statePath); err != nil || state.Status != compat.StatusFirstRun {
		t.Errorf("state = %+v, %v; want nothing answered: the offer kept for the next launch", state, err)
	}
	if rest := sh.editorCfg.Input.Typeahead(); !slices.Equal(rest, keysOf(t, "echo hi")) {
		t.Errorf("the first prompt gets %+v, want only the typeahead \"echo hi\"", rest)
	}
}
