package shell

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tfcace/hash/internal/agent"
	"github.com/tfcace/hash/internal/agentupdate"
	"github.com/tfcace/hash/internal/config"
	"github.com/tfcace/hash/internal/executor"
	"github.com/tfcace/hash/internal/learning"
	"github.com/tfcace/hash/internal/programstatus"
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

// A notice computed before the adapter was updated (model update at the
// first prompt, or npm run by hand in another terminal) must not be shown
// once the installed version has caught up with it.
func TestShell_UpdateNoticeDroppedWhenAlreadyInstalled(t *testing.T) {
	var banner bytes.Buffer
	s := &Shell{
		fixes:          newFixTracker(nil),
		errors:         &ErrorHandler{out: &banner},
		updateNotices:  make(chan agentupdate.Notice, 1),
		adapterVersion: func() string { return "0.89.0" }, // what is on disk now
	}
	s.updateNotices <- agentupdate.Notice{Installed: "0.42.0", Latest: "0.89.0"}

	s.flushUpdateNotice(time.Now())

	if banner.Len() != 0 {
		t.Errorf("stale notice was shown:\n%s", banner.String())
	}
	if got := s.promptGhost(); got != "" {
		t.Errorf("promptGhost() = %q, want no ghost for a stale notice", got)
	}
	if len(s.updateNotices) != 0 {
		t.Error("stale notice should be consumed, not left for the next prompt")
	}

	// A notice that still applies is shown as before.
	s.adapterVersion = func() string { return "0.42.0" }
	s.updateNotices <- agentupdate.Notice{Installed: "0.42.0", Latest: "0.89.0"}
	s.flushUpdateNotice(time.Now())
	if !strings.Contains(banner.String(), "0.42.0 → 0.89.0") {
		t.Errorf("live notice not shown:\n%s", banner.String())
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

func TestShell_StartUpdateCheckDeliversNotice(t *testing.T) {
	var banner bytes.Buffer
	s := &Shell{
		mode:   Mode{Interactive: true},
		config: config.Default(),
		fixes:  newFixTracker(nil),
		errors: &ErrorHandler{out: &banner},
		updateChecker: func(context.Context) (agentupdate.Notice, bool) {
			return agentupdate.Notice{Installed: "0.42.0", Latest: "0.88.0"}, true
		},
		adapterVersion: func() string { return "0.42.0" }, // the notice must not be judged against this machine
	}

	s.startUpdateCheck(context.Background())

	select {
	case n := <-s.updateNotices:
		s.updateNotices <- n // put it back for the flush
	case <-time.After(2 * time.Second):
		t.Fatal("no notice delivered")
	}
	s.flushUpdateNotice(time.Now())
	if !strings.Contains(banner.String(), "0.42.0 → 0.88.0") {
		t.Errorf("banner = %q", banner.String())
	}
}

func TestShell_StartUpdateCheckHonorsOffAndNonInteractive(t *testing.T) {
	off := config.Default()
	off.Agent.AutoUpdate = "off"
	httpTransport := config.Default()
	httpTransport.Agent.Transport = "http"
	noCommand := config.Default()
	noCommand.Agent.Command = ""
	cases := map[string]*Shell{
		"auto_update off": {mode: Mode{Interactive: true}, config: off},
		"non-interactive": {mode: Mode{Interactive: false}, config: config.Default()},
		"http transport":  {mode: Mode{Interactive: true}, config: httpTransport},
		"empty command":   {mode: Mode{Interactive: true}, config: noCommand},
	}
	for name, s := range cases {
		t.Run(name, func(t *testing.T) {
			// A nil channel below proves the goroutine never started, so the
			// stub only needs to exist.
			s.updateChecker = func(context.Context) (agentupdate.Notice, bool) { return agentupdate.Notice{}, false }
			s.startUpdateCheck(context.Background())
			if s.updateNotices != nil {
				t.Error("notice channel created although checks are off")
			}
		})
	}
}

type fakeInstaller struct {
	res    agentupdate.Result
	err    error
	prefix string
	output string
}

func (f fakeInstaller) Install(_ context.Context, out io.Writer) (agentupdate.Result, error) {
	_, _ = io.WriteString(out, f.output)
	return f.res, f.err
}

func (f fakeInstaller) GlobalPrefix(context.Context) string { return f.prefix }

func updatedResult() agentupdate.Result {
	return agentupdate.Result{
		Before: agentupdate.Install{Version: "0.42.0", Dir: "/opt/homebrew/lib/node_modules/@agentclientprotocol/claude-agent-acp"},
		After:  agentupdate.Install{Version: "0.88.0", Dir: "/opt/homebrew/lib/node_modules/@agentclientprotocol/claude-agent-acp"},
	}
}

func newUpdateShell(t *testing.T, tr *modelsTestTransport, inst adapterInstaller) *Shell {
	t.Helper()
	return &Shell{
		config:          config.Default(),
		agentHandler:    NewAgentHandler(agent.NewClient(tr)),
		installer:       inst,
		updateStateFile: filepath.Join(t.TempDir(), "agent-update.json"),
		latestLookup:    func(context.Context) string { return "" }, // no registry in tests
	}
}

func TestModelUpdate_ShowsFreshModelsWithNewMarkers(t *testing.T) {
	old := []agent.ModelOption{
		{Value: "default", Name: "Default (recommended)", Description: "Opus 4.8 with 1M context"},
		{Value: "sonnet", Name: "Sonnet", Description: "Sonnet 4.6"},
	}
	fresh := []agent.ModelOption{
		{Value: "default", Name: "Default (recommended)", Description: "Opus 5.5 with 1M context"},
		{Value: "sonnet", Name: "Sonnet", Description: "Sonnet 5.5"},
		{Value: "haiku", Name: "Haiku", Description: "Haiku 5.5"},
	}
	tr := &modelsTestTransport{models: old, next: fresh, current: "Default (recommended)"}
	s := newUpdateShell(t, tr, fakeInstaller{res: updatedResult(), output: "added 3 packages in 12s\n"})

	var out bytes.Buffer
	if err := s.runModelUpdate(context.Background(), &out); err != nil {
		t.Fatalf("runModelUpdate() error = %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"◐ Updating Claude Agent",
		"$ npm install -g @agentclientprotocol/claude-agent-acp@latest",
		"added 3 packages in 12s",
		"✓\033[0m Claude Agent 0.88.0",
		"●\033[0m Default (recommended)",
		"Haiku",
		"└─ model to switch",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
	if tr.closes != 1 {
		t.Errorf("agent closes = %d, want a restart", tr.closes)
	}
	for _, line := range strings.Split(got, "\n") {
		switch {
		case strings.Contains(line, "Haiku") && !strings.Contains(line, "new"):
			t.Errorf("Haiku was not in the old list, want a new tag: %q", line)
		case strings.Contains(line, "Sonnet") && strings.Contains(line, "new"):
			t.Errorf("Sonnet was in the old list, must not be tagged new: %q", line)
		}
	}
}

func TestModelUpdate_NoNpm(t *testing.T) {
	tr := &modelsTestTransport{}
	s := newUpdateShell(t, tr, fakeInstaller{err: agentupdate.ErrNoNPM})
	var out bytes.Buffer
	if err := s.runModelUpdate(context.Background(), &out); err == nil {
		t.Fatal("runModelUpdate() = nil error, want failure")
	}
	if !strings.Contains(out.String(), "npm not found") || !strings.Contains(out.String(), agentupdate.InstallCommand()) {
		t.Errorf("output = %q, want the reason and the manual command", out.String())
	}
	if tr.closes != 0 {
		t.Error("agent restarted although nothing was installed")
	}
}

func TestModelUpdate_PermissionDenied(t *testing.T) {
	s := newUpdateShell(t, &modelsTestTransport{}, fakeInstaller{err: agentupdate.ErrPermission, output: "npm error code EACCES\n"})
	var out bytes.Buffer
	_ = s.runModelUpdate(context.Background(), &out)
	if !strings.Contains(out.String(), "sudo "+agentupdate.InstallCommand()) {
		t.Errorf("output = %q, want a sudo hint", out.String())
	}
}

func TestModelUpdate_PrefixMismatch(t *testing.T) {
	res := updatedResult()
	res.After.Version = "0.42.0" // the binary on PATH did not change
	tr := &modelsTestTransport{}
	s := newUpdateShell(t, tr, fakeInstaller{res: res, prefix: "/Users/me/.nvm/versions/node/v22/"})
	if err := agentupdate.SaveState(s.updateStateFile, agentupdate.State{Latest: "0.88.0"}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := s.runModelUpdate(context.Background(), &out); err == nil {
		t.Fatal("runModelUpdate() = nil error, want the mismatch reported as failure")
	}
	got := out.String()
	for _, want := range []string{"still runs 0.42.0", res.After.Dir, "/Users/me/.nvm/versions/node/v22/"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
	if tr.closes != 0 {
		t.Error("agent restarted although the old version is still on PATH")
	}
}

func TestModelUpdate_RestartFailureStillReportsInstall(t *testing.T) {
	tr := &modelsTestTransport{ensureErr: errors.New("agent exited")}
	s := newUpdateShell(t, tr, fakeInstaller{res: updatedResult()})
	var out bytes.Buffer
	if err := s.runModelUpdate(context.Background(), &out); err != nil {
		t.Fatalf("runModelUpdate() error = %v, want nil: the install stands", err)
	}
	if !strings.Contains(out.String(), "0.88.0") || !strings.Contains(out.String(), "next ??") {
		t.Errorf("output = %q, want the installed version and the next-?? note", out.String())
	}
}

func TestModelUpdate_NotNpmInstall(t *testing.T) {
	// No installer injected and the configured command is not on PATH as the
	// npm package: there is nothing hash knows how to update.
	cfg := config.Default()
	cfg.Agent.Command = "gemini"
	s := &Shell{config: cfg, agentHandler: NewAgentHandler(agent.NewClient(&modelsTestTransport{}))}
	var out bytes.Buffer
	if err := s.runModelUpdate(context.Background(), &out); err == nil {
		t.Fatal("runModelUpdate() = nil error for a non-npm agent")
	}
	if !strings.Contains(out.String(), "nothing to update") {
		t.Errorf("output = %q", out.String())
	}
}

// cancelingInstaller stands in for npm interrupted by Ctrl-C. A real SIGINT
// cannot be sent safely in a unit test; the handler's only effect is to
// cancel the install's context, so the fake cancels the parent context
// mid-install, waits for the cancellation to reach it, and returns ctx.Err()
// the way ExecRunner does for a SIGINTed npm.
type cancelingInstaller struct{ cancel context.CancelFunc }

func (c cancelingInstaller) Install(ctx context.Context, out io.Writer) (agentupdate.Result, error) {
	_, _ = io.WriteString(out, "npm http fetch GET 200 https://registry.npmjs.org/") // mid-line when interrupted
	c.cancel()
	<-ctx.Done()
	return agentupdate.Result{}, ctx.Err()
}

func (cancelingInstaller) GlobalPrefix(context.Context) string { return "" }

func TestModelUpdate_CtrlCCancelsInstallNotHash(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tr := &modelsTestTransport{}
	s := newUpdateShell(t, tr, cancelingInstaller{cancel: cancel})
	var out bytes.Buffer
	err := s.runModelUpdate(ctx, &out)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("runModelUpdate() error = %v, want context.Canceled", err)
	}
	got := out.String()
	for _, want := range []string{"update failed", agentupdate.InstallCommand(), "registry.npmjs.org/\033[0m\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
	if tr.closes != 0 {
		t.Error("agent restarted although the install was interrupted")
	}
}

func TestModelUpdate_AlreadyCurrent(t *testing.T) {
	res := updatedResult()
	res.After.Version = "0.42.0" // nothing newer was published
	tr := &modelsTestTransport{}
	s := newUpdateShell(t, tr, fakeInstaller{res: res})
	s.latestLookup = func(context.Context) string { return "0.42.0" } // no state file yet: the registry is asked once
	var out bytes.Buffer
	if err := s.runModelUpdate(context.Background(), &out); err != nil {
		t.Fatalf("runModelUpdate() error = %v", err)
	}
	if !strings.Contains(out.String(), "✓\033[0m Claude Agent 0.42.0 · already current") {
		t.Errorf("output = %q, want the already-current line", out.String())
	}
	if tr.closes != 0 {
		t.Error("agent restarted although nothing changed")
	}
}

func TestModelUpdate_UnchangedAndUnconfirmedOffline(t *testing.T) {
	res := updatedResult()
	res.After.Version = "0.42.0" // the version on PATH did not change
	tr := &modelsTestTransport{}
	s := newUpdateShell(t, tr, fakeInstaller{res: res}) // no state file, and latestLookup returns "" (offline)
	var out bytes.Buffer
	if err := s.runModelUpdate(context.Background(), &out); err != nil {
		t.Fatalf("runModelUpdate() error = %v, want nil: the install itself succeeded", err)
	}
	got := out.String()
	if !strings.Contains(got, "Claude Agent 0.42.0 unchanged") || !strings.Contains(got, "could not reach the registry") {
		t.Errorf("output = %q, want the unchanged-and-unconfirmed line", got)
	}
	if strings.Contains(got, "✓") {
		t.Errorf("output = %q, must not claim success when nothing could be confirmed", got)
	}
	if tr.closes != 0 {
		t.Error("agent restarted although nothing changed")
	}
}

func TestModelUpdate_PrefixMismatchWithoutStateOrPrefix(t *testing.T) {
	res := updatedResult()
	res.After.Version = "0.42.0"
	tr := &modelsTestTransport{}
	s := newUpdateShell(t, tr, fakeInstaller{res: res}) // GlobalPrefix unknown
	s.latestLookup = func(context.Context) string { return "0.88.0" }
	var out bytes.Buffer
	if err := s.runModelUpdate(context.Background(), &out); err == nil {
		t.Fatal("runModelUpdate() = nil error, want the mismatch reported as failure")
	}
	got := out.String()
	for _, want := range []string{"still runs 0.42.0", "0.88.0 to a different prefix", "npm prefix -g", res.After.Dir, agentupdate.InstallCommand()} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
	if tr.closes != 0 {
		t.Error("agent restarted although the old version is still on PATH")
	}
}

func TestModelUpdate_NoAgentCommand(t *testing.T) {
	cfg := config.Default()
	cfg.Agent.Command = ""
	s := &Shell{config: cfg, agentHandler: NewAgentHandler(agent.NewClient(&modelsTestTransport{}))}
	var out bytes.Buffer
	if err := s.runModelUpdate(context.Background(), &out); err == nil {
		t.Fatal("runModelUpdate() = nil error with no agent command")
	}
	if !strings.Contains(out.String(), "no agent command configured; nothing to update") {
		t.Errorf("output = %q", out.String())
	}
}

// The OSC 7501 record opened by working is closed by done or failed, so the
// terminal never keeps a stale "Updating" badge.
func TestModelUpdate_ReportsProgramStatus(t *testing.T) {
	working := rootReport(programstatus.Working, "", "Updating Claude Agent")

	var reports bytes.Buffer
	tr := &modelsTestTransport{models: []agent.ModelOption{{Value: "default", Name: "Default"}}}
	s := newUpdateShell(t, tr, fakeInstaller{res: updatedResult()})
	s.agentStatus = newAgentStatus(programstatus.NewReporter(&reports))
	if err := s.runModelUpdate(context.Background(), io.Discard); err != nil {
		t.Fatalf("runModelUpdate() error = %v", err)
	}
	if got, done := reports.String(), rootReport(programstatus.Done, "", "Updated Claude Agent"); !strings.HasPrefix(got, working) || !strings.HasSuffix(got, done) {
		t.Errorf("success reported %q\nwant %q then %q", got, working, done)
	}

	reports.Reset()
	s = newUpdateShell(t, &modelsTestTransport{}, fakeInstaller{err: agentupdate.ErrNoNPM})
	s.agentStatus = newAgentStatus(programstatus.NewReporter(&reports))
	if err := s.runModelUpdate(context.Background(), io.Discard); err == nil {
		t.Fatal("runModelUpdate() = nil error, want ErrNoNPM")
	}
	if got, failed := reports.String(), rootReport(programstatus.Error, "", "Update failed"); !strings.HasPrefix(got, working) || !strings.HasSuffix(got, failed) {
		t.Errorf("failure reported %q\nwant %q then %q", got, working, failed)
	}
}

func TestIndentWriter_FinishClosesAnOpenLine(t *testing.T) {
	var out bytes.Buffer
	iw := newIndentWriter(&out)
	_, _ = iw.Write([]byte("one\ntwo"))
	iw.finish()
	iw.finish() // idempotent at line start
	if got, want := out.String(), "  \033[90mone\033[0m\n  \033[90mtwo\033[0m\n"; got != want {
		t.Errorf("wrote %q, want %q", got, want)
	}
}
