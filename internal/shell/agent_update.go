package shell

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/tfcace/hash/internal/agent"
	"github.com/tfcace/hash/internal/agentupdate"
	"github.com/tfcace/hash/internal/progress"
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
		check = defaultUpdateCheck(agentCfg.Command, s.updateStatePath())
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

// adapterInstaller is what `model update` drives; agentupdate.Installer in
// production, a fake in tests.
type adapterInstaller interface {
	Install(ctx context.Context, out io.Writer) (agentupdate.Result, error)
	GlobalPrefix(ctx context.Context) string
}

func (s *Shell) updateStatePath() string {
	if s.updateStateFile != "" {
		return s.updateStateFile
	}
	return agentUpdateStatePath()
}

// oscProgress is the OSC 9;4 progress channel of the response UI, or a
// disabled one when the shell has no UI (tests).
func (s *Shell) oscProgress() *progress.OSC {
	if s.responseUI != nil && s.responseUI.progress != nil {
		return s.responseUI.progress
	}
	o := progress.NewOSC(io.Discard)
	o.SetEnabled(false)
	return o
}

// runModelUpdate updates the Claude adapter so the model list is current: it
// runs npm visibly, restarts the agent, and shows the models now offered.
// Every failure names the manual command, so the user is never stuck.
func (s *Shell) runModelUpdate(ctx context.Context, out io.Writer) error {
	agentCfg := s.config.EffectiveAgent()
	installer, err := s.resolveInstaller(agentCfg.Command, out)
	if err != nil {
		return err
	}

	// Outside a ?? turn hash has no SIGINT handler, so a Ctrl-C while npm
	// runs would be Go's default: hash exits. Mirror the ?? turn instead:
	// cancel the install's context, which makes ExecRunner SIGINT npm (it
	// got the terminal's SIGINT too), and the failure branch below names
	// the manual command.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT)
	defer signal.Stop(sigCh)
	go func() {
		select {
		case <-sigCh:
			cancel()
		case <-ctx.Done():
		}
	}()

	before := s.agentHandler.AvailableModels()
	prog := s.oscProgress()
	cmd := agentupdate.InstallCommand()

	fmt.Fprintf(out, "\033[36m◐ Updating %s\033[0m\n", agentupdate.DisplayName)
	fmt.Fprintf(out, "  \033[90m$ %s\033[0m\n", cmd)
	s.agentStatus.working("Updating " + agentupdate.DisplayName)
	prog.Start()

	npmOut := newIndentWriter(out)
	res, err := installer.Install(ctx, npmOut)
	npmOut.finish()
	if err != nil {
		failProgress(prog)
		s.agentStatus.failed("Update failed")
		printInstallFailure(out, err, cmd)
		return err
	}

	// The command on PATH still resolves to the version it had: either it
	// was current already, or npm wrote to a prefix PATH does not favor.
	if res.After.Version != "" && res.After.Version == res.Before.Version {
		latest := agentupdate.LoadState(s.updateStatePath()).Latest
		if latest == "" {
			latest = s.lookupLatest(ctx)
		}
		switch {
		case latest == "":
			// Offline right after npm: nothing changed and nothing can be
			// confirmed, so say that instead of a ✓ and a pointless restart.
			prog.Done()
			fmt.Fprintf(out, "\n\033[33m⚠\033[0m %s %s unchanged · could not reach the registry to confirm it is current; try again online\n",
				agentupdate.DisplayName, res.After.Version)
			s.agentStatus.done(agentupdate.DisplayName + " unchanged")
			return nil
		case latest == res.After.Version:
			prog.Done()
			fmt.Fprintf(out, "\n\033[32m✓\033[0m %s %s · already current\n", agentupdate.DisplayName, res.After.Version)
			s.agentStatus.done(agentupdate.DisplayName + " already current")
			return nil
		case agentupdate.Compare(latest, res.After.Version) > 0:
			failProgress(prog)
			s.agentStatus.failed("Update did not take effect")
			fmt.Fprintf(out, "\033[33m⚠ %s on PATH still runs %s\033[0m from %s\n", agentCfg.Command, res.After.Version, res.After.Dir)
			if prefix := installer.GlobalPrefix(ctx); prefix != "" {
				fmt.Fprintf(out, "  npm installed %s under %s · put that bin directory first on PATH, or update the install at %s\n", latest, prefix, res.After.Dir)
			} else {
				fmt.Fprintf(out, "  npm installed %s to a different prefix · compare npm prefix -g with %s, or run: %s\n", latest, res.After.Dir, cmd)
			}
			return fmt.Errorf("%s still resolves to %s", agentCfg.Command, res.After.Version)
		}
	}

	return s.restartAfterUpdate(ctx, out, prog, before, res)
}

// resolveInstaller is what model update drives for command: the injected
// installer, or the npm installer when command is the npm-installed Claude
// adapter. Anything else is nothing hash knows how to update.
func (s *Shell) resolveInstaller(command string, out io.Writer) (adapterInstaller, error) {
	if s.installer != nil {
		return s.installer, nil
	}
	if command == "" {
		fmt.Fprint(out, "\033[31m✗\033[0m no agent command configured; nothing to update\n")
		return nil, errors.New("no agent command configured")
	}
	if _, ok := agentupdate.Detect(command, exec.LookPath); !ok {
		fmt.Fprintf(out, "\033[31m✗\033[0m %s is not the npm-installed %s; nothing to update\n", command, agentupdate.Package)
		return nil, fmt.Errorf("%s: not an npm install of %s", command, agentupdate.Package)
	}
	return agentupdate.Installer{Command: command}, nil
}

