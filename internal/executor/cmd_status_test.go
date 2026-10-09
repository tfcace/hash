package executor

import (
	"bytes"
	"testing"

	"github.com/tfcace/hash/internal/programstatus"
)

func newTestCmdStatus(line string) (*cmdStatus, *bytes.Buffer) {
	var buf bytes.Buffer
	return newCmdStatus(programstatus.NewReporter(&buf), line), &buf
}

func TestCmdStatus_StartThenFinish(t *testing.T) {
	cs, buf := newTestCmdStatus("make -j8")
	cs.start("make")
	cs.finish(2)
	want := cmdReport(programstatus.Working, "make", "make -j8") + cmdReport(programstatus.Error, "make", "make -j8 (exit 2)")
	if buf.String() != want {
		t.Errorf("wrote %q\nwant  %q", buf.String(), want)
	}
}

func TestCmdStatus_FinishWithoutStartIsSilent(t *testing.T) {
	cs, buf := newTestCmdStatus("true")
	cs.finish(0)
	if buf.Len() != 0 {
		t.Errorf("wrote %q, want nothing", buf.String())
	}
}

func TestCmdStatus_StartAfterFinishIsSilent(t *testing.T) {
	cs, buf := newTestCmdStatus("true")
	cs.finish(0)
	cs.start("true")
	if buf.Len() != 0 {
		t.Errorf("wrote %q, want nothing", buf.String())
	}
}

// A child that owns its status (full screen, or speaking the protocol
// itself) takes the record back: hash clears what it reported and says
// nothing more for the line.
func TestCmdStatus_WithdrawClearsAndSilences(t *testing.T) {
	cs, buf := newTestCmdStatus("vim notes.md")
	cs.start("vim")
	cs.withdraw()
	cs.finish(0)
	want := cmdReport(programstatus.Working, "vim", "vim notes.md") +
		programstatus.Sequence(programstatus.Report{State: programstatus.Clear, ID: "cmd"})
	if buf.String() != want {
		t.Errorf("wrote %q\nwant  %q", buf.String(), want)
	}
}

func TestCmdStatus_WithdrawBeforeStartIsSilent(t *testing.T) {
	cs, buf := newTestCmdStatus("vim notes.md")
	cs.withdraw()
	cs.start("vim")
	cs.finish(0)
	if buf.Len() != 0 {
		t.Errorf("wrote %q, want nothing", buf.String())
	}
}

func TestOwnerScanner_PassesBytesThrough(t *testing.T) {
	var out bytes.Buffer
	hits := 0
	s := newOwnerScanner(&out, func() { hits++ })
	for _, chunk := range []string{"hello ", "world\n"} {
		if n, err := s.Write([]byte(chunk)); err != nil || n != len(chunk) {
			t.Fatalf("Write(%q) = %d, %v", chunk, n, err)
		}
	}
	if out.String() != "hello world\n" || hits != 0 {
		t.Errorf("out = %q, hits = %d", out.String(), hits)
	}
}

func TestOwnerScanner_SeesTheAlternateScreenAcrossWrites(t *testing.T) {
	var out bytes.Buffer
	hits := 0
	s := newOwnerScanner(&out, func() { hits++ })
	s.Write([]byte("\x1b[?10"))                   //nolint:errcheck
	s.Write([]byte("49h\x1b[2J"))                 //nolint:errcheck
	s.Write([]byte("\x1b[?1049h again, ignored")) //nolint:errcheck
	if hits != 1 {
		t.Errorf("hits = %d, want 1 for the split alternate-screen enable", hits)
	}
	if out.String() != "\x1b[?1049h\x1b[2J\x1b[?1049h again, ignored" {
		t.Errorf("out = %q", out.String())
	}
}

func TestOwnerScanner_SeesAChildsOwnReport(t *testing.T) {
	var out bytes.Buffer
	hits := 0
	s := newOwnerScanner(&out, func() { hits++ })
	s.Write([]byte("thinking\x1b]7501;state=working:app=claude-code\x1b\\")) //nolint:errcheck
	if hits != 1 {
		t.Errorf("hits = %d, want 1 for the child's OSC 7501", hits)
	}
}

func TestOwnerScanner_IgnoresOtherSequences(t *testing.T) {
	var out bytes.Buffer
	hits := 0
	s := newOwnerScanner(&out, func() { hits++ })
	s.Write([]byte("\x1b[?25l\x1b[31mred\x1b[0m\x1b]9;4;1;50\x07\x1b[?2004h")) //nolint:errcheck
	if hits != 0 {
		t.Errorf("hits = %d, want 0", hits)
	}
}
