package agentupdate

import (
	"context"
	"time"
)

// Notice says a newer version than the installed one is available.
type Notice struct {
	Installed   string
	Latest      string
	InstalledAt time.Time
}

// defaultEvery is how often the registry is asked and how often the user is
// reminded about the same version.
const defaultEvery = 24 * time.Hour

// Checker decides whether to ask the registry and whether to tell the user.
// Run never fails: when the registry is unreachable the stored answer
// stands, and with nothing stored it stays quiet.
type Checker struct {
	StatePath string
	Installed Install
	Fetcher   Fetcher
	Now       func() time.Time // time.Now when nil
}

// Run refreshes the latest version when due, persists state, and returns a
// Notice when a newer version exists and the user has not been told today.
func (c Checker) Run(ctx context.Context) (Notice, bool) {
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	t := now()
	st := LoadState(c.StatePath)
	defer func() { _ = SaveState(c.StatePath, st) }()

	// A zero or future CheckedAt (first run, clock moved back) counts as due.
	due := st.CheckedAt.IsZero() || st.CheckedAt.After(t) || t.Sub(st.CheckedAt) >= defaultEvery
	if c.Fetcher != nil && due {
		if latest, err := c.Fetcher.Latest(ctx, Package); err == nil {
			st.Latest = latest
			st.CheckedAt = t
		}
	}
	if st.Latest == "" || Compare(st.Latest, c.Installed.Version) <= 0 {
		return Notice{}, false
	}
	if st.NotifiedVersion == st.Latest && t.Sub(st.NotifiedAt) < defaultEvery {
		return Notice{}, false
	}
	st.NotifiedAt = t
	st.NotifiedVersion = st.Latest
	return Notice{Installed: c.Installed.Version, Latest: st.Latest, InstalledAt: c.Installed.InstalledAt}, true
}
