package shell

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/tfcace/hash/internal/agent"
	"github.com/tfcace/hash/internal/config"
	"github.com/tfcace/hash/internal/executor"
	"github.com/tfcace/hash/internal/parser"
	"github.com/tfcace/hash/internal/programstatus"
	"github.com/tfcace/hash/internal/prompt"
)

// turnScript is what a scriptedTransport replays for one request: its events
// in order, then err (if any), holding the stream open for hold first.
type turnScript struct {
	events []agent.StreamEvent
	err    error
	hold   time.Duration
	// during runs on its own goroutine once the events are out, the way an
	// ACP permission request arrives beside the stream.
	during func(ctx context.Context)
	// waitCtx keeps the stream open until the request ends, then reports
	// the request's error, the way a hung agent times out.
	waitCtx bool
	// after is sent once gate closes, behind whatever during started.
	gate  <-chan struct{}
	after []agent.StreamEvent
}

type scriptedTransport struct {
	namedTestTransport
	mu       sync.Mutex
	scripts  []turnScript
	requests []agent.Request
}

func (s *scriptedTransport) SendEventStream(ctx context.Context, req agent.Request) (<-chan agent.StreamEvent, <-chan error) {
	s.mu.Lock()
	s.requests = append(s.requests, req)
	var script turnScript
	if len(s.scripts) > 0 {
		script, s.scripts = s.scripts[0], s.scripts[1:]
	}
	s.mu.Unlock()

	events := make(chan agent.StreamEvent)
	errs := make(chan error, 1)
	go func() {
		defer close(events)
		defer close(errs)
		for _, ev := range script.events {
			select {
			case events <- ev:
			case <-ctx.Done():
				errs <- ctx.Err()
				return
			}
		}
		if script.during != nil {
			go script.during(ctx)
		}
		if script.gate != nil {
			<-script.gate
		}
		for _, ev := range script.after {
			select {
			case events <- ev:
			case <-ctx.Done():
				errs <- ctx.Err()
				return
			}
		}
		if script.waitCtx {
			<-ctx.Done()
			errs <- ctx.Err()
			return
		}
		if script.hold > 0 {
			time.Sleep(script.hold)
		}
		if script.err != nil {
			errs <- script.err
		}
	}()
	return events, errs
}

func (s *scriptedTransport) requestCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.requests)
}

func textEvent(text string) agent.StreamEvent {
	return agent.StreamEvent{Type: agent.StreamEventText, Text: text}
}

func toolEvent(id, title string, status agent.ToolCallStatus) agent.StreamEvent {
	return agent.StreamEvent{Type: agent.StreamEventToolCall, ToolCall: agent.ToolCallUpdate{ID: id, Title: title, Kind: "execute", Status: status}}
}

// newTurnShell is a shell whose ?? turns reach tr, draw to screen and report
// OSC 7501 to status. With answers it runs on a test terminal whose user
// types them at the confirmation hints, in order (Esc once they run out),
// and n at each permission prompt; with none, stdin is no terminal, so no
// one can be asked: a hint is declined and a tool denied unasked.
func newTurnShell(t *testing.T, tr agent.Transport, answers ...string) (sh *Shell, screen, status *lockedBuffer) {
	t.Helper()
	sh, screen, status = newTurnShellOff(t, tr)
	if len(answers) > 0 {
		newTestTerminal(t, sh).answerHints(screen, answers...)
	}
	return sh, screen, status
}

// newTurnShellOn is newTurnShell on a test terminal, returned for the
// test to type on; its user answers as newTurnShell's does.
func newTurnShellOn(t *testing.T, tr agent.Transport, answers ...string) (sh *Shell, screen, status *lockedBuffer, tt *testTerminal) {
	t.Helper()
	sh, screen, status = newTurnShellOff(t, tr)
	tt = newTestTerminal(t, sh)
	tt.answerHints(screen, answers...)
	return sh, screen, status, tt
}

func newTurnShellOff(t *testing.T, tr agent.Transport) (sh *Shell, screen, status *lockedBuffer) {
	t.Helper()
	cfg := config.Default()
	cfg.Agent.Transport = "http"
	cfg.Agent.URL = "http://agent.invalid"
	cfg.Prompt.Mode = "built-in"
	screen, status = &lockedBuffer{}, &lockedBuffer{}
	sh = &Shell{
		config:       cfg,
		executor:     executor.New(),
		prompt:       prompt.New(prompt.Config{Mode: "built-in"}),
		agentHandler: NewAgentHandler(agent.NewClient(tr)),
		responseUI:   NewResponseUI(screen),
		agentOutput:  NewAgentOutputCoordinator(screen),
		agentStatus:  newAgentStatus(programstatus.NewReporter(status)),
	}
	t.Cleanup(func() { sh.agentOutput.TakeRow() })
	return sh, screen, status
}

func lastStatus(status *lockedBuffer) string {
	s := status.String()
	if i := strings.LastIndex(s, "\x1b]7501;"); i >= 0 {
		return s[i:]
	}
	return ""
}

