package shell

import (
	"context"
	"syscall"
	"testing"

	"github.com/tfcace/hash/internal/agent"
	"github.com/tfcace/hash/internal/history"
)

// historyTurnShell is a turn shell with a history, and the user answering
// hints with answers.
func historyTurnShell(t *testing.T, tr agent.Transport, answers ...string) (*Shell, *history.Store) {
	t.Helper()
	sh, _, _ := newTurnShell(t, tr, answers...)
	store, err := history.NewStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	sh.history = store
	return sh, store
}

// A ?? line goes into history with the status it ended with, the one the
// prompt and $? show: 1 when the turn failed. It was recorded with 0
// before the turn ran, whatever came of it.
func TestHistory_AgentLineHasTheStatusItEndedWith(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		script turnScript
		want   int
	}{
		"answered": {turnScript{events: []agent.StreamEvent{textEvent("Files are things on disk.")}}, 0},
		"failed":   {turnScript{err: errFlaky}, 1},
	} {
		t.Run(name, func(t *testing.T) {
			sh, store := historyTurnShell(t, &scriptedTransport{scripts: []turnScript{tc.script}})
			captureStdout(t, sh, func() { _ = sh.dispatchCommand(context.Background(), "?? what are files") })
			rows, err := store.GetRecent(10)
			if err != nil || len(rows) != 1 {
				t.Fatalf("history = %+v, %v; want the ?? line", rows, err)
			}
			if rows[0].ExitCode != tc.want || sh.last.exitCode != tc.want {
				t.Errorf("history has exit %d, the line ended %d; want %d", rows[0].ExitCode, sh.last.exitCode, tc.want)
			}
		})
	}
}

// The ?? line stays before the command its turn ran: Up recalls the
// command first, then the request that produced it.
func TestHistory_AgentLineComesBeforeTheCommandItRan(t *testing.T) {
	t.Parallel()
	tr := &scriptedTransport{scripts: []turnScript{{events: []agent.StreamEvent{textEvent("```bash\necho ran\n```")}}}}
	sh, store := historyTurnShell(t, tr, keyRun)
	captureStdout(t, sh, func() { _ = sh.dispatchCommand(context.Background(), "?? say ran") })
	rows, err := store.Search(history.SearchOptions{Limit: historyNavLimit})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range rows {
		got = append(got, r.Command)
	}
	if len(got) != 2 || got[0] != "echo ran" || got[1] != "?? say ran" {
		t.Fatalf("Up recalls %q, want the command, then the ?? line", got)
	}
	if rows[1].ExitCode != 0 {
		t.Errorf("the ?? line that ran a command has exit %d, want 0", rows[1].ExitCode)
	}
}

// shellSignal is a shell context's cause once a fatal signal ended it, as
// cmd/hash's signalContext sets it.
type shellSignal syscall.Signal

func (s shellSignal) Error() string          { return syscall.Signal(s).String() }
func (s shellSignal) Signal() syscall.Signal { return syscall.Signal(s) }

// A ?? line the shell's ending cut off mid-stream (the terminal closed,
// a kill) keeps what the user typed in history, with the status of a line
// a signal ended, 128+signal: the agent's work neither failed nor finished.
// It was recorded with 1, as if the request had failed.
func TestHistory_AgentLineTheShellEndedHasTheSignalsStatus(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		cause error
		want  int
	}{
		{shellSignal(syscall.SIGHUP), 128 + int(syscall.SIGHUP)},
		{shellSignal(syscall.SIGTERM), 128 + int(syscall.SIGTERM)},
		{context.Canceled, 128 + int(syscall.SIGTERM)}, // no signal named: as the executor stops a command
	} {
		shellCtx, endShell := context.WithCancelCause(context.Background())
		tr := &scriptedTransport{scripts: []turnScript{{
			events:  []agent.StreamEvent{textEvent("Files are")},
			during:  func(context.Context) { endShell(tc.cause) },
			waitCtx: true,
		}}}
		sh, store := historyTurnShell(t, tr)
		captureStdout(t, sh, func() { _ = sh.dispatchCommand(shellCtx, "?? what are files") })
		rows, err := store.GetRecent(10)
		if err != nil || len(rows) != 1 || rows[0].Command != "?? what are files" {
			t.Fatalf("%v: history = %+v, %v; want the ?? line", tc.cause, rows, err)
		}
		if rows[0].ExitCode != tc.want || sh.last.exitCode != tc.want {
			t.Errorf("%v: history has exit %d, the line ended %d; want %d", tc.cause, rows[0].ExitCode, sh.last.exitCode, tc.want)
		}
	}
}
