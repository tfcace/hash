package executor

import (
	"errors"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A PTY leader's report, word by word, ends in the state its words lead
// to; started, atEnd and wait answer from it. A word out of turn
// is dropped.
func TestPTYReport_States(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		report   string
		final    ptyReportState
		startErr error
		left     string
		atEnd    bool
	}{
		{"seuecho hi\npart", leaderReported, nil, "echo hi\npart", true},
		{"seu", leaderReported, nil, "", true},
		{"sseu", leaderReported, nil, "", true}, // a repeated start
		{"seeu", leaderReported, nil, "", true}, // a repeated end
		{"esu", leaderLost, nil, "", false},     // an end before the start
		{"su", leaderLost, nil, "", false},      // unread keys while it runs
		{"se", leaderLost, nil, "", true},       // killed once it ended
		{"s", leaderLost, nil, "", false},       // killed while it ran
		{"", leaderSilent, errLeaderSilent, "", false},
		{"?", leaderSilent, errLeaderSilent, "", false},
		{"x2", commandStartFailed, syscall.ENOENT, "", false},
		{"sx2", leaderLost, nil, "", false}, // a start failure after the start
	} {
		_, releaseW, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		r := newPTYReport(releaseW)
		_ = releaseW.Close()
		stopped := make(chan struct{})
		r.atEnd(func() { close(stopped) })
		r.read(strings.NewReader(tc.report), "/bin/x")
		left := r.wait()
		startErr := r.started()
		atEnd := false
		select {
		case <-stopped:
			atEnd = true
		case <-time.After(50 * time.Millisecond):
		}
		if r.state != tc.final || string(left) != tc.left || atEnd != tc.atEnd ||
			(tc.startErr == nil) != (startErr == nil) || tc.startErr != nil && !errors.Is(startErr, tc.startErr) {
			t.Errorf("report %q: state %d, left %q, at end %v, started() %v; want %d, %q, %v, %v",
				tc.report, r.state, left, atEnd, startErr, tc.final, tc.left, tc.atEnd, tc.startErr)
		}
	}
}

// With no leader (the command led its PTY itself) the report is nil: the
// command started, nothing comes back, and nothing is released.
func TestPTYReport_None(t *testing.T) {
	var r *ptyReport
	r.atEnd(func() { t.Error("at the end with no leader") })
	if r.releaser() != nil {
		t.Error("a release with no leader")
	}
	if err := r.started(); err != nil {
		t.Errorf("started() = %v", err)
	}
	if left := r.wait(); left != nil {
		t.Errorf("wait() = %q", left)
	}
}
