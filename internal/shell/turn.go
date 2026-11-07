package shell

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"syscall"

	"github.com/tfcace/hash/internal/agent"
	"github.com/tfcace/hash/internal/programstatus"
)

// turnPhase is where a ?? turn is. One turn spans the request, its retries,
// the confirmation, and a conversation's follow-ups; it ends when the user
// acts on the last reply or hands a command to the shell. The phases come
// in three groups: the agent works (working), the turn waits for the user,
// and the turn rests, which says how its last request ended. A resting
// turn takes another request (a retry, a follow-up) until it closes.
type turnPhase int

const (
	turnStarting turnPhase = iota // preflight and pipe capture; nothing reported yet

	// The agent works.
	turnWaiting    // the spinner shows it: thinking, or a tool running
	turnStreaming  // reply text is drawing; no spinner
	turnPermission // a tool permission prompt owns the screen and stdin

	// The turn waits for the user.
	turnConfirming // the reply is complete; a hint waits for a key
	turnReplying   // the conversation's reply editor is open
	turnEditing    // the user edits the turn's command; the turn holds nothing meanwhile

	// The turn rests.
	turnDone      // the reply is complete and needs nothing from the user
	turnFailed    // the request failed, timed out or came back empty
	turnCanceled  // the user canceled a request (Ctrl+C, Esc)
	turnAtRest    // the user acted on the reply, or left the conversation
	turnHandedOff // a command the turn produced took over the line
	turnCutOff    // Ctrl+C interrupted the command of `cmd | ?? prompt` as a line: no request went, and the line ends as one Ctrl+C ended
)

// working reports whether the agent works in phase p: the spinner's row and
// the progress bar are the turn's.
func (p turnPhase) working() bool {
	return p == turnWaiting || p == turnStreaming || p == turnPermission
}

// keepsStatus reports whether a turn that ends in phase p leaves the status
// it reported as it is: a done or failed turn's, meant to outlive it, and
// one that reported none.
func (p turnPhase) keepsStatus() bool {
	return p == turnDone || p == turnFailed || p == turnStarting
}

// turnRequest is the request a turn has in flight. Its type is the turn's
// form, how the reply reaches the user, which decides who owns stdin and the
// screen while it runs: a fullRequest or an inlineRequest. A turn's first
// request fixes its form; there is none before it.
type turnRequest interface {
	// stop releases what the request holds once it is over. Safe to repeat.
	stop()
}

// fullRequest is a full turn's request: the reply draws below the line, and
// the turn's key watcher owns stdin while it runs.
type fullRequest struct {
	keys *streamKeyWatcher // Esc, and the key a permission prompt asks for; nil when stdin is not a terminal
}

func (r *fullRequest) stop() { r.keys.Stop() }

// inlineRequest is an inline turn's request: the reply is ghost text in the
// line editor, which owns stdin and the screen.
type inlineRequest struct {
	stopFill context.CancelCauseFunc // ends the fill with the reason
}

func (r *inlineRequest) stop() { r.stopFill(nil) }

// idle reports whether the turn has reported hash idle by the time it is
// in phase p: leaving p for rest has nothing more to report.
func (p turnPhase) idle() bool {
	switch p {
	case turnCanceled, turnAtRest, turnEditing, turnHandedOff, turnCutOff:
		return true
	}
	return false
}

// turnLife is where the turn is in its life, beside its phase: whether its
// events still move it and draw what they show.
type turnLife int

const (
	turnOpen   turnLife = iota // events move the phase and draw
	turnEnding                 // the shell is ending (a fatal signal): the turn was settled silently, and its events change and draw nothing; final, so closing keeps it (lineExitCode)
	turnClosed                 // the turn let go of everything; its phase is its outcome, and its events change and draw nothing
)

var turnPhaseNames = [...]string{
	turnStarting: "starting", turnWaiting: "waiting", turnStreaming: "streaming", turnPermission: "permission",
	turnConfirming: "confirming", turnReplying: "replying", turnEditing: "editing",
	turnDone: "done", turnFailed: "failed", turnCanceled: "canceled", turnAtRest: "at rest",
	turnHandedOff: "handed off", turnCutOff: "cut off",
}

func (p turnPhase) String() string {
	if p < 0 || int(p) >= len(turnPhaseNames) {
		return "unknown"
	}
	return turnPhaseNames[p]
}