func ask(t *testing.T, sh *Shell, line string) error {
	t.Helper()
	return sh.handleAgentRequest(questionCtx(t), line, parser.Parse(line))
}

// "Let me check... [tool] ... answer": text that follows a tool call must not
// stream under a spinner that is still redrawing its row every 80ms.
func TestTurn_TextAfterAToolIsNotOverdrawnBySpinner(t *testing.T) {
	t.Parallel()
	tr := &scriptedTransport{scripts: []turnScript{{
		events: []agent.StreamEvent{
			textEvent("Let me check.\n"),
			toolEvent("t1", "ls", agent.ToolCallInProgress),
			toolEvent("t1", "ls", agent.ToolCallCompleted),
			textEvent("The answer is 42."),
		},
		hold: 300 * time.Millisecond,
	}}}
	sh, screen, _ := newTurnShell(t, tr, keyRun)

	if err := ask(t, sh, "?? what is it"); err != nil {
		t.Fatal(err)
	}

	out := screen.String()
	i := strings.LastIndex(out, "The answer is 42.")
	if i < 0 {
		t.Fatalf("answer never drawn: %q", out)
	}
	if after := out[i:]; strings.Contains(after, "agent · thinking") || strings.Contains(after, "agent · running") {
		t.Errorf("spinner drew over the answer: %q", after)
	}
}

// probeBuiltin records what a command handed off by a ?? turn sees while it
// runs: who Ctrl+C reaches, and what the terminal was last told about hash.
type probeBuiltin struct {
	ran        bool
	claims     int   // claims on Ctrl+C while it ran
	cause      error // why its context ended after Ctrl+C
	lastStatus string
}

func (p *probeBuiltin) register(sh *Shell, status *lockedBuffer) {
	sh.executor.RegisterBuiltin("probe", func(ctx context.Context, _ []string, _, _ io.Writer) error {
		p.ran = true
		p.lastStatus = lastStatus(status)
		sh.interrupts.mu.Lock()
		p.claims = len(sh.interrupts.claims)
		sh.interrupts.mu.Unlock()
		sh.interrupts.fire() // Ctrl+C while the command runs
		p.cause = context.Cause(ctx)
		return nil
	})
}

func (p *probeBuiltin) check(t *testing.T) {
	t.Helper()
	if !p.ran {
		t.Fatal("the command never ran")
	}
	if want := rootReport(programstatus.Idle, "", ""); p.lastStatus != want {
		t.Errorf("while the command ran the terminal was last told %q, want idle %q", p.lastStatus, want)
	}
	if p.claims != 1 {
		t.Errorf("%d claims on Ctrl+C while the command ran, want only its line's: the finished ?? turn still held one", p.claims)
	}
	if !errors.Is(p.cause, errLineInterrupted) {
		t.Errorf("Ctrl+C during the command ended it with %v, want its line interrupted", p.cause)
	}
}

// The inline form hands the accepted line to the shell; the turn must be
// over before it runs, not still reporting Working with Ctrl+C armed.
func TestTurn_InlineAcceptedLineRunsAfterTheTurnEnds(t *testing.T) {
	tr := &scriptedTransport{scripts: []turnScript{{events: []agent.StreamEvent{textEvent("--all")}}}}
	sh, _, status := newTurnShell(t, tr)
	var probe probeBuiltin
	probe.register(sh, status)
	typeAhead(t, sh, keyKillAll+"probe\r") // the user makes the line probe and runs it

	var err error
	captureStdout(t, sh, func() { err = ask(t, sh, "ls ?? show hidden") })
	if err != nil {
		t.Fatal(err)
	}
	probe.check(t)
}

func TestTurn_AgentCommandRunsAfterTheTurnEnds(t *testing.T) {
	t.Parallel()
	tr := &scriptedTransport{scripts: []turnScript{{events: []agent.StreamEvent{textEvent("```bash\nprobe --now\n```")}}}}
	sh, _, status := newTurnShell(t, tr, keyRun)
	var probe probeBuiltin
	probe.register(sh, status)

	if err := ask(t, sh, "?? run the probe"); err != nil {
		t.Fatal(err)
	}
	probe.check(t)
}

func TestTurn_EditedAgentCommandRunsAfterTheTurnEnds(t *testing.T) {
	t.Parallel()
	tr := &scriptedTransport{scripts: []turnScript{{events: []agent.StreamEvent{textEvent("```bash\nprobe --dry\n```")}}}}
	sh, _, status := newTurnShell(t, tr, keyEdit+keyKillAll+"probe\r") // Tab, then make the line probe and run it
	var probe probeBuiltin
	probe.register(sh, status)

	var err error
	captureStdout(t, sh, func() { err = ask(t, sh, "?? run the probe") })
	if err != nil {
		t.Fatal(err)
	}
	probe.check(t)
}

var errFlaky = errors.New("connection reset by peer")

