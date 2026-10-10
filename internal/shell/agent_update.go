package shell

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/tfcace/hash/internal/agentupdate"
	"github.com/tfcace/hash/internal/version"
)

// updateCommand is the builtin the update banner offers as ghost text.
const updateCommand = "model update"

// agentUpdateStatePath is where update checks remember what they learned.
func agentUpdateStatePath() string {
	return filepath.Join(getDataDir(), "agent-update.json")
}

// flushUpdateNotice shows a queued update notice and arms the ghost text,
// unless a learned fix already claims this prompt; then the notice waits.
// A notice that the adapter has caught up with since the check ran (model
// update at the first prompt, npm run by hand elsewhere) is dropped unseen.
func (s *Shell) flushUpdateNotice(now time.Time) {
	if s.updateNotices == nil || s.fixes.SuggestedFix() != "" {
		return
	}
	select {
	case n := <-s.updateNotices:
		if v := s.installedAdapterVersion(); v != "" && agentupdate.Compare(v, n.Latest) >= 0 {
			return
		}
		h := s.errors
		if h == nil {
			h = NewErrorHandler()
		}
		h.showUpdateAvailable(n, now)
		s.updateGhost = updateCommand
	default:
	}
}

// installedAdapterVersion re-reads the adapter version on disk, or "" when
// there is no npm-installed adapter to read. Cheap: a readlink and a small
// file, and only called when a notice is about to be shown.
func (s *Shell) installedAdapterVersion() string {
	if s.adapterVersion != nil {
		return s.adapterVersion()
	}
	if s.config == nil {
		return ""
	}
	inst, ok := agentupdate.Detect(s.config.EffectiveAgent().Command, exec.LookPath)
	if !ok {
		return ""
	}
	return inst.Version
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

// startUpdateCheck looks for a newer adapter in the background. It runs only
// for an interactive shell with a stdio agent whose config has not turned
// checks off. Everything, including resolving the binary, happens off the
// startup path; the result is drained before a later prompt.
func (s *Shell) startUpdateCheck(ctx context.Context) {
	if !s.mode.Interactive || s.config == nil {
		return
	}
	agentCfg := s.config.EffectiveAgent()
	if agentCfg.AutoUpdate == "off" || agentCfg.Transport == "http" || agentCfg.Command == "" {
		return
	}
	check := s.updateChecker
	if check == nil {
		check = defaultUpdateCheck(agentCfg.Command, agentUpdateStatePath())
	}
	s.updateNotices = make(chan agentupdate.Notice, 1)
	ch := s.updateNotices
	go func() {
		if n, ok := check(ctx); ok {
			ch <- n
		}
	}()
}

// defaultUpdateCheck detects the npm-installed adapter behind command and
// runs the daily registry check against it.
func defaultUpdateCheck(command, statePath string) func(context.Context) (agentupdate.Notice, bool) {
	return func(ctx context.Context) (agentupdate.Notice, bool) {
		inst, ok := agentupdate.Detect(command, exec.LookPath)
		if !ok {
			return agentupdate.Notice{}, false
		}
		return agentupdate.Checker{
			StatePath: statePath,
			Installed: inst,
			Fetcher:   agentupdate.Registry{UserAgent: "hash/" + version.Version},
		}.Run(ctx)
	}
}