// liveTurn owns a ?? turn while it runs: its phase, and with it the spinner,
// the OSC 9;4 progress bar, the OSC 7501 status, the Esc watcher and Ctrl+C.
// Each event the turn can see is one method, so what the terminal shows
// follows from the phase. The permission handler calls in from the agent's
// goroutine, so the phase is guarded by mu.
type liveTurn struct {
	s       *Shell
	parent  context.Context    // the shell's context: what a handed-off command runs on
	ctx     context.Context    // canceled by Ctrl+C or Esc, once ending settled the turn, and when the turn closes
	cancel  context.CancelFunc // cancels ctx
	release func()             // gives Ctrl+C back to the shell
	unwatch func() bool        // stops watching parent for the shell ending
	facts   requestFacts       // what every request of the turn tells the agent beside its prompt

	mu       sync.Mutex
	life     turnLife
	phase    turnPhase
	prompt   string          // what the agent works on, for the status
	resumeTo turnPhase       // the phase an answered permission prompt returns to
	req      turnRequest     // the request in flight, or the last one; nil before the first
	denied   map[string]bool // commands the user denied at a permission prompt
}

// beginTurn starts a ?? turn on the shell's context and makes it the one the
// permission handler and Ctrl+C reach. Turns do not nest: a ?? line the
// user makes of a turn's command (an edit, an inline fill) runs after that
// turn handed off, so there is no turn left when the next one begins. One
// still in flight is handed off here, so it can never share the screen,
// stdin or Ctrl+C with the new one.
func (s *Shell) beginTurn(parent context.Context) *liveTurn {
	if outer := s.turn.Load(); outer != nil {
		outer.handOff()
	}
	// The shell ending reaches the turn as an event (ending), which
	// cancels ctx only once the turn knows: whatever sees ctx end then
	// finds the turn ending, never a turn that would draw as if the user
	// had canceled it.
	ctx, cancel := context.WithCancel(context.WithoutCancel(parent))
	t := &liveTurn{s: s, parent: parent, ctx: ctx, cancel: cancel}
	s.agentOutput.BeginTurn()
	t.release = s.interrupts.claim(t.interrupt)
	t.unwatch = context.AfterFunc(parent, t.ending)
	s.turn.Store(t)
	return t
}

// Phase reports where the turn is.
func (t *liveTurn) Phase() turnPhase {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.phase
}

// request starts a request on prompt: the spinner shows the agent thinking,
// and Esc is watched from before the request goes out, so a permission
// prompt that comes first finds the watcher to pause.
func (t *liveTurn) request(prompt string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.life != turnOpen {
		return
	}
	s := t.s
	t.prompt = prompt
	t.enterWorkingLocked(turnWaiting)
	s.agentOutput.SetAgentModel(s.agentHandler.CurrentModel())
	s.agentOutput.ShowState(AgentStateThinking)
	s.agentOutput.BeginRequest()
	t.stopRequestLocked() // a retry's request: the previous one's watcher is done
	t.req = &fullRequest{keys: s.keyboard().watch(t.interrupt)}
}

// requestInline starts an inline request and returns the context it runs
// on: its reply is ghost text in the editor, so there is no spinner and no
// Esc watcher.
func (t *liveTurn) requestInline(prompt string) context.Context {
	t.mu.Lock()
	defer t.mu.Unlock()
	ctx, stop := context.WithCancelCause(t.ctx)
	if t.life != turnOpen {
		stop(nil)
		return ctx
	}
	t.prompt = prompt
	t.enterWorkingLocked(turnWaiting)
	t.s.agentOutput.BeginRequest()
	t.req = &inlineRequest{stopFill: stop}
	return ctx
}

// inlineToolDenied is why an inline fill stopped when its agent asked to run
// a tool (stopFillForToolLocked).
func inlineToolDenied(command string) error {
	return fmt.Errorf("wanted to run %s; use ?? for tools", truncateTerminalText(sanitizeTerminalText(command), 32))
}