// Retrying a failed turn replaces the "enter to retry" hint with the new
// attempt instead of leaving it above the retried reply.
func TestTurn_RetryClearsTheRetryHint(t *testing.T) {
	t.Parallel()
	tr := &scriptedTransport{scripts: []turnScript{
		{err: errFlaky},
		{events: []agent.StreamEvent{textEvent("Recovered.")}},
	}}
	sh, screen, _ := newTurnShell(t, tr, keyRun, keyRun)

	if err := ask(t, sh, "?? try"); err != nil {
		t.Fatal(err)
	}

	out := screen.String()
	hint := strings.Index(out, "enter to retry")
	answer := strings.Index(out, "Recovered.")
	if hint < 0 || answer < hint {
		t.Fatalf("want the retry hint then the answer, got %q", out)
	}
	if between := out[hint:answer]; !strings.Contains(between, "\x1b[A\x1b[K\x1b[A\x1b[K") {
		t.Errorf("retry hint left on screen above the retried reply: %q", between)
	}
	if got := tr.requestCount(); got != 2 {
		t.Errorf("requests = %d, want the turn and its retry", got)
	}
}

// A follow-up in a conversation fails and recovers the same way the first
// turn does: the user can retry it.
func TestTurn_FollowUpErrorOffersRetry(t *testing.T) {
	t.Parallel()
	tr := &scriptedTransport{scripts: []turnScript{
		{events: []agent.StreamEvent{textEvent("Which directory?")}},
		{err: errFlaky},
		{events: []agent.StreamEvent{textEvent("Found it.")}},
	}}
	// reply to the question, retry the failed follow-up, finish
	sh, _, _ := newTurnShell(t, tr, keyReply+"internal/shell\r", keyRun, keyRun)

	var err error
	captureStdout(t, sh, func() { err = ask(t, sh, "?? find my config") })
	if err != nil {
		t.Fatal(err)
	}
	if got := tr.requestCount(); got != 3 {
		t.Errorf("requests = %d, want the turn, the failed follow-up and its retry", got)
	}
}

// A request that times out while a permission prompt is up ends in an error.
// The prompt's goroutine notices the timeout later: it must neither report
// the turn working again (losing the error) nor leave the prompt's rows.
func TestTurn_TimeoutDuringPermissionKeepsTheErrorAndClearsThePrompt(t *testing.T) {
	t.Parallel()
	permissionDone := make(chan struct{})
	var sh *Shell
	tr := &scriptedTransport{scripts: []turnScript{{
		events:  []agent.StreamEvent{toolEvent("t1", "rm -rf build", agent.ToolCallPending)},
		waitCtx: true,
		during: func(ctx context.Context) {
			defer close(permissionDone)
			sh.handleToolPermission(ctx, agent.ToolPermissionRequest{Command: "rm -rf build", ToolName: "Bash", ToolCallID: "t1"})
		},
	}}}
	sh, screen, status, tt := newTurnShellOn(t, tr)
	tt.answerPermissions("") // nobody answers: the request times out under the prompt
	sh.config.Agent.Timeout = "300ms"

	if err := ask(t, sh, "?? clean up"); err != nil {
		t.Fatal(err)
	}
	<-permissionDone

	if got := lastStatus(status); !strings.Contains(got, "state=error") {
		t.Errorf("last status = %q, want the timeout error to stay", got)
	}
	out := screen.String()
	prompt := strings.Index(out, "Agent wants to run")
	if prompt < 0 {
		t.Fatalf("permission prompt never drawn: %q", out)
	}
	if !strings.Contains(out[prompt:], strings.Repeat(ansiCursorUp+"\r"+ansiClearLine, 4)) {
		t.Errorf("permission prompt rows left on screen: %q", out[prompt:])
	}
}

// startTurn begins a ?? turn on sh for a test that drives one of its steps
// directly, and ends it when the test does.
func startTurn(t *testing.T, sh *Shell) *liveTurn {
	t.Helper()
	tt := sh.beginTurn(context.Background())
	t.Cleanup(tt.end)
	return tt
}

