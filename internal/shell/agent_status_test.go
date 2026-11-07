package shell

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

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
	s.blocked(programstatus.Question, "x")
	s.done("x")
	s.failed("x")
	s.idle()
}

// Ending a turn that is still working reports idle; a done answer is meant
// to outlive the turn, so ending it adds nothing.
func TestTurn_EndReportsAnOpenTurnIdleAndKeepsADoneOne(t *testing.T) {
	sh, buf := newStatusShell(agent.NewMockTransport())

	open := sh.beginTurn(context.Background())
	open.requestInline("find big files")
	open.end()
	want := rootReport(programstatus.Working, "", "find big files") + rootReport(programstatus.Idle, "", "")
	if buf.String() != want {
		t.Errorf("wrote %q\nwant  %q", buf.String(), want)
	}

	answered := sh.beginTurn(context.Background())
	answered.requestInline("q")
	answered.answered(agent.Response{Type: agent.ResponseTypeExplanation, Explanation: "answer"}, "answer", replyStands, 0)
	buf.Reset()
	answered.end()
	if buf.String() != "" {
		t.Errorf("ending a done turn wrote %q, want nothing", buf.String())
	}
}

// A pipe turn has no confirmation: the answer is ready to look at.
func TestHandleAgentFullStreaming_ProseAnswerReportsWorkingThenDone(t *testing.T) {
	mock := agent.NewMockTransport(agent.Response{Type: agent.ResponseTypeExplanation, Explanation: "Two files changed.\nBoth in docs."})
	sh, buf := newStatusShell(mock)

	sh.handleAgentFullStreaming(startTurn(t, sh), parser.ParseResult{Type: parser.CommandTypeAgentPipe, Command: "git diff", AgentPrompt: "summarize"})

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

	sh.handleAgentFullStreaming(startTurn(t, sh), parser.ParseResult{Type: parser.CommandTypeAgent, AgentPrompt: "find big files"})

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
	typeAhead(t, sh, "internal/shell\r\x04") // the reply, then leave

	captureStdout(t, sh, func() {
		sh.runAgentConversationLoop(startTurn(t, sh), []agentConversationMessage{
			{Role: "user", Text: "Find my config files"},
			{Role: "assistant", Text: "Sure.\nWhich directory should I inspect?"},
		})
	})

	want := rootReport(programstatus.Blocked, programstatus.Question, "Which directory should I inspect?") +
		rootReport(programstatus.Working, "", "internal/shell")
	if !strings.HasPrefix(buf.String(), want) {
		t.Errorf("wrote %q\nwant it to start with %q", buf.String(), want)
	}
}

func TestHandleToolPermission_BlocksThenResumes(t *testing.T) {
	sh, buf := newStatusShell(agent.NewMockTransport())
	screen := &lockedBuffer{}
	sh.agentOutput = NewAgentOutputCoordinator(screen)
	tt := newTestTerminal(t, sh)
	tt.answerPermissions("y")
	tt.answerHints(screen)
	t.Cleanup(func() { sh.agentOutput.TakeRow() }) // an allowed tool shows activity until the turn ends
	startTurn(t, sh).request("list pods")
	buf.Reset()

	sh.handleToolPermission(context.Background(), agent.ToolPermissionRequest{Command: "kubectl get pods", ToolName: "bash"})

	want := rootReport(programstatus.Blocked, programstatus.Permission, "kubectl get pods") + rootReport(programstatus.Working, "", "list pods")
	if buf.String() != want {
		t.Errorf("wrote %q\nwant  %q", buf.String(), want)
	}
}

func TestHandleAgentStreamError_ReportsError(t *testing.T) {
	sh, buf := newStatusShell(agent.NewMockTransport())

	sh.handleAgentStreamError(startTurn(t, sh), errors.New("adapter crashed"), 0)

	if want := rootReport(programstatus.Error, "", "adapter crashed"); !strings.HasPrefix(buf.String(), want) {
		t.Errorf("wrote %q\nwant it to start with %q", buf.String(), want)
	}
}