// inlineEnded is an inline request's fill stream ending while the user
// edits the line, line being the line with the fill as the ghost shows it.
// Nothing is drawn (the editor shows the fill, or a notice for an error);
// the work is over, so the progress bar goes, and the status says what the
// line waits for. A turn that moved on already (the user canceled, or
// accepted the line) is left as it is.
func (t *liveTurn) inlineEnded(line string, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.life != turnOpen || t.phase != turnWaiting {
		return
	}
	t.stopWorkingLocked()
	switch {
	case err == nil:
		t.phase = turnConfirming // the fill waits for Enter, an edit or Ctrl+C
		t.s.agentStatus.blocked(programstatus.Permission, line)
	case errors.Is(err, context.Canceled):
		t.phase = turnCanceled
		t.s.agentStatus.idle()
	default:
		t.phase = turnFailed
		t.s.agentStatus.failed(agentStreamErrorMessage(err))
	}
}

// inlineLeft is the user leaving the inline line without running it: a
// request still running is canceled (the line exits 1, as when typing over
// the streaming ghost canceled it); a fill already there was declined (the
// line exits 0: the agent's work was done).
func (t *liveTurn) inlineLeft() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.life != turnOpen {
		return
	}
	switch t.phase {
	case turnWaiting:
		t.cancel()
		t.restLocked()
		t.phase = turnCanceled
	case turnConfirming:
		t.restLocked()
	}
}

// enterWorkingLocked moves into a phase where the agent works, reporting it
// when the turn was not working already.
func (t *liveTurn) enterWorkingLocked(p turnPhase) {
	if !t.phase.working() {
		t.s.agentStatus.working(t.prompt)
		t.s.responseUI.StartProgress()
	}
	t.phase = p
}

// text is called before reply text is drawn. Leaving the spinner (thinking,
// or a tool that just ran) stops it and takes its row back, every time, so
// text after a tool never streams under a spinner still redrawing its row.
// During a permission prompt the coordinator holds the text, and the prompt
// hands back to streaming.
func (t *liveTurn) text() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.life != turnOpen {
		return
	}
	switch t.phase {
	case turnWaiting:
		t.s.agentOutput.TakeRow()
		t.phase = turnStreaming
	case turnPermission:
		t.resumeTo = turnStreaming
	}
}

// tool shows an agent tool update: a running tool on the spinner, a finished
// one as its durable row. Updates that arrive once the reply is over are
// recorded, not drawn; once the turn closed they are dropped.
func (t *liveTurn) tool(update agent.ToolCallUpdate, prefix string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.toolLocked(update, prefix)
}

func (t *liveTurn) toolLocked(update agent.ToolCallUpdate, prefix string) {
	if t.life != turnOpen {
		return
	}
	s := t.s
	merged, finalized := s.agentOutput.RecordToolUpdate(update)
	switch t.phase {
	case turnWaiting, turnStreaming:
	case turnPermission:
		// The prompt owns the screen; a finished row waits in the
		// coordinator's buffer, and the prompt hands back to the spinner.
		if finalized {
			s.agentOutput.RenderToolResult(merged, prefix)
		}
		t.resumeTo = turnWaiting
		return
	default:
		return
	}
	t.phase = turnWaiting
	if finalized {
		s.agentOutput.RenderToolResult(merged, prefix)
		s.agentOutput.ShowState(AgentStateThinking)
		return
	}
	s.agentOutput.ShowActivity(agentToolActivityLabel(merged))
}

// enterPermission gives the screen to a permission prompt drawn by render,
// which takes the spinner's row; the spinner stays stopped until the prompt
// is answered. Only a full request asks, once
// claimKey found its key: an inline one is denied unasked. It reports
// false, drawing nothing, when the turn no longer waits on the agent, so
// the caller denies.
func (t *liveTurn) enterPermission(waitingFor string, render func()) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.workingLocked() {
		return false
	}
	t.resumeTo = t.phase
	t.phase = turnPermission
	t.s.agentStatus.blocked(programstatus.Permission, waitingFor)
	render()
	return true
}

// stopFillForToolLocked stops an inline fill whose agent asked to run a
// tool: the editor owns the terminal, so no one can be asked, and the ghost
// names the way that can.
func (t *liveTurn) stopFillForToolLocked(inline *inlineRequest, waitingFor string) {
	err := inlineToolDenied(waitingFor)
	t.stopWorkingLocked()
	t.phase = turnFailed
	t.s.agentStatus.failed(err.Error())
	inline.stopFill(err)
}

