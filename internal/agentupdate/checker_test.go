package agentupdate

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

type stubFetcher struct {
	version string
	err     error
	calls   int
}

func (f *stubFetcher) Latest(context.Context, string) (string, error) {
	f.calls++
	return f.version, f.err
}

func newChecker(t *testing.T, installed string, fetch Fetcher, now *time.Time) Checker {
	t.Helper()
	return Checker{
		StatePath: filepath.Join(t.TempDir(), "agent-update.json"),
		Installed: Install{Version: installed, InstalledAt: now.AddDate(0, -4, 0)},
		Fetcher:   fetch,
		Now:       func() time.Time { return *now },
	}
}

func TestChecker_NotifiesOnceADay(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	fetch := &stubFetcher{version: "0.88.0"}
	c := newChecker(t, "0.42.0", fetch, &now)

	n, ok := c.Run(context.Background())
	if !ok {
		t.Fatal("first Run() = no notice, want one")
	}
	if n.Installed != "0.42.0" || n.Latest != "0.88.0" || n.InstalledAt.IsZero() {
		t.Errorf("Notice = %+v", n)
	}
	if fetch.calls != 1 {
		t.Fatalf("registry calls = %d, want 1", fetch.calls)
	}

	// Another session two hours later: no registry call, no second banner.
	now = now.Add(2 * time.Hour)
	if _, ok := c.Run(context.Background()); ok {
		t.Error("second Run() the same day notified again")
	}
	if fetch.calls != 1 {
		t.Errorf("registry calls = %d, want still 1 (24h throttle)", fetch.calls)
	}

	// Next day: check again and remind once more.
	now = now.Add(24 * time.Hour)
	if _, ok := c.Run(context.Background()); !ok {
		t.Error("Run() the next day = no notice, want the daily reminder")
	}
	if fetch.calls != 2 {
		t.Errorf("registry calls = %d, want 2", fetch.calls)
	}
}

func TestChecker_QuietWhenCurrent(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	c := newChecker(t, "0.88.0", &stubFetcher{version: "0.88.0"}, &now)
	if _, ok := c.Run(context.Background()); ok {
		t.Error("Run() notified although installed == latest")
	}
	if LoadState(c.StatePath).Latest != "0.88.0" {
		t.Error("state should remember the latest version even when current")
	}
}

func TestChecker_OfflineFirstRunIsSilent(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	c := newChecker(t, "0.42.0", &stubFetcher{err: errors.New("dial tcp: no route")}, &now)
	if _, ok := c.Run(context.Background()); ok {
		t.Error("Run() notified with nothing known")
	}
}

func TestChecker_OfflineKeepsPreviousAnswer(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	fetch := &stubFetcher{version: "0.88.0"}
	c := newChecker(t, "0.42.0", fetch, &now)
	if _, ok := c.Run(context.Background()); !ok {
		t.Fatal("expected the first notice")
	}
	fetch.err = errors.New("offline")
	now = now.Add(25 * time.Hour)
	if _, ok := c.Run(context.Background()); !ok {
		t.Error("offline the next day should still remind from the stored answer")
	}
}