// lookupLatest asks the registry for the latest version, for the install
// that lands with no state file to compare against: the network is up (npm
// just used it) and Registry gives up after five seconds. "" when it cannot
// say.
func (s *Shell) lookupLatest(ctx context.Context) string {
	if s.latestLookup != nil {
		return s.latestLookup(ctx)
	}
	latest, _ := agentupdate.Registry{UserAgent: "hash/" + version.Version}.Latest(ctx, agentupdate.Package)
	return latest
}

// restartAfterUpdate brings the new adapter into service and shows what it
// offers. A restart failure is not an install failure: the version is on
// disk and the next ?? starts it, so the outcome is still a success.
func (s *Shell) restartAfterUpdate(ctx context.Context, out io.Writer, prog *progress.OSC, before []agent.ModelOption, res agentupdate.Result) error {
	installed := agentupdate.DisplayName
	if res.After.Version != "" {
		installed += " " + res.After.Version
	}
	fmt.Fprint(out, "  \033[90mrestarting agent\033[0m\n")
	err := s.agentHandler.Restart(ctx)
	prog.Done()
	if err != nil {
		fmt.Fprintf(out, "\n\033[32m✓\033[0m %s installed · it loads on your next ??\n", installed)
		fmt.Fprintf(out, "  \033[90m(agent restart failed: %v)\033[0m\n", err)
		s.agentStatus.done("Updated " + agentupdate.DisplayName)
		return nil
	}
	fmt.Fprintf(out, "\n\033[32m✓\033[0m %s\n", installed)
	if after := s.agentHandler.AvailableModels(); len(after) > 0 {
		fmt.Fprint(out, renderModelsCard(after, s.agentHandler.CurrentModel(), before))
	}
	s.agentStatus.done("Updated " + agentupdate.DisplayName)
	return nil
}

// printInstallFailure says why npm did not install and how to do it by hand.
func printInstallFailure(out io.Writer, err error, cmd string) {
	switch {
	case errors.Is(err, agentupdate.ErrNoNPM):
		fmt.Fprintf(out, "\033[31m✗ npm not found\033[0m · install Node.js, or run it yourself:\n  %s\n", cmd)
	case errors.Is(err, agentupdate.ErrPermission):
		fmt.Fprintf(out, "\033[31m✗ npm could not write to its global prefix\033[0m · try:\n  sudo %s\n", cmd)
	default:
		fmt.Fprintf(out, "\033[31m✗ update failed\033[0m · %v\n  run it yourself: %s\n", err, cmd)
	}
}

// failProgress shows the error badge briefly and then clears it, the way
// progress.Tracker does; a bare Error() leaves the terminal's badge red.
func failProgress(prog *progress.OSC) {
	prog.Error()
	time.AfterFunc(500*time.Millisecond, prog.Done)
}

// renderModelsCard lists the models the agent offers after an update, the
// current one marked, and names absent from the previous list tagged new.
// This is the tangible result of the update: the thing the user wanted.
func renderModelsCard(models []agent.ModelOption, current string, previous []agent.ModelOption) string {
	known := make(map[string]bool, len(previous))
	for _, m := range previous {
		known[m.Name] = true
	}
	width := 0
	for _, m := range models {
		if n := len([]rune(modelLabel(m))); n > width {
			width = n
		}
	}
	var b strings.Builder
	b.WriteString("\n")
	for _, m := range models {
		marker := "  "
		if m.Value == current || m.Name == current {
			marker = "\033[36m●\033[0m "
		}
		fmt.Fprintf(&b, "    %s%-*s", marker, width, modelLabel(m))
		if m.Description != "" {
			fmt.Fprintf(&b, "  \033[90m%s\033[0m", m.Description)
		}
		if len(previous) > 0 && !known[m.Name] {
			b.WriteString("  \033[32mnew\033[0m")
		}
		b.WriteString("\n")
	}
	b.WriteString("    \033[90m└─ model to switch\033[0m\n")
	return b.String()
}

func modelLabel(m agent.ModelOption) string {
	if m.Name != "" {
		return m.Name
	}
	return m.Value
}

// indentWriter renders a child process's output as subordinate to hash's own
// lines: each line indented and dim. Partial writes are handled per byte so
// npm's line-buffered output never loses its framing.
type indentWriter struct {
	w           io.Writer
	atLineStart bool
}

func newIndentWriter(w io.Writer) *indentWriter {
	return &indentWriter{w: w, atLineStart: true}
}

func (iw *indentWriter) Write(p []byte) (int, error) {
	var buf bytes.Buffer
	for _, c := range p {
		if iw.atLineStart {
			buf.WriteString("  \033[90m")
			iw.atLineStart = false
		}
		if c == '\n' {
			buf.WriteString("\033[0m\n")
			iw.atLineStart = true
			continue
		}
		buf.WriteByte(c)
	}
	if _, err := iw.w.Write(buf.Bytes()); err != nil {
		return 0, err
	}
	return len(p), nil
}

// finish closes a line the child left open, so hash's next line starts at
// column 0 in normal color instead of appended dim to npm's last chunk.
func (iw *indentWriter) finish() {
	if iw.atLineStart {
		return
	}
	_, _ = iw.w.Write([]byte("\033[0m\n"))
	iw.atLineStart = true
}
