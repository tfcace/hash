package shell

import (
	"sync"

	"github.com/tfcace/hash/internal/agent"
	"github.com/tfcace/hash/internal/programstatus"
)

// agentStatus reports the ?? turn to the terminal with the Program Status
// Protocol (OSC 7501), on the root record: while a turn runs, hash is the
// program in the foreground. It remembers the last report so finish can end
// a turn no other path ended, and the prompt so a permission prompt can
// hand back to working. Methods are safe to call from the permission
// handler's goroutine, and on a nil receiver, which reports nothing.
type agentStatus struct {
	mu     sync.Mutex
	r      *programstatus.Reporter
	last   programstatus.State
	prompt string
}

func newAgentStatus(r *programstatus.Reporter) *agentStatus {
	return &agentStatus{r: r}
}

func (a *agentStatus) report(state programstatus.State, kind programstatus.Kind, msg string) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.last = state
	a.r.Report(programstatus.Report{State: state, Kind: kind, App: "hash", Msg: msg})
}

// working starts a turn on prompt.
func (a *agentStatus) working(prompt string) {
	if a == nil {
		return
	}
	a.mu.Lock()
	a.prompt = prompt
	a.mu.Unlock()
	a.report(programstatus.Working, "", prompt)
}

// resume returns to working on the turn's prompt once a prompt is answered.
func (a *agentStatus) resume() {
	if a == nil {
		return
	}
	a.mu.Lock()
	prompt := a.prompt
	a.mu.Unlock()
	a.report(programstatus.Working, "", prompt)
}

// blocked says the turn waits on the user: kind says what for, msg why.
func (a *agentStatus) blocked(kind programstatus.Kind, msg string) {
	a.report(programstatus.Blocked, kind, msg)
}

// done says the turn's answer is ready to look at.
func (a *agentStatus) done(msg string) {
	a.report(programstatus.Done, "", msg)
}

// failed says the turn stopped on an error.
func (a *agentStatus) failed(msg string) {
	a.report(programstatus.Error, "", msg)
}

// idle says hash is at rest: the user canceled or acted, or a command took
// over and reports for itself.
func (a *agentStatus) idle() {
	a.report(programstatus.Idle, "", "")
}

// finish ends a turn still working or blocked with idle, so no path leaves
// a stale record. A done or error stays: it is meant to outlive the turn.
func (a *agentStatus) finish() {
	if a == nil {
		return
	}
	a.mu.Lock()
	open := a.last == programstatus.Working || a.last == programstatus.Blocked
	a.mu.Unlock()
	if open {
		a.idle()
	}
}

// reportConfirmationWait tells the terminal what the confirmation hint
// waits for: a command needs the user's go-ahead; prose is done, and the
// keypress only dismisses it.
func (s *Shell) reportConfirmationWait(confirmType ConfirmationType, resp agent.Response, responseText string) {
	switch confirmType {
	case ConfirmTypeCommand:
		s.agentStatus.blocked(programstatus.Permission, resp.Command)
	case ConfirmTypeExplanation:
		s.agentStatus.done(firstNonEmptyLine(responseText))
	}
}

// lastAssistantText is what the user is replying to.
func lastAssistantText(transcript []agentConversationMessage) string {
	for i := len(transcript) - 1; i >= 0; i-- {
		if transcript[i].Role == "assistant" {
			return transcript[i].Text
		}
	}
	return ""
}