func TestTurn_PhasesFollowTheStream(t *testing.T) {
	sh, _, status := newTurnShell(t, &scriptedTransport{})
	turn := startTurn(t, sh)
	step := func(want turnPhase) {
		t.Helper()
		if got := turn.Phase(); got != want {
			t.Fatalf("phase = %v, want %v", got, want)
		}
	}
	rendered := false
	render := func() { rendered = true }

	step(turnStarting)
	turn.request("explain")
	step(turnWaiting)
	turn.text()
	step(turnStreaming)
	turn.tool(agent.ToolCallUpdate{ID: "t1", Title: "ls", Status: agent.ToolCallInProgress}, "")
	step(turnWaiting)
	turn.text()
	step(turnStreaming)

	if !turn.enterPermission("rm -rf build", render) || !rendered {
		t.Fatal("a streaming turn should draw the permission prompt")
	}
	step(turnPermission)
	turn.text() // buffered behind the prompt
	step(turnPermission)
	turn.exitPermission("t2", false)
	step(turnStreaming) // denied: the reply carries on
	if !strings.HasSuffix(status.String(), rootReport(programstatus.Working, "", "explain")) {
		t.Errorf("after the prompt the status should be working again, got %q", lastStatus(status))
	}

	turn.answered(agent.Response{Type: agent.ResponseTypeCommand, Command: "ls"}, "ls", replyAwaitsKey, ConfirmTypeCommand)
	step(turnConfirming)
	if want := rootReport(programstatus.Blocked, programstatus.Permission, "ls"); lastStatus(status) != want {
		t.Errorf("status = %q, want %q", lastStatus(status), want)
	}
	turn.acted()
	step(turnAtRest)
	if want := rootReport(programstatus.Idle, "", ""); lastStatus(status) != want {
		t.Errorf("status = %q, want %q", lastStatus(status), want)
	}
}

// A permission request that arrives once the turn is no longer waiting on
// the agent (failed, answered and confirming, canceled) has no one to ask:
// it is denied without drawing anything, so no prompt ever shares the
// screen with a confirmation hint.
func TestTurn_LatePermissionIsDeniedWithoutAPrompt(t *testing.T) {
	for name, end := range map[string]func(*liveTurn){
		"failed": func(turn *liveTurn) { turn.failed("boom") },
		"confirming": func(turn *liveTurn) {
			turn.answered(agent.Response{Type: agent.ResponseTypeCommand, Command: "ls"}, "ls", replyAwaitsKey, ConfirmTypeCommand)
		},
		"canceled": func(turn *liveTurn) { turn.canceled("") },
	} {
		t.Run(name, func(t *testing.T) {
			sh, screen, _ := newTurnShell(t, &scriptedTransport{})
			turn := startTurn(t, sh)
			turn.request("x")
			end(turn)

			allow, always := sh.handleToolPermission(context.Background(), agent.ToolPermissionRequest{Command: "ls", ToolName: "Bash"})
			if allow || always {
				t.Errorf("late permission = %v, %v; want denied", allow, always)
			}
			if strings.Contains(screen.String(), "Agent wants to run") {
				t.Errorf("late permission drew a prompt: %q", screen.String())
			}
		})
	}
}

// Outside a ?? turn (a builtin asking the agent) the prompt still shows, but
// nothing is reported: there is no turn to say is working afterwards.
func TestHandleToolPermission_OutsideATurnReportsNothing(t *testing.T) {
	sh, screen, status := newTurnShell(t, &scriptedTransport{})
	sh.handleToolPermission(context.Background(), agent.ToolPermissionRequest{Command: "tool --help", ToolName: "Bash"})
	if !strings.Contains(screen.String(), "Agent wants to run") {
		t.Errorf("no prompt drawn: %q", screen.String())
	}
	if status.String() != "" {
		t.Errorf("status written outside a turn: %q", status.String())
	}
}

func TestTurn_CanceledTurnRestsAndEndsQuietly(t *testing.T) {
	sh, _, status := newTurnShell(t, &scriptedTransport{})
	turn := sh.beginTurn(context.Background())
	turn.request("x")
	turn.interrupt()
	if turn.ctx.Err() == nil {
		t.Fatal("interrupt did not cancel the request")
	}
	turn.canceled("")
	turn.end()
	if got := turn.Phase(); got != turnCanceled {
		t.Errorf("phase = %v, want canceled", got)
	}
	want := rootReport(programstatus.Working, "", "x") + rootReport(programstatus.Idle, "", "")
	if status.String() != want {
		t.Errorf("status = %q, want %q", status.String(), want)
	}
}

// Ctrl+C at a permission prompt cancels the turn, as it does everywhere
// else in a turn; it used to fall into deny, leaving the agent working on.
// The turn's key watcher keeps signals on, so Ctrl+C reaches hash as
// SIGINT, which the turn claimed.
func TestTurn_CtrlCAtAPermissionPromptCancelsTheTurn(t *testing.T) {
	t.Parallel()
	var sh *Shell
	var allowed atomic.Bool
	tr := &scriptedTransport{scripts: []turnScript{{
		events:  []agent.StreamEvent{toolEvent("t1", "rm -rf build", agent.ToolCallPending)},
		waitCtx: true,
		during: func(ctx context.Context) {
			allow, _ := sh.handleToolPermission(ctx, agent.ToolPermissionRequest{Command: "rm -rf build", ToolName: "Bash", ToolCallID: "t1"})
			allowed.Store(allow)
		},
	}}}
	sh, screen, status, tt := newTurnShellOn(t, tr)
	tt.answerPermissions("") // Ctrl+C is SIGINT while the turn watches the keys
	sh.config.Agent.Timeout = "2s"
	go func() {
		for !strings.Contains(screen.String(), "Agent wants to run") {
			time.Sleep(time.Millisecond)
		}
		sh.interrupts.fire()
	}()

	if err := ask(t, sh, "?? clean up"); err != nil {
		t.Fatal(err)
	}
	if allowed.Load() {
		t.Error("Ctrl+C allowed the tool")
	}
	out := screen.String()
	prompt := strings.Index(out, "Agent wants to run")
	if prompt < 0 {
		t.Fatalf("permission prompt never drawn: %q", out)
	}
	if after := out[prompt:]; strings.Contains(after, "deadline") || strings.Contains(after, "thinking") {
		t.Errorf("want the turn canceled, not the agent working on until the timeout: %q", after)
	}
	if sh.last.exitCode != 1 {
		t.Errorf("line exit = %d, want 1 for a canceled turn", sh.last.exitCode)
	}
	if want := rootReport(programstatus.Idle, "", ""); lastStatus(status) != want {
		t.Errorf("last status = %q, want idle", lastStatus(status))
	}
}