// The activity spinner's row must not survive a permission prompt: its last
// frame ("agent · running · ...") used to stay in scrollback above the tool.
func TestHandleToolPermission_ClearsSpinnerRow(t *testing.T) {
	t.Parallel()
	sh, _ := newStatusShell(agent.NewMockTransport())
	ui := &lockedBuffer{}
	sh.responseUI = NewResponseUI(ui)
	sh.agentOutput = NewAgentOutputCoordinator(ui)
	newTestTerminal(t, sh).answerHints(ui) // n at the prompt

	startTurn(t, sh).request("ls")
	sh.agentOutput.ShowActivity("agent · running · ls")
	time.Sleep(200 * time.Millisecond) // let a frame draw
	sh.handleToolPermission(context.Background(), agent.ToolPermissionRequest{Command: "ls", ToolName: "bash"})
	sh.agentOutput.TakeRow()

	out := ui.String()
	prompt := strings.Index(out, "Agent wants to run")
	if prompt < 0 {
		t.Fatalf("no permission prompt in %q", out)
	}
	before := out[:prompt]
	frame := strings.LastIndex(before, "agent · running · ls")
	if frame < 0 {
		t.Fatalf("spinner never drew: %q", before)
	}
	if !strings.Contains(before[frame:], "\r\x1b[K") {
		t.Errorf("spinner row not cleared before the prompt: %q", before[frame:])
	}
	after := out[strings.LastIndex(before, "\r\x1b[K"):]
	if down, up := strings.Count(after, "\n"), strings.Count(after, ansiCursorUp); down != up {
		t.Errorf("moved down %d rows and up %d: the spinner's row is left behind", down, up)
	}
}

// Off a terminal (stdin a pipe) no key can answer a full turn's permission
// prompt: drawing it only to deny it at once shows a question nobody can
// answer. The tool is denied with a row that says why, and the turn never
// reports itself blocked on a permission.
func TestTurn_OffATerminalAToolIsDeniedWithoutAPrompt(t *testing.T) {
	sh, screen, status := newTurnShell(t, &scriptedTransport{})
	sh.editorCfg.Input, _ = typedKeys(t) // the shell's key reader, on a pipe: the watcher finds no terminal
	turn := startTurn(t, sh)
	turn.request("clean up")
	if full := turn.req.(*fullRequest); full.keys != nil {
		t.Fatal("a key watcher started on a pipe")
	}
	turn.tool(agent.ToolCallUpdate{ID: "t1", Title: "rm -rf build", Kind: "execute", Status: agent.ToolCallInProgress}, "")

	allow, always := sh.handleToolPermission(context.Background(), agent.ToolPermissionRequest{Command: "rm -rf build", ToolName: "Bash", ToolCallID: "t1"})
	turn.streamEnded()
	if allow || always {
		t.Errorf("permission = %v, %v; want denied", allow, always)
	}
	out := screen.String()
	if strings.Contains(out, "Agent wants to run") {
		t.Errorf("drew a permission prompt nobody can answer: %q", out)
	}
	if !strings.Contains(out, "denied") || !strings.Contains(out, "no terminal") {
		t.Errorf("no row says the tool was denied and why: %q", out)
	}
	if strings.Contains(status.String(), rootReport(programstatus.Blocked, programstatus.Permission, "rm -rf build")) {
		t.Errorf("reported blocked on a permission nobody can give: %q", status.String())
	}
	if got := turn.demoteDenied(agent.Response{Type: agent.ResponseTypeCommand, Command: "rm -rf build"}); got.Type != agent.ResponseTypeExplanation {
		t.Error("the denied command comes back one Enter away")
	}
}

// An inline request has no key to answer with either (the editor owns
// stdin): its fill stops, saying why, with no prompt drawn.
func TestTurn_AnInlineToolIsDeniedWithoutAPrompt(t *testing.T) {
	sh, screen, status := newTurnShell(t, &scriptedTransport{})
	turn := startTurn(t, sh)
	turn.requestInline("long listing")
	if allow, _ := sh.handleToolPermission(context.Background(), agent.ToolPermissionRequest{Command: "ls", ToolName: "Bash"}); allow {
		t.Error("an inline request's tool was allowed")
	}
	if strings.Contains(screen.String(), "Agent wants to run") || turn.Phase() != turnFailed {
		t.Errorf("phase %v, screen %q; want failed with no prompt", turn.Phase(), screen.String())
	}
	if want := rootReport(programstatus.Error, "", inlineToolDenied("ls").Error()); lastStatus(status) != want {
		t.Errorf("status = %q, want %q", lastStatus(status), want)
	}
}
