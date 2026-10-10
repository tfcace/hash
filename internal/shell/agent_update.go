package shell

import (
	"fmt"
	"time"
)

// updateCommand is the builtin the update banner offers as ghost text.
const updateCommand = "model update"

// flushUpdateNotice shows a queued update notice and arms the ghost text,
// unless a learned fix already claims this prompt; then the notice waits.
func (s *Shell) flushUpdateNotice(now time.Time) {
	if s.updateNotices == nil || s.fixes.SuggestedFix() != "" {
		return
	}
	select {
	case n := <-s.updateNotices:
		h := s.errors
		if h == nil {
			h = NewErrorHandler()
		}
		h.showUpdateAvailable(n, now)
		s.updateGhost = updateCommand
	default:
	}
}

// formatAge says how long ago t was, coarsely: "today", "12 days ago",
// "4 months ago", "2 years ago". A zero time reads "a while ago".
func formatAge(t, now time.Time) string {
	if t.IsZero() {
		return "a while ago"
	}
	days := int(now.Sub(t).Hours() / 24)
	switch {
	case days < 1:
		return "today"
	case days == 1:
		return "yesterday"
	case days < 30:
		return fmt.Sprintf("%d days ago", days)
	case days < 365:
		return agoPlural(days/30, "month")
	default:
		return agoPlural(days/365, "year")
	}
}

func agoPlural(n int, unit string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s ago", unit)
	}
	return fmt.Sprintf("%d %ss ago", n, unit)
}