// Canceling mid-stream keeps the half-drawn row: it is reply text the user
// was reading, not leftover output. Only a spinner's row is erased.
func TestTurn_CancelKeepsTheStreamedRow(t *testing.T) {
	sh, screen, _ := newTurnShell(t, &scriptedTransport{})
	turn := startTurn(t, sh)
	turn.request("x")
	turn.text()
	sh.agentOutput.WriteStream("12. Use set -e so")
	drawn := len(screen.String())

	turn.interrupt()
	captureStdout(t, sh, func() { turn.canceled("") })
	after := screen.String()[drawn:]
	if strings.Contains(after, "\x1b[K") {
		t.Errorf("cancel erased the streamed row: %q", after)
	}
	if !strings.Contains(after, "\n") || rowOf(sh.agentOutput) != rowFree {
		t.Errorf("the streamed row was not ended before the canceled row: %q", after)
	}
}

// A request that fails mid-reply keeps the half-drawn row, as a canceled
// one does: the error goes on the row after it. The failure erased the
// row, the reply's last words with it.
func TestTurn_FailureKeepsTheStreamedRow(t *testing.T) {
	sh, screen, _ := newTurnShell(t, &scriptedTransport{})
	turn := startTurn(t, sh)
	turn.request("x")
	turn.text()
	sh.agentOutput.WriteStream("12. Use set -e so")
	drawn := len(screen.String())

	var out string
	stdout := captureStdout(t, sh, func() {
		turn.failed("agent crashed")
		out = screen.String()[drawn:]
	})
	if strings.Contains(out, "\x1b[K") {
		t.Errorf("the failure erased the streamed row: %q", out)
	}
	errAt := strings.Index(out, "agent crashed")
	if stdout != "" || errAt < 0 || strings.Count(out[:errAt], "\n") != 1 {
		t.Errorf("the streamed row was not ended once before the error: screen %q, stdout %q", out, stdout)
	}
	if errAt < 0 {
		t.Errorf("no error drawn: %q", out)
	}
}

// A request that fails before any reply text (an empty reply, a timeout)
// takes the spinner's row down before the error: the error drew on the
// spinner's last frame, "agent · thinking✗ ...".
func TestTurn_FailureBeforeTextErasesTheSpinnerRow(t *testing.T) {
	sh, screen, _ := newTurnShell(t, &scriptedTransport{})
	turn := startTurn(t, sh)
	turn.request("x")
	waitFor(t, func() bool { return strings.Contains(screen.String(), "thinking") })
	drawn := len(screen.String())

	stdout := captureStdout(t, sh, func() { turn.failed("agent ended the turn without text") })
	after := screen.String()[drawn:]
	errAt := strings.Index(after, "✗")
	if errAt < 0 {
		t.Fatalf("no error drawn: %q", after)
	}
	if !strings.Contains(after[:errAt], "\x1b[K") {
		t.Errorf("the spinner's row was not erased before the error: %q", after)
	}
	if stdout != "" {
		t.Errorf("the failure drew %q outside the turn's screen", stdout)
	}
}

// A follow-up request (the user replied in the conversation) that fails
// before any text draws its error on the row after the first reply: the
// first reply's unfinished row was ended when it was answered, and ending
// it again drew a blank row.
func TestTurn_FollowUpFailureDrawsNoBlankRow(t *testing.T) {
	sh, screen, _ := newTurnShell(t, &scriptedTransport{})
	turn := startTurn(t, sh)
	turn.request("x")
	turn.text()
	sh.agentOutput.WriteStream("This is a prose answer.")
	drawn := len(screen.String())
	stdout := captureStdout(t, sh, func() {
		turn.answered(agent.Response{}, "This is a prose answer.", replyAsksBack, ConfirmTypeExplanation)
	})
	turn.request("follow up")
	stdout += captureStdout(t, sh, func() { turn.failed("agent crashed") })
	after := screen.String()[drawn:]
	errAt := strings.Index(after, "✗")
	if errAt < 0 {
		t.Fatalf("no error drawn: %q", after)
	}
	if rows := strings.Count(stdout, "\n") + strings.Count(after[:errAt], "\n"); rows != 1 {
		t.Errorf("rows ended between the reply and the follow-up's error = %d, want 1 (the reply's own): screen %q, stdout %q", rows, after, stdout)
	}
}