// claimKey claims the key that answers a permission prompt from the
// request's key watcher, which owns the terminal's mode while the request
// runs, so the prompt never sets or restores it. It reports false when no
// key can answer: the request is inline (the editor owns stdin), or stdin
// is not a terminal (deniedUnasked), or the turn is no longer open. note
// draws on the prompt that a key came too soon to answer it.
func (t *liveTurn) claimKey(note func()) (wait func(context.Context) byte, release func(), ok bool) {
	t.mu.Lock()
	var keys *streamKeyWatcher
	if full, isFull := t.req.(*fullRequest); isFull && t.life == turnOpen {
		keys = full.keys
	}
	t.mu.Unlock()
	if keys == nil {
		return nil, nil, false
	}
	ask := keys.Ask(note)
	return ask.Wait, ask.release, true
}

// deniedUnasked denies a tool permission no key can answer (claimKey)
// without drawing a prompt: an inline fill stops, saying why, and a full
// turn shows the tool denied, with why, where the prompt would have been.
// The agent works on, so the turn stays where it was.
func (t *liveTurn) deniedUnasked(toolCallID, waitingFor string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.workingLocked() {
		return
	}
	if inline, ok := t.req.(*inlineRequest); ok {
		t.stopFillForToolLocked(inline, waitingFor)
		return
	}
	title := truncateTerminalText(sanitizeTerminalText(waitingFor), 64) + " · no terminal to ask"
	t.toolLocked(agent.ToolCallUpdate{ID: toolCallID, Title: title, PermissionDenied: true}, "")
}

// exitPermission takes the screen back from an answered permission prompt
// and shows what the agent does next. When the turn moved on while the
// prompt was up (canceled, failed, timed out), the prompt is already gone
// and nothing is redrawn or reported; a request canceled at the prompt is
// left for the turn's cancel to take down.
func (t *liveTurn) exitPermission(toolCallID string, allowed bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.life != turnOpen || t.phase != turnPermission || t.ctx.Err() != nil {
		return
	}
	s := t.s
	s.agentOutput.ClearPermissionPrompt()
	t.phase = turnWaiting
	s.agentStatus.working(t.prompt)
	update, finalized := s.agentOutput.ResolveToolPermission(toolCallID, allowed)
	switch {
	case finalized:
		s.agentOutput.RenderToolResult(update, "")
		s.agentOutput.ShowState(AgentStateThinking)
	case allowed:
		s.agentOutput.ShowActivity(agentToolActivityLabel(update))
	case t.resumeTo == turnStreaming:
		t.phase = turnStreaming
	default:
		s.agentOutput.ShowState(AgentStateThinking)
	}
}

// interrupt is Ctrl+C or Esc while the turn runs. It only cancels the
// request: it runs on the signal or key goroutine, and the screen is the
// turn's to clean up, once the request path sees the cancel (canceled).
func (t *liveTurn) interrupt() {
	t.cancel()
}

// streamEnded is called once the reply stream is over, whatever its outcome:
// the Esc watcher stops before anything else reads stdin.
func (t *liveTurn) streamEnded() {
	t.mu.Lock()
	req := t.req
	t.mu.Unlock()
	if req != nil {
		req.stop()
	}
}

// canceled ends a request the user canceled: a permission prompt still up
// goes with what it held, the spinner's row is cleared, and what the reply
// already drew stays.
func (t *liveTurn) canceled(prefix string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.life != turnOpen {
		return
	}
	t.takeDownLocked()
	showTurnCanceled(t.s.out(), prefix)
	t.restLocked()
	t.phase = turnCanceled
}

// cutOff ends a turn whose pipe command Ctrl+C interrupted, before it
// reported anything: it draws nothing, as a line Ctrl+C ends draws nothing
// of its own.
func (t *liveTurn) cutOff() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.life != turnOpen {
		return
	}
	t.restLocked()
	t.phase = turnCutOff
}

// takeDownLocked takes what a stopped request left on screen down: a
// permission prompt still up, with what it held, and the spinner's row. What
// the reply already drew stays.
func (t *liveTurn) takeDownLocked() {
	if t.phase == turnPermission {
		t.s.agentOutput.DismissPermissionPrompt()
	}
	t.s.agentOutput.TakeRow()
}

// ending is the shell ending (a fatal signal ended its context) while the
// turn is open. What the turn was doing stops because hash is going, not
// because the user canceled it or it failed: the turn is taken down
// silently, as a canceled one without its row, and only then is its context
// canceled, so the request and the editors that see it end find the turn
// ending and draw nothing more for it.
func (t *liveTurn) ending() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.life != turnOpen {
		return
	}
	switch {
	case t.phase.working():
		t.takeDownLocked()
		t.restLocked()
		t.phase = turnCanceled
	case t.phase.keepsStatus(): // as end does
	default:
		t.restLocked()
	}
	t.life = turnEnding
	t.cancel()
}

