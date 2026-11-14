package shell

import (
	"sync"

	"github.com/tfcace/hash/internal/agent"
	"github.com/tfcace/hash/internal/programstatus"
)

// agentStatus reports the ?? turn to the terminal with the Program Status
// Protocol (OSC 7501), on the root record: while a turn runs, hash is the
// program in the foreground. The turn decides what to report from its phase
// (see liveTurn); this only writes it. Methods are safe to call from the
// permission handler's goroutine, and on a nil receiver, which reports
// nothing.
type agentStatus struct {
	mu sync.Mutex
	r  *programstatus.Reporter
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
	a.r.Report(programstatus.Report{State: state, Kind: kind, App: "hash", Msg: msg})
}

// working says the agent works on prompt.
func (a *agentStatus) working(prompt string) {
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
