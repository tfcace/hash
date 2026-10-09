package executor

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/tfcace/hash/internal/programstatus"
)

func newStatusExecutor(t *testing.T, threshold time.Duration) (*Executor, *bytes.Buffer) {
	t.Helper()
	e := New()
	e.SetProgressThreshold(threshold)
	var buf bytes.Buffer
	e.status = programstatus.NewReporter(&buf)
	return e, &buf
}

func cmdReport(state programstatus.State, app, msg string) string {
	return programstatus.Sequence(programstatus.Report{State: state, ID: "cmd", App: app, Msg: msg})
}

func TestExecute_LongCommandReportsWorkingThenDone(t *testing.T) {
	e, buf := newStatusExecutor(t, 10*time.Millisecond)
	if _, err := e.Execute(context.Background(), "sleep 0.2", io.Discard, io.Discard); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	want := cmdReport(programstatus.Working, "sleep", "sleep 0.2") + cmdReport(programstatus.Done, "sleep", "sleep 0.2")
	if buf.String() != want {
		t.Errorf("wrote %q\nwant  %q", buf.String(), want)
	}
}

func TestExecute_LongFailingCommandReportsError(t *testing.T) {
	e, buf := newStatusExecutor(t, 10*time.Millisecond)
	_, _ = e.Execute(context.Background(), "sleep 0.2; false", io.Discard, io.Discard)
	if want := cmdReport(programstatus.Error, "sleep", "sleep 0.2; false (exit 1)"); !strings.HasSuffix(buf.String(), want) {
		t.Errorf("wrote %q\nwant it to end with %q", buf.String(), want)
	}
}

func TestExecute_QuickCommandReportsNothing(t *testing.T) {
	e, buf := newStatusExecutor(t, time.Second)
	if _, err := e.Execute(context.Background(), "true", io.Discard, io.Discard); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("quick command wrote %q", buf.String())
	}
}

// go test's stdout is not a terminal, so neither escape sequence may be on.
func TestNew_StatusAndProgressNeedATerminal(t *testing.T) {
	e := New()
	if e.status.Enabled() {
		t.Error("status reports should be off when stdout is not a terminal")
	}
	if e.progressOSC.Enabled() {
		t.Error("the progress bar should be off when stdout is not a terminal")
	}
}