// editorFailed fails a turn whose editor (an inline line, an edit of its
// command) returned err. One the shell's ending stopped finds the turn
// ending, and draws nothing.
func (t *liveTurn) editorFailed(err error) {
	t.failed(fmt.Sprintf("editor error: %v", err))
}

// failed ends a request on an error. A permission prompt still up (the
// request timed out under it) is taken off the screen first; the error
// takes the spinner's row, and a reply cut off mid-row keeps that row, as a
// canceled one does (TakeRow).
func (t *liveTurn) failed(msg string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.life != turnOpen {
		return
	}
	s := t.s
	if t.phase == turnPermission {
		s.agentOutput.DismissPermissionPrompt()
	}
	t.stopWorkingLocked()
	t.phase = turnFailed
	s.agentOutput.TakeRow()
	s.responseUI.ShowError(msg)
	s.agentStatus.failed(msg)
}

// answered ends a request whose reply is complete: the reply's last row is
// ended, then the turn is done, waits at a confirmation hint, or (a question
// inside a conversation) asks the user for the next message.
func (t *liveTurn) answered(resp agent.Response, responseText string, next replyNext, confirmType ConfirmationType) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.life != turnOpen {
		return
	}
	s := t.s
	if t.phase == turnPermission {
		s.agentOutput.DismissPermissionPrompt()
	}
	t.stopWorkingLocked()
	s.agentOutput.TakeRow() // the footer starts on a row of its own
	switch next {
	case replyStands:
		t.phase = turnDone
		s.agentStatus.done(firstNonEmptyLine(responseText))
	case replyAsksBack:
		t.replyingLocked(responseText)
	default:
		t.phase = turnConfirming
		s.reportConfirmationWait(confirmType, resp, responseText)
	}
}

// confirm shows the hint for confirmType and waits for the key that answers
// it. A failed turn keeps its error status while it offers a retry. Once
// the shell is ending it is ConfirmEnded: nothing is drawn, and the caller
// neither acts on the reply nor records it. A wait that ended while the
// turn is still open (a SIGINT canceled it) declines, as Ctrl+C does.
func (t *liveTurn) confirm(confirmType ConfirmationType) ConfirmAction {
	if !t.open() {
		return ConfirmEnded
	}
	s := t.s
	s.agentOutput.ShowHints(confirmType)
	action := s.keyboard().confirm(t.ctx, confirmType)
	if action == ConfirmEnded {
		if !t.open() {
			return ConfirmEnded
		}
		action = ConfirmCancel
	}
	fmt.Fprintln(t.s.out())
	return action
}

// open reports whether the turn's events still move it and draw.
func (t *liveTurn) open() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.life == turnOpen
}

// replying opens the conversation: the user is asked for the next message.
func (t *liveTurn) replying(lastAssistantText string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.replyingLocked(lastAssistantText)
}

func (t *liveTurn) replyingLocked(lastAssistantText string) {
	if t.life != turnOpen || t.phase == turnReplying {
		return
	}
	t.phase = turnReplying
	t.s.agentStatus.blocked(programstatus.Question, lastNonEmptyLine(lastAssistantText))
}

// acted says the user acted on the reply: hash is at rest.
func (t *liveTurn) acted() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.life != turnOpen {
		return
	}
	t.restLocked()
}

// handOff ends the turn so a command it produced (run, edited, or accepted
// inline) runs as if typed: no spinner, progress bar or Esc watcher left, Ctrl+C
// back with the shell, and the request canceled. It returns the context the
// command runs on, the shell's, which Ctrl+C never cancels.
func (t *liveTurn) handOff() context.Context {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.life == turnOpen {
		t.restLocked()
		t.phase = turnHandedOff
	}
	t.closeLocked()
	return t.parent
}

// editing gives the turn's command to the user to edit: the editor owns
// stdin, the screen and Ctrl+C, so the turn lets go of them as a hand-off
// does, and returns the context the editor runs on, which only the shell's
// ending cancels. The turn stays open for the edit's outcome: a hand-off
// when the user submits a line, else the reply was declined (acted) or the
// editor failed (editorFailed).
func (t *liveTurn) editing() context.Context {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.life == turnOpen {
		t.restLocked()
		t.phase = turnEditing
		t.letGoLocked()
	}
	return t.ctx
}

