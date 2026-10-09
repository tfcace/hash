package shell

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/tfcace/hash/internal/agent"
	"github.com/tfcace/hash/internal/config"
	"github.com/tfcace/hash/internal/parser"
	"github.com/tfcace/hash/internal/programstatus"
)

func rootReport(state programstatus.State, kind programstatus.Kind, msg string) string {
	return programstatus.Sequence(programstatus.Report{State: state, Kind: kind, App: "hash", Msg: msg})
}

func newStatusShell(mock *agent.MockTransport) (*Shell, *bytes.Buffer) {
	var buf bytes.Buffer
	sh := &Shell{
		config:       config.Default(),
		agentHandler: NewAgentHandler(agent.NewClient(mock)),
		responseUI:   NewResponseUI(io.Discard),
		agentOutput:  NewAgentOutputCoordinator(io.Discard),
		agentStatus:  newAgentStatus(programstatus.NewReporter(&buf)),
	}
	return sh, &buf
}

func TestAgentStatus_NilIsSafe(t *testing.T) {
	var s *agentStatus
	s.working("x")
	s.resume()
	s.blocked(programstatus.Question, "x")
	s.done("x")
	s.failed("x")
	s.idle()
	s.finish()
}

func TestAgentStatus_FinishEndsAnOpenTurnWithIdle(t *testing.T) {
	var buf bytes.Buffer
	s := newAgentStatus(programstatus.NewReporter(&buf))
	s.working("find big files")
	s.finish()
	want := rootReport(programstatus.Working, "", "find big files") + rootReport(programstatus.Idle, "", "")
	if buf.String() != want {
		t.Errorf("wrote %q\nwant  %q", buf.String(), want)
	}

	buf.Reset()
	s.done("answer")
	s.finish()
	if want := rootReport(programstatus.Done, "", "answer"); buf.String() != want {
		t.Errorf("finish after done wrote %q, want only the done", buf.String())
	}
}

func TestAgentStatus_ResumeRepeatsTheTurnsPrompt(t *testing.T) {
	var buf bytes.Buffer
	s := newAgentStatus(programstatus.NewReporter(&buf))
	s.working("explain this")
	s.blocked(programstatus.Permission, "kubectl get pods")
	s.resume()
	want := rootReport(programstatus.Working, "", "explain this") +
		rootReport(programstatus.Blocked, programstatus.Permission, "kubectl get pods") +
		rootReport(programstatus.Working, "", "explain this")
	if buf.String() != want {
		t.Errorf("wrote %q\nwant  %q", buf.String(), want)
	}
}

// A pipe turn has no confirmation: the answer is ready to look at.
func TestHandleAgentFullStreaming_ProseAnswerReportsWorkingThenDone(t *testing.T) {
	mock := agent.NewMockTransport(agent.Response{Type: agent.ResponseTypeExplanation, Explanation: "Two files changed.\nBoth in docs."})
	sh, buf := newStatusShell(mock)

	sh.handleAgentFullStreaming(context.Background(), parser.ParseResult{Type: parser.CommandTypeAgentPipe, Command: "git diff", AgentPrompt: "summarize"}, "m")

	want := rootReport(programstatus.Working, "", "summarize") + rootReport(programstatus.Done, "", "Two files changed.")
	if buf.String() != want {
		t.Errorf("wrote %q\nwant  %q", buf.String(), want)
	}
}

// A command reply waits for Enter: blocked on permission, then idle once
// the user acts. Here stdin is not a terminal, so the wait returns cancel.
func TestHandleAgentFullStreaming_CommandReplyBlocksOnPermission(t *testing.T) {
	mock := agent.NewMockTransport(agent.Response{Type: agent.ResponseTypeCommand, Command: "find / -size +100M"})
	sh, buf := newStatusShell(mock)

	sh.handleAgentFullStreaming(context.Background(), parser.ParseResult{Type: parser.CommandTypeAgent, AgentPrompt: "find big files"}, "m")

	want := rootReport(programstatus.Working, "", "find big files") +
		rootReport(programstatus.Blocked, programstatus.Permission, "find / -size +100M") +
		rootReport(programstatus.Idle, "", "")
	if buf.String() != want {
		t.Errorf("wrote %q\nwant  %q", buf.String(), want)
	}
}

func TestRunAgentConversationLoop_ReplyBlocksOnQuestion(t *testing.T) {
	mock := agent.NewMockTransport(agent.Response{Type: agent.ResponseTypeExplanation, Explanation: "Thanks, done."})
	sh, buf := newStatusShell(mock)
	sh.agentReplyInputHook = func(ctx context.Context) (string, error) { return "internal/shell", nil }

	sh.runAgentConversationLoop(context.Background(), "m", []agentConversationMessage{
		{Role: "user", Text: "Find my config files"},
		{Role: "assistant", Text: "Sure.\nWhich directory should I inspect?"},
	})

	want := rootReport(programstatus.Blocked, programstatus.Question, "Which directory should I inspect?") +
		rootReport(programstatus.Working, "", "internal/shell")
	if !strings.HasPrefix(buf.String(), want) {
		t.Errorf("wrote %q\nwant it to start with %q", buf.String(), want)
	}
}

func TestHandleToolPermission_BlocksThenResumes(t *testing.T) {
	sh, buf := newStatusShell(agent.NewMockTransport())
	sh.readKey = func(ctx context.Context) byte { return 'y' }
	sh.agentStatus.working("list pods")
	buf.Reset()

	sh.handleToolPermission(context.Background(), agent.ToolPermissionRequest{Command: "kubectl get pods", ToolName: "bash"})

	want := rootReport(programstatus.Blocked, programstatus.Permission, "kubectl get pods") + rootReport(programstatus.Working, "", "list pods")
	if buf.String() != want {
		t.Errorf("wrote %q\nwant  %q", buf.String(), want)
	}
}

func TestHandleAgentStreamError_ReportsError(t *testing.T) {
	sh, buf := newStatusShell(agent.NewMockTransport())

	sh.handleAgentStreamError(context.Background(), parser.ParseResult{}, "m", errors.New("adapter crashed"), 0, 0)

	if want := rootReport(programstatus.Error, "", "adapter crashed"); !strings.HasPrefix(buf.String(), want) {
		t.Errorf("wrote %q\nwant it to start with %q", buf.String(), want)
	}
}
