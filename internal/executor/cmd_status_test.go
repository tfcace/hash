package executor

import (
	"bytes"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/tfcace/hash/internal/programstatus"
	"github.com/tfcace/hash/internal/progress"
)

// newTestCmdStatus returns a line status whose threshold never passes on its
// own; tests drive it with start.
func newTestCmdStatus(line string) (*cmdStatus, *bytes.Buffer) {
	cs, buf, _ := newTestCmdStatusWithProgress(line)
	return cs, buf
}

func newTestCmdStatusWithProgress(line string) (cs *cmdStatus, status, bar *bytes.Buffer) {
	status, bar = &bytes.Buffer{}, &bytes.Buffer{}
	cs = newCmdStatus(programstatus.NewReporter(status), progress.NewOSC(bar), line, time.Hour)
	return cs, status, bar
}

// start passes the threshold with app as the program the line started.
func (c *cmdStatus) start(app string) {
	c.setProgram(app)
	c.thresholdPassed()
}

func TestCmdStatus_ProgressFollowsTheLine(t *testing.T) {
	cs, _, bar := newTestCmdStatusWithProgress("curl -O big.iso")
	cs.start("curl")
	cs.finish(0)
	if bar.String() != progressOn+progressOff {
		t.Errorf("bar wrote %q, want on then off", bar.String())
	}
}

// A PTY child hides the bar while it runs; afterwards the threshold starts
// over, so the bar does not come straight back (or flash at line end).
func TestCmdStatus_PTYChildHidesProgressUntilThresholdAgain(t *testing.T) {
	cs, _, bar := newTestCmdStatusWithProgress("sleep 9 >/dev/null; vim; make >log")
	cs.start("sleep")
	cs.drawStarted()
	if bar.String() != progressOn+progressOff {
		t.Fatalf("after PTY start bar wrote %q, want on then off", bar.String())
	}
	cs.drawEnded()
	if bar.String() != progressOn+progressOff {
		t.Fatalf("after PTY end bar wrote %q, want it to stay off until the threshold", bar.String())
	}
	cs.barThresholdPassed() // make runs past the bar's threshold
	cs.finish(0)
	if want := progressOn + progressOff + progressOn + progressOff; bar.String() != want {
		t.Fatalf("bar wrote %q, want %q", bar.String(), want)
	}
}

// A PTY child running when the threshold passes keeps the bar off, while
// the record still reports working.
func TestCmdStatus_ThresholdDuringPTYChildShowsRecordOnly(t *testing.T) {
	cs, status, bar := newTestCmdStatusWithProgress("make")
	cs.drawStarted()
	cs.start("make")
	cs.drawEnded()
	cs.finish(0)
	if bar.Len() != 0 {
		t.Errorf("bar wrote %q, want nothing", bar.String())
	}
	if want := cmdReport(programstatus.Working, "make", "make") + cmdReport(programstatus.Done, "make", "make"); status.String() != want {
		t.Errorf("record wrote %q\nwant %q", status.String(), want)
	}
}

// Withdrawing the record (a full-screen child) leaves the bar to the PTY
// rules.
func TestCmdStatus_WithdrawLeavesProgressToPTY(t *testing.T) {
	cs, _, bar := newTestCmdStatusWithProgress("sleep 9 >/dev/null")
	cs.start("sleep")
	cs.withdraw()
	cs.finish(0)
	if bar.String() != progressOn+progressOff {
		t.Errorf("bar wrote %q, want on then off", bar.String())
	}
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

// syncBuffer is a bytes.Buffer safe for the status timers' goroutines.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// A PTY child that ends before the threshold restarts the wait for the bar
// only: the record still reports working one threshold after the line
// started.
func TestCmdStatus_PTYEndDelaysOnlyTheBar(t *testing.T) {
	t.Parallel()
	const threshold = 200 * time.Millisecond
	status, bar := &syncBuffer{}, &syncBuffer{}
	cs := newCmdStatus(programstatus.NewReporter(status), progress.NewOSC(bar), "vim; make", threshold)
	defer cs.finish(0)
	cs.setProgram("vim")
	cs.drawStarted()
	time.Sleep(150 * time.Millisecond)
	cs.drawEnded()
	time.Sleep(130 * time.Millisecond) // 280ms: past the line's threshold, not the bar's
	if want := cmdReport(programstatus.Working, "vim", "vim; make"); status.String() != want {
		t.Fatalf("at 280ms the record wrote %q, want %q", status.String(), want)
	}
	if bar.String() != "" {
		t.Fatalf("at 280ms the bar wrote %q, want nothing until 350ms", bar.String())
	}
	time.Sleep(170 * time.Millisecond) // 450ms
	if bar.String() != progressOn {
		t.Fatalf("at 450ms the bar wrote %q, want it shown", bar.String())
	}
}

// Every pattern is found split at any byte, after any output, and written a
// byte at a time: ownerTail keeps enough of the last write.
func TestOwnerScanner_SeesEveryPatternSplitAnywhere(t *testing.T) {
	for _, pat := range ownerPatterns {
		for i := 1; i < len(pat); i++ {
			hits := 0
			s := newOwnerScanner(io.Discard, func() { hits++ })
			s.Write([]byte("some output " + string(pat[:i])))            //nolint:errcheck
			s.Write(append(append([]byte(nil), pat[i:]...), " more"...)) //nolint:errcheck
			if hits != 1 {
				t.Errorf("%q split at %d: hits = %d, want 1", pat, i, hits)
			}
		}
		hits := 0
		s := newOwnerScanner(io.Discard, func() { hits++ })
		for _, b := range append([]byte("xx"), pat...) {
			s.Write([]byte{b}) //nolint:errcheck
		}
		if hits != 1 {
			t.Errorf("%q a byte at a time: hits = %d, want 1", pat, hits)
		}
	}
}