// end closes the turn on whatever path it took. A done or failed turn keeps
// its status, meant to outlive the turn; any other is reported idle, once.
func (t *liveTurn) end() {
	t.mu.Lock()
	defer t.mu.Unlock()
	switch {
	case t.life != turnOpen:
	case t.phase.keepsStatus(), t.phase.idle():
	default:
		t.restLocked()
	}
	t.closeLocked()
}

// lineExitCode is the status the ?? line ends with. It says whether the
// agent's work finished, not which key the user left with: 1 when the turn
// failed or a request was canceled before its reply was complete (Ctrl+C or
// Esc while it ran, typing over a streaming inline ghost), else 0, a reply
// the user declined included (Esc or Ctrl+C at a hint, leaving an inline
// line once its fill arrived, abandoning an edit of its command). A turn
// the shell's ending cut off ends as a line a signal ended: 128+signal
// (shellEndStatus), and one whose pipe command Ctrl+C interrupted as a
// line Ctrl+C ended: 130. A turn that handed a command to the shell has none:
// the command's outcome is the line's.
func (t *liveTurn) lineExitCode() (exitCode int, ok bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	switch {
	case t.phase == turnHandedOff:
		return 0, false
	case t.life == turnEnding:
		return shellEndStatus(t.parent), true
	case t.phase == turnCutOff:
		return 128 + int(syscall.SIGINT), true
	case t.phase == turnFailed, t.phase == turnCanceled:
		return 1, true
	default:
		return 0, true
	}
}

// restLocked puts the turn at rest, reporting hash idle unless the turn's
// phase did already.
func (t *liveTurn) restLocked() {
	t.stopWorkingLocked()
	if !t.phase.idle() {
		t.s.agentStatus.idle()
	}
	t.phase = turnAtRest
}

// workingLocked reports whether the agent works for the open turn: it
// waits on the agent or streams the reply, with no prompt up.
func (t *liveTurn) workingLocked() bool {
	return t.life == turnOpen && (t.phase == turnWaiting || t.phase == turnStreaming)
}

// stopWorkingLocked takes down what shows the agent working, the spinner's
// row and the progress bar, when the turn leaves a working phase.
func (t *liveTurn) stopWorkingLocked() {
	if t.phase.working() {
		t.s.agentOutput.TakeRow()
		t.s.responseUI.StopProgress()
	}
}

// letGoLocked releases what the turn holds while it runs: Ctrl+C, its
// request, and the shell's current-turn slot. Safe to repeat.
func (t *liveTurn) letGoLocked() {
	t.release()
	t.stopRequestLocked()
	t.s.turn.CompareAndSwap(t, nil)
}

// closeLocked lets go of everything and closes the turn: its phase is its
// outcome from here on, with the shell's ending (turnEnding) if that is
// what settled it. Safe to repeat.
func (t *liveTurn) closeLocked() {
	t.letGoLocked()
	t.unwatch()
	t.cancel()
	if t.life == turnOpen {
		t.life = turnClosed
	}
}

// stopRequestLocked releases what the turn's request holds, if it made one.
func (t *liveTurn) stopRequestLocked() {
	if t.req != nil {
		t.req.stop()
	}
}

// noteDenied remembers a command the user denied at a permission prompt
// of the open turn.
func (t *liveTurn) noteDenied(command string) {
	command = strings.TrimSpace(command)
	if command == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.life != turnOpen {
		return
	}
	if t.denied == nil {
		t.denied = map[string]bool{}
	}
	t.denied[command] = true
}

// demoteDenied keeps a command the user denied during this turn from coming
// back one Enter away. The agent is told to reply with a denied command, so
// the reply stays readable, just not runnable.
func (t *liveTurn) demoteDenied(resp agent.Response) agent.Response {
	if resp.Type != agent.ResponseTypeCommand {
		return resp
	}
	t.mu.Lock()
	denied := t.denied[strings.TrimSpace(resp.Command)]
	t.mu.Unlock()
	if !denied {
		return resp
	}
	resp.Type = agent.ResponseTypeExplanation
	if resp.Explanation == "" {
		resp.Explanation = resp.Command
	}
	return resp
}