// A command the user edits into a ?? line starts a new turn once the first
// one handed off: the new turn is the only one the shell knows, and the
// first is over (its ctx canceled, Ctrl+C no longer its own).
func TestTurn_EditedIntoARequestStartsAFreshTurn(t *testing.T) {
	t.Parallel()
	type seen struct {
		first, inner *liveTurn
		phase        turnPhase // the first turn's, while the second runs
	}
	var sh *Shell
	firsts, seconds := make(chan *liveTurn, 1), make(chan seen, 1)
	tr := &scriptedTransport{scripts: []turnScript{
		{events: []agent.StreamEvent{textEvent("```bash\nls -la\n```")}, during: func(context.Context) { firsts <- sh.turn.Load() }},
		{events: []agent.StreamEvent{textEvent("It lists files.")}},
	}}
	tr.scripts[1].during = func(context.Context) {
		first := <-firsts
		seconds <- seen{first: first, inner: sh.turn.Load(), phase: first.Phase()}
	}
	sh, _, _ = newTurnShell(t, tr, keyEdit+keyKillAll+"?? what does ls -la do\r", keyRun)

	var err error
	captureStdout(t, sh, func() { err = ask(t, sh, "?? list files") })
	if err != nil {
		t.Fatal(err)
	}
	var got seen
	select {
	case got = <-seconds:
	case <-time.After(2 * time.Second):
		t.Fatal("the edited request never ran")
	}
	if got.inner == got.first || got.inner == nil {
		t.Error("the edited request did not get its own turn")
	}
	if got.phase != turnHandedOff {
		t.Errorf("first turn during the second = %v, want handed off", got.phase)
	}
	if got.first.ctx.Err() == nil {
		t.Error("first turn's request context still live")
	}
	if sh.turn.Load() != nil {
		t.Error("a turn is left after both ended")
	}
	if got := tr.requestCount(); got != 2 {
		t.Errorf("requests = %d, want 2", got)
	}
}

// A turn begun while another is still in flight takes over: the outer one
// is handed off, not left holding Ctrl+C beside it.
func TestTurn_BeginHandsAnOuterTurnOff(t *testing.T) {
	sh, _, _ := newTurnShell(t, &scriptedTransport{})
	outer := sh.beginTurn(context.Background())
	inner := startTurn(t, sh)
	if outer.Phase() != turnHandedOff || outer.ctx.Err() == nil {
		t.Errorf("outer turn = %v, ctx err %v; want handed off and canceled", outer.Phase(), outer.ctx.Err())
	}
	if sh.turn.Load() != inner {
		t.Error("the inner turn is not the shell's turn")
	}
}

// Declining a complete reply at its hint (Esc, or Ctrl+C, which the hint
// reads as the same key: TestWaitForConfirmation_KeysAnswerTheHint) leaves
// the ?? line at 0: the agent's work finished, and the line's status says
// that, not which key the user left with. Ctrl+C while the request still
// runs cancels it and the line exits 1 (TestTurn_CtrlCAtAPermissionPromptCancelsTheTurn,
// TestInlineTurn_TheLineStatusFollowsTheRequest).
func TestTurn_DecliningACompleteReplyLeavesTheLineAtZero(t *testing.T) {
	t.Parallel()
	for name, reply := range map[string]string{"command": "ls -la", "explanation": "It lists files."} {
		t.Run(name, func(t *testing.T) {
			tr := &scriptedTransport{scripts: []turnScript{{events: []agent.StreamEvent{textEvent(reply)}}}}
			sh, _, status := newTurnShell(t, tr, keyDecline)
			if err := ask(t, sh, "?? list files"); err != nil {
				t.Fatal(err)
			}
			if sh.last.exitCode != 0 || sh.last.kind != agentLine {
				t.Errorf("last = %+v, want the ?? line at 0", sh.last)
			}
			if want := rootReport(programstatus.Idle, "", ""); lastStatus(status) != want {
				t.Errorf("status = %q, want idle", lastStatus(status))
			}
		})
	}
}

