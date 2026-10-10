package shell

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/tfcace/hash/internal/agentupdate"
	"github.com/tfcace/hash/internal/executor"
	"github.com/tfcace/hash/internal/learning"
)

func TestFormatAge(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		t    time.Time
		want string
	}{
		{now.Add(-time.Hour), "today"},
		{now.AddDate(0, 0, -1), "yesterday"},
		{now.AddDate(0, 0, -12), "12 days ago"},
		{now.AddDate(0, 0, -29), "29 days ago"},
		{now.AddDate(0, 0, -30), "1 month ago"},
		{now.AddDate(0, -1, -3), "1 month ago"},
		{now.AddDate(0, -4, -2), "4 months ago"},
		{now.AddDate(0, 0, -364), "12 months ago"},
		{now.AddDate(0, 0, -365), "1 year ago"},
		{now.AddDate(-2, -1, 0), "2 years ago"},
		{time.Time{}, "a while ago"},
	}
	for _, c := range cases {
		if got := formatAge(c.t, now); got != c.want {
			t.Errorf("formatAge(%v) = %q, want %q", c.t, got, c.want)
		}
	}
}

func TestShell_UpdateBannerOffersModelUpdate(t *testing.T) {
	var banner bytes.Buffer
	s := &Shell{
		fixes:         newFixTracker(nil),
		errors:        &ErrorHandler{out: &banner},
		updateNotices: make(chan agentupdate.Notice, 1),
	}
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	s.updateNotices <- agentupdate.Notice{Installed: "0.42.0", Latest: "0.88.0", InstalledAt: now.AddDate(0, -4, -2)}

	s.flushUpdateNotice(now)

	out := banner.String()
	for _, want := range []string{"✦ Claude Agent update available", "→\033[0m model update", "0.42.0 → 0.88.0", "installed 4 months ago", "→ to accept at prompt"} {
		if !strings.Contains(out, want) {
			t.Errorf("banner missing %q:\n%s", want, out)
		}
	}
	if got := s.promptGhost(); got != "model update" {
		t.Errorf("promptGhost() = %q, want the update command", got)
	}
	// A second flush with nothing pending prints nothing.
	banner.Reset()
	s.flushUpdateNotice(now)
	if banner.Len() != 0 {
		t.Errorf("flush with no notice printed: %q", banner.String())
	}
}

func TestShell_UpdateNoticeWaitsBehindLearnedFix(t *testing.T) {
	store := newTestFixStore(t)
	pattern := learning.ExtractPattern("./deploy.sh", "permission denied", 126)
	for i := 0; i < 3; i++ {
		if err := store.RecordFix(pattern, "chmod +x deploy.sh", true); err != nil {
			t.Fatal(err)
		}
	}
	var banner bytes.Buffer
	s := &Shell{
		fixes:         newFixTracker(store),
		errors:        &ErrorHandler{out: &banner},
		updateNotices: make(chan agentupdate.Notice, 1),
	}
	s.updateNotices <- agentupdate.Notice{Installed: "0.42.0", Latest: "0.88.0"}
	cap1 := newStderrCapture(io.Discard)
	_, _ = cap1.Write([]byte("permission denied"))
	s.handleExecutionResult("./deploy.sh", &executor.Result{ExitCode: 126}, nil, cap1)

	s.flushUpdateNotice(time.Now())

	if got := s.promptGhost(); got != "chmod +x deploy.sh" {
		t.Errorf("promptGhost() = %q, want the learned fix to win", got)
	}
	if strings.Contains(banner.String(), "update available") {
		t.Error("update banner shown while a fix is pending")
	}
	if len(s.updateNotices) != 1 {
		t.Error("notice should still be queued for the next prompt")
	}
}