// A ?? line whose command the user opens with Tab and then abandons ran
// nothing: it is a declined reply, 0, not the stale status of the line
// before (false). Leaving the editor (Ctrl+C, Ctrl+D) and submitting it
// empty abandon the edit.
func TestTurn_AbandonedEditIsADecline(t *testing.T) {
	t.Parallel()
	for name, keys := range map[string]string{
		"canceled": "\x03\x03", // the first clears the line
		"eof":      keyKillAll + "\x04",
		"empty":    keyKillAll + "  \r",
	} {
		t.Run(name, func(t *testing.T) {
			tr := &scriptedTransport{scripts: []turnScript{{events: []agent.StreamEvent{textEvent("```bash\necho hi\n```")}}}}
			sh, _, status := newTurnShell(t, tr, keyEdit+keys)
			captureStdout(t, sh, func() { _ = sh.handleLine(context.Background(), "false") })
			captureStdout(t, sh, func() { _ = sh.handleLine(context.Background(), "?? say hi") })
			if got := dollarQuestion(t, sh); got != "0" || sh.last.command != "?? say hi" || sh.last.kind != agentLine {
				t.Errorf("$? = %s, last = %+v; want 0 for the ?? line", got, sh.last)
			}
			if want := rootReport(programstatus.Idle, "", ""); lastStatus(status) != want {
				t.Errorf("status = %q, want idle", lastStatus(status))
			}
		})
	}
}

// An editor error while the user edits the turn's command fails the line,
// as in the inline form: the edit's outcome moves the turn, though it let
// go of the terminal when the edit began.
func TestTurn_AnEditorErrorFailsTheEdit(t *testing.T) {
	sh, screen, status := newTurnShell(t, &scriptedTransport{})
	turn := startTurn(t, sh)
	turn.request("say hi")
	turn.answered(agent.Response{Type: agent.ResponseTypeCommand, Command: "echo hi"}, "echo hi", replyAwaitsKey, ConfirmTypeCommand)
	turn.editing()
	turn.editorFailed(errors.New("tty gone"))
	if code, ok := turn.lineExitCode(); !ok || code != 1 {
		t.Errorf("line exit = %d, %v; want 1", code, ok)
	}
	if !strings.Contains(screen.String(), "editor error: tty gone") || !strings.Contains(lastStatus(status), "state=error") {
		t.Errorf("screen %q, status %q; want the editor error shown and reported", screen.String(), lastStatus(status))
	}
}

// An edit of the turn's command reports hash idle once, whatever the edit
// ends in: the turn let go of the terminal when the edit began, so a
// decline or a hand-off has nothing new to say.
func TestTurn_AnEditReportsIdleOnce(t *testing.T) {
	for name, settle := range map[string]func(*liveTurn){
		"declined":  func(turn *liveTurn) { turn.acted() },
		"submitted": func(turn *liveTurn) { turn.handOff() },
	} {
		t.Run(name, func(t *testing.T) {
			sh, _, status := newTurnShell(t, &scriptedTransport{})
			turn := sh.beginTurn(context.Background())
			turn.request("list files")
			turn.answered(agent.Response{Type: agent.ResponseTypeCommand, Command: "ls"}, "ls", replyAwaitsKey, ConfirmTypeCommand)
			turn.editing()
			settle(turn)
			turn.end()
			if n := strings.Count(status.String(), rootReport(programstatus.Idle, "", "")); n != 1 {
				t.Errorf("reported idle %d times: %q", n, status.String())
			}
		})
	}
}

// A closed turn's phase is its outcome: an event that arrives after it
// (a late failure, a cancel, an answer) changes nothing, draws nothing and
// reports nothing.
func TestTurn_EventsAfterCloseChangeNothing(t *testing.T) {
	sh, screen, status, _ := newTurnShellOn(t, &scriptedTransport{}) // a terminal: the request watches keys
	turn := sh.beginTurn(context.Background())
	turn.request("list files")
	turn.answered(agent.Response{Type: agent.ResponseTypeCommand, Command: "ls"}, "ls", replyAwaitsKey, ConfirmTypeCommand)
	turn.handOff()
	drawn, reported := screen.String(), status.String()

	turn.failed("boom")
	turn.editorFailed(errors.New("read error"))
	turn.canceled("")
	turn.acted()
	turn.replying("more?")
	turn.text()
	turn.tool(agent.ToolCallUpdate{ID: "t1", Title: "ls", Status: agent.ToolCallInProgress}, "")
	turn.answered(agent.Response{Type: agent.ResponseTypeExplanation, Explanation: "x"}, "x", replyStands, 0)
	turn.request("again")
	if ctx := turn.requestInline("again"); ctx.Err() == nil {
		t.Error("a closed turn's inline request runs")
	}
	turn.inlineEnded("ls -a", nil)
	turn.inlineLeft()
	if turn.enterPermission("rm -rf /", func() { t.Error("a closed turn drew a permission prompt") }) {
		t.Error("a closed turn entered a permission prompt")
	}
	if _, _, ok := turn.claimKey(nil); ok {
		t.Error("a closed turn claimed the key for a permission prompt")
	}
	turn.deniedUnasked("t2", "rm -rf /")
	turn.exitPermission("t2", true)
	turn.noteDenied("rm -rf /")
	if action := turn.confirm(ConfirmTypeCommand); action != ConfirmEnded {
		t.Errorf("a closed turn's hint = %v, want ended", action)
	}
	turn.end()

	if got := turn.Phase(); got != turnHandedOff {
		t.Errorf("phase = %v, want handed off", got)
	}
	if len(sh.agentOutput.tools) > 0 {
		t.Errorf("a closed turn recorded tool updates %+v", sh.agentOutput.tools)
	}
	if len(turn.denied) > 0 {
		t.Errorf("a closed turn noted denials %v", turn.denied)
	}
	if screen.String() != drawn || status.String() != reported {
		t.Errorf("a closed turn drew %q and reported %q", screen.String()[len(drawn):], status.String()[len(reported):])
	}
	if _, ok := turn.lineExitCode(); ok {
		t.Error("the handed-off turn's line got an exit code of its own")
	}
}

// The shell ending (a fatal signal) reaches the turn as an event before its
// context ends, so whatever sees the context end finds the turn ending: no
// canceled row, no error, and hash reported idle once.
func TestTurn_TheShellEndingSettlesTheTurnSilently(t *testing.T) {
	sh, screen, status := newTurnShell(t, &scriptedTransport{})
	parent, endShell := context.WithCancel(context.Background())
	turn := sh.beginTurn(parent)
	turn.request("count files")
	endShell()
	<-turn.ctx.Done()
	if got := turn.Phase(); got != turnCanceled {
		t.Errorf("phase = %v once the shell ended, want canceled", got)
	}

	turn.canceled("") // the request path sees its context end
	turn.failed("context canceled")
	turn.editorFailed(context.Canceled)
	turn.end()

	if out := screen.String(); strings.Contains(out, "canceled") || strings.Contains(out, "✗") {
		t.Errorf("the shell ending drew %q", out)
	}
	want := rootReport(programstatus.Working, "", "count files") + rootReport(programstatus.Idle, "", "")
	if status.String() != want {
		t.Errorf("status = %q, want %q", status.String(), want)
	}
	if code, ok := turn.lineExitCode(); !ok || code != 128+int(syscall.SIGTERM) {
		t.Errorf("line exit = %d, %v; want 143: the shell's ending cut the line off", code, ok)
	}
}

// An edit's editor runs on a context only the shell's ending cancels:
// the turn let go of Ctrl+C and its request, and the edit outlives them.
func TestTurn_AnEditRunsUntilTheShellEnds(t *testing.T) {
	sh, _, _ := newTurnShell(t, &scriptedTransport{})
	parent, endShell := context.WithCancel(context.Background())
	turn := sh.beginTurn(parent)
	t.Cleanup(turn.end)
	turn.request("list files")
	turn.answered(agent.Response{Type: agent.ResponseTypeCommand, Command: "ls"}, "ls", replyAwaitsKey, ConfirmTypeCommand)
	edit := turn.editing()
	if sh.turn.Load() != nil {
		t.Error("the turn kept the shell's current-turn slot while the user edits")
	}
	if edit.Err() != nil {
		t.Fatal("the edit's context ended before the shell did")
	}
	endShell()
	select {
	case <-edit.Done():
	case <-time.After(time.Second):
		t.Fatal("the shell ended and the edit's editor went on")
	}
}

// A pipe command the shell's ending cuts off is not a failed pipe: nothing
// is drawn for it on the way out.
func TestEnding_APipeCommandTheShellEndsDrawsNoFailure(t *testing.T) {
	t.Parallel()
	sh, screen, _ := newTurnShell(t, &scriptedTransport{})
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	out := captureStdout(t, sh, func() { _ = sh.handleLine(ctx, "sleep 5 | ?? summarize") })
	if drawn := screen.String() + out; strings.Contains(drawn, "pipe command failed") {
		t.Errorf("the shell ending drew a failed pipe: %q", drawn)
	}
}

// A turn starts with no reply row of its own: a pipe whose command could not
// run fails before any request, and it ended the row of the turn before
// (a reply that stopped mid-row), so its error went on a row of its own
// instead of after the turn's label.
func TestTurn_StartsWithoutTheLastTurnsRow(t *testing.T) {
	sh, _, _ := newTurnShell(t, &scriptedTransport{})
	sh.agentOutput.WriteStream("the last turn's reply, cut off mid-row")
	out := captureStdout(t, sh, func() { _ = sh.handleLine(context.Background(), "echo ) | ?? summarize") })
	if strings.Contains(out, "\n") {
		t.Errorf("the failed pipe ended a row it never drew: %q", out)
	}
}

// A pipe command that does not parse is reported as the user typed it: the
// shell runs it wrapped in a subshell, `( cmd\n)`, and the error named the
// wrapper's line and columns, and the wrapper's own complaint.
func TestTurn_APipeCommandsParseErrorIsTheUsers(t *testing.T) {
	sh, screen, _ := newTurnShell(t, &scriptedTransport{})
	captureStdout(t, sh, func() { _ = sh.handleLine(context.Background(), "echo ) | ?? summarize") })
	want := "pipe command failed: hash:1:6: a command can only contain words and redirects; encountered `)`"
	if !strings.Contains(screen.String(), want) {
		t.Errorf("screen %q, want %q", screen.String(), want)
	}
}
