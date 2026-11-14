package shell

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tfcace/hash/internal/agent"
	"github.com/tfcace/hash/internal/config"
	"github.com/tfcace/hash/internal/editor"
	"github.com/tfcace/hash/internal/parser"
)

func TestAgentResponseWantsReply(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{
			name: "final question",
			text: "I found three config files.\nWhich one should I edit?",
			want: true,
		},
		{
			name: "final follow up phrase",
			text: "I can do that two ways.\nWhich would you prefer",
			want: true,
		},
		{
			name: "instructional question in body only",
			text: "Ask yourself: is this worth automating?\nThe command is ready.",
			want: false,
		},
		{
			name: "plain explanation",
			text: "The build failed because the module cache is stale.",
			want: false,
		},
		{
			name: "legacy marker ignored",
			text: "Which directory should I inspect?\n[AWAITING_INPUT]",
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := agentResponseWantsReply(tt.text); got != tt.want {
				t.Fatalf("agentResponseWantsReply(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
}

func TestAgentConversationReplyPromptLabelsUser(t *testing.T) {
	if !strings.Contains(agentConversationReplyPrompt, "you") {
		t.Fatalf("agentConversationReplyPrompt = %q, want user-labeled prompt", agentConversationReplyPrompt)
	}
	if !strings.Contains(agentConversationReplyPrompt, "›") {
		t.Fatalf("agentConversationReplyPrompt = %q, want polished reply marker", agentConversationReplyPrompt)
	}
}

func TestAgentConversationRailsUseLiveRailColor(t *testing.T) {
	goldSolidRail := agentConversationLiveRailStyle + "│\x1b[0m"
	goldDashedRail := agentConversationLiveRailStyle + "┆\x1b[0m"
	goldHeaderRail := agentConversationLiveRailStyle + "╭─ conversation\x1b[0m"

	frame := agentConversationInputFrame(true)
	for _, tt := range []struct {
		name string
		text string
		want string
	}{
		{name: "committed user prompt", text: frame.Prefix, want: goldSolidRail},
		{name: "live user prompt", text: frame.LivePrefix, want: goldDashedRail},
		{name: "live top line", text: frame.LiveTopLine, want: goldDashedRail},
		{name: "header rail", text: frame.TopLine, want: goldHeaderRail},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if !strings.Contains(tt.text, tt.want) {
				t.Fatalf("%s = %q, want gold rail sequence %q", tt.name, tt.text, tt.want)
			}
		})
	}

	var buf strings.Builder
	prefixer := newAgentConversationRailPrefixer(func(text string) {
		buf.WriteString(text)
	})
	prefixer.Write("Question 1:\nYes or no?")

	output := buf.String()
	if got := strings.Count(output, goldSolidRail); got != 2 {
		t.Fatalf("agent output should color every solid rail gold, got %d gold rails in %q", got, output)
	}
}

func TestAgentConversationInputFrame(t *testing.T) {
	frame := agentConversationInputFrame(true)
	if frame.Prefix != agentConversationReplyPrompt {
		t.Fatalf("frame prefix = %q, want reply prompt %q", frame.Prefix, agentConversationReplyPrompt)
	}
	if frame.LivePrefix != agentConversationLiveReplyPrompt {
		t.Fatalf("frame live prefix = %q, want %q", frame.LivePrefix, agentConversationLiveReplyPrompt)
	}
	if frame.LiveTopLine != agentConversationLiveRailLine {
		t.Fatalf("frame live top line = %q, want %q", frame.LiveTopLine, agentConversationLiveRailLine)
	}
	if frame.PrefixWidth != agentConversationReplyPromptWidth {
		t.Fatalf("frame prefix width = %d, want %d", frame.PrefixWidth, agentConversationReplyPromptWidth)
	}
	if !strings.Contains(frame.Prefix, "│") || strings.Contains(frame.Prefix, "╎") || strings.Contains(frame.Prefix, "┆") {
		t.Fatalf("committed prompt should use solid rail, got %q", frame.Prefix)
	}
	if !strings.Contains(frame.LivePrefix, "┆") {
		t.Fatalf("live prompt should use dashed rail, got %q", frame.LivePrefix)
	}
	if !strings.Contains(frame.LiveTopLine, "┆") {
		t.Fatalf("live connector should use dashed rail, got %q", frame.LiveTopLine)
	}
	if !strings.Contains(frame.TopLine, "conversation") {
		t.Fatalf("frame top line should label conversation mode, got %q", frame.TopLine)
	}
	if !strings.Contains(frame.TopLine, agentConversationLiveRailStyle+"╭─ conversation\033[0m") {
		t.Fatalf("frame top line should use live rail color for header, got %q", frame.TopLine)
	}
	if !strings.Contains(frame.TopLine, "/exit") {
		t.Fatalf("frame top line should show exit affordance, got %q", frame.TopLine)
	}
	if frame.LineBg != "" || frame.BottomLineBg != "" || frame.BottomExtraLineBg != "" {
		t.Fatalf("conversation frame should avoid background fills in terminals, got line=%q bottom=%q extra=%q",
			frame.LineBg, frame.BottomLineBg, frame.BottomExtraLineBg)
	}
	if frame.BottomLine != "" || frame.BottomExtraLine != "" {
		t.Fatalf("conversation frame should stay open across turns, got bottom=%q extra=%q",
			frame.BottomLine, frame.BottomExtraLine)
	}
}

func TestAgentConversationEditorConfigCancelsOnEscape(t *testing.T) {
	base := editor.Config{Gutter: true}
	cfg := agentConversationEditorConfig(base, true)
	if cfg.Decline != editor.DeclineEraseRow {
		t.Fatal("conversation reply editor should let Escape leave the conversation")
	}
	if cfg.InputFrame == nil || !strings.Contains(cfg.InputFrame.TopLine, "esc to leave") {
		t.Fatalf("conversation reply editor should render matching Escape hint, got %#v", cfg.InputFrame)
	}
}

func TestAgentConversationInputFrame_ContinuationDoesNotReopenRail(t *testing.T) {
	frame := agentConversationInputFrame(false)
	if frame.TopLine != "" {
		t.Fatalf("continuation frame should not repeat top line, got %q", frame.TopLine)
	}
	if !strings.Contains(frame.Prefix, "│") {
		t.Fatalf("continuation prompt should keep conversation rail, got %q", frame.Prefix)
	}
}

func TestAgentConversationRailPrefixer_ThreadsAgentOutput(t *testing.T) {
	var buf strings.Builder
	prefixer := newAgentConversationRailPrefixer(func(text string) {
		buf.WriteString(text)
	})

	prefixer.Write("Question 1:")
	prefixer.Write(" Is it physical?\nYes or no?")
	prefixer.Write("\n")
	prefixer.Write("Answer yes/no.")

	output := buf.String()
	for _, expected := range []string{
		agentConversationAgentPrefix + "Question 1:",
		"\n" + agentConversationAgentContinuation + "Yes or no?",
		"\n" + agentConversationAgentContinuation + "Answer yes/no.",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("expected %q in threaded output, got %q", expected, output)
		}
	}
	if strings.Contains(output, "\n"+agentConversationAgentPrefix+"Yes or no?") {
		t.Fatalf("continuation lines should align under the agent message, got %q", output)
	}
	if strings.Contains(output, "\n"+agentConversationAgentPrefix+"Answer yes/no.") {
		t.Fatalf("new chunks after a newline should stay under the same agent turn, got %q", output)
	}
}

func TestAgentConversationReplyEndsConversation(t *testing.T) {
	tests := []struct {
		name  string
		reply string
		want  bool
	}{
		{name: "done", reply: "done", want: true},
		{name: "slash exit", reply: "/exit", want: true},
		{name: "vim quit", reply: ":q", want: true},
		{name: "polite had enough", reply: "ok, I've had enough", want: true},
		{name: "thats all", reply: "thanks, that's all", want: true},
		{name: "stop playing", reply: "stop playing", want: true},
		{name: "twenty questions answer no", reply: "no", want: false},
		{name: "side quest keeps conversation", reply: "let's pause the game and list my kubernetes contexts", want: false},
		{name: "command with stop is not exit", reply: "stop the container", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := agentConversationReplyEndsConversation(tt.reply); got != tt.want {
				t.Fatalf("agentConversationReplyEndsConversation(%q) = %v, want %v", tt.reply, got, tt.want)
			}
		})
	}
}

func TestLegacyAgentMarkerSanitizer_StripsSplitMarkers(t *testing.T) {
	sanitizer := newLegacyAgentMarkerSanitizer()

	var got string
	got += sanitizer.Write("Which directory?\n[AWAI")
	got += sanitizer.Write("TING_INPUT]")
	got += sanitizer.Flush()

	if got != "Which directory?\n" {
		t.Fatalf("sanitized stream = %q, want marker-free question", got)
	}
}

// classifyReply decides every turn's next step. The same reply classifies
// the same on any turn, with one deliberate exception: a question opens the
// reply box directly only once the user is in a conversation.
func TestClassifyReply(t *testing.T) {
	commandLikeQuestion := "Go ahead and answer Question 1: Is it a physical object you could touch?"
	deny := func(cmd string) func(agent.Response) agent.Response {
		return func(r agent.Response) agent.Response {
			if r.Type == agent.ResponseTypeCommand && r.Command == cmd {
				return agent.Response{Type: agent.ResponseTypeExplanation, Explanation: cmd}
			}
			return r
		}
	}
	tests := []struct {
		name           string
		typ            parser.CommandType
		inConversation bool
		text           string
		demote         func(agent.Response) agent.Response
		wantType       agent.ResponseType
		wantNext       replyNext
		wantConfirm    ConfirmationType
	}{
		{"command", parser.CommandTypeAgent, false, "ls -la", nil, agent.ResponseTypeCommand, replyAwaitsKey, ConfirmTypeCommand},
		{"command in conversation", parser.CommandTypeAgent, true, "ls -la", nil, agent.ResponseTypeCommand, replyAwaitsKey, ConfirmTypeCommand},
		{"prose", parser.CommandTypeAgent, false, "The config is in ./internal.", nil, agent.ResponseTypeExplanation, replyAwaitsKey, ConfirmTypeExplanation},
		{"prose in conversation", parser.CommandTypeAgent, true, "The config is in ./internal.", nil, agent.ResponseTypeExplanation, replyAwaitsKey, ConfirmTypeExplanation},
		{"question shows the hint first", parser.CommandTypeAgent, false, "Which directory?", nil, agent.ResponseTypeExplanation, replyAwaitsKey, ConfirmTypeExplanation},
		{"question in conversation asks back", parser.CommandTypeAgent, true, "Which directory?", nil, agent.ResponseTypeExplanation, replyAsksBack, 0},
		{"question misread as a command", parser.CommandTypeAgent, false, commandLikeQuestion, nil, agent.ResponseTypeExplanation, replyAwaitsKey, ConfirmTypeExplanation},
		{"question misread as a command in conversation", parser.CommandTypeAgent, true, commandLikeQuestion, nil, agent.ResponseTypeExplanation, replyAsksBack, 0},
		{"command ending in ? stays a command", parser.CommandTypeAgent, true, "ls ?", nil, agent.ResponseTypeCommand, replyAwaitsKey, ConfirmTypeCommand},
		{"denied command becomes prose", parser.CommandTypeAgent, false, "rm -rf build", deny("rm -rf build"), agent.ResponseTypeExplanation, replyAwaitsKey, ConfirmTypeExplanation},
		{"pipe prose stands", parser.CommandTypeAgentPipe, false, "Two files changed.", nil, agent.ResponseTypeExplanation, replyStands, 0},
		{"pipe question stands", parser.CommandTypeAgentPipe, false, "Which directory?", nil, agent.ResponseTypeExplanation, replyStands, 0},
		{"pipe command still confirms", parser.CommandTypeAgentPipe, false, "ls -la", nil, agent.ResponseTypeCommand, replyAwaitsKey, ConfirmTypeCommand},
		// An error reply has no key to press: "enter to retry" would do nothing.
		{"error reply stands", parser.CommandTypeAgent, false, "", nil, agent.ResponseTypeError, replyStands, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, next, confirm := classifyReply(tt.typ, tt.inConversation, tt.text, tt.demote)
			if resp.Type != tt.wantType || next != tt.wantNext || confirm != tt.wantConfirm {
				t.Errorf("classifyReply(%q) = %v, %v, %v; want %v, %v, %v", tt.text, resp.Type, next, confirm, tt.wantType, tt.wantNext, tt.wantConfirm)
			}
		})
	}
}

func TestRunAgentConversationLoop_SendsFollowUpReply(t *testing.T) {
	replyIn, replyOut, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer replyIn.Close()  //nolint:errcheck
	defer replyOut.Close() //nolint:errcheck

	mock := agent.NewMockTransport(agent.Response{
		Type:        agent.ResponseTypeExplanation,
		Explanation: "Thanks, I can inspect that.",
	})
	responseUI := NewResponseUI(&strings.Builder{})
	sh := &Shell{
		config:       config.Default(),
		agentHandler: NewAgentHandler(agent.NewClient(mock)),
		responseUI:   responseUI,
		agentOutput:  NewAgentOutputCoordinator(&strings.Builder{}),
	}
	typeAhead(t, sh, "internal/shell\r\x04") // the reply, then leave
	transcript := []agentConversationMessage{
		{Role: "user", Text: "Find my config files"},
		{Role: "assistant", Text: "Which directory should I inspect?"},
	}

	captureStdout(t, sh, func() { sh.runAgentConversationLoop(startTurn(t, sh), transcript) })

	requests := mock.Requests()
	if len(requests) != 1 {
		t.Fatalf("mock recorded %d requests, want 1", len(requests))
	}
	if !strings.Contains(requests[0].Prompt, "Latest user message: internal/shell") {
		t.Fatalf("follow-up prompt did not include reply:\n%s", requests[0].Prompt)
	}
}

// The first text chunk of a follow-up arrives while a tool permission
// prompt is up: it must be held, not written, and the clear before it must
// not erase the prompt's key line.
func TestStreamAgentFollowUpTurn_FirstChunkDuringPermissionPrompt(t *testing.T) {
	t.Parallel()
	prompted := make(chan struct{})
	var sh *Shell
	var screen *lockedBuffer
	tr := &scriptedTransport{scripts: []turnScript{{
		events: []agent.StreamEvent{toolEvent("t1", "ls", agent.ToolCallPending)},
		during: func(ctx context.Context) {
			sh.handleToolPermission(ctx, agent.ToolPermissionRequest{Command: "ls", ToolName: "Bash", ToolCallID: "t1"})
		},
		gate:  prompted,
		after: []agent.StreamEvent{textEvent("All done.")},
		hold:  400 * time.Millisecond, // the agent waits on the answer
	}}}
	var tt *testTerminal
	sh, screen, _, tt = newTurnShellOn(t, tr)
	tt.answerPermissions("") // the test answers
	go func() {
		waitFor(t, func() bool { return strings.Contains(screen.String(), "Agent wants to run") })
		close(prompted)
		time.Sleep(100 * time.Millisecond) // the chunk lands behind the prompt
		out := screen.String()
		if strings.Contains(out, "All done.") {
			t.Errorf("chunk written to terminal while permission prompt active:\n%q", out)
		}
		if tail := out[strings.Index(out, "Agent wants to run"):]; strings.Contains(tail, "\r\x1b[K") {
			t.Errorf("a clear-line erased the prompt's key line: %q", tail)
		}
		tt.typeKeys("y")
	}()

	text, ok := sh.streamAgentFollowUpTurn(startTurn(t, sh), "go ahead", nil)
	if !ok {
		t.Fatal("streamAgentFollowUpTurn failed")
	}
	if !strings.Contains(text, "All done.") {
		t.Fatalf("response text not collected, got %q", text)
	}
	if !strings.Contains(screen.String(), "All done.") {
		t.Errorf("held chunk never drawn once the prompt cleared: %q", screen.String())
	}
}

func TestRunAgentConversationLoop_ExitReplyDoesNotCallAgent(t *testing.T) {
	mock := agent.NewMockTransport(agent.Response{
		Type:        agent.ResponseTypeExplanation,
		Explanation: "This should not be sent.",
	})
	sh := &Shell{
		config:       config.Default(),
		agentHandler: NewAgentHandler(agent.NewClient(mock)),
		responseUI:   NewResponseUI(&strings.Builder{}),
		agentOutput:  NewAgentOutputCoordinator(&strings.Builder{}),
	}
	typeAhead(t, sh, "ok, I've had enough\r")
	transcript := []agentConversationMessage{
		{Role: "user", Text: "play twenty questions"},
		{Role: "assistant", Text: "Question 3: Is it found indoors?"},
	}

	captureStdout(t, sh, func() { sh.runAgentConversationLoop(startTurn(t, sh), transcript) })

	if got := len(mock.Requests()); got != 0 {
		t.Fatalf("mock recorded %d requests, want none", got)
	}
}

func TestRunAgentConversationLoop_CommandClassifiedQuestionKeepsReplyPrompt(t *testing.T) {
	t.Parallel()
	var out strings.Builder
	mock := agent.NewMockTransport(agent.Response{
		Type:    agent.ResponseTypeCommand,
		Command: "Question --answer?",
	})
	sh := &Shell{
		config:       config.Default(),
		agentHandler: NewAgentHandler(agent.NewClient(mock)),
		responseUI:   NewResponseUI(&out),
		agentOutput:  NewAgentOutputCoordinator(&out),
	}
	typeAhead(t, sh, "you ask the questions\ryes\r\x04")
	transcript := []agentConversationMessage{
		{Role: "user", Text: "play twenty questions"},
		{Role: "assistant", Text: "Ready."},
	}

	captureStdout(t, sh, func() { sh.runAgentConversationLoop(startTurn(t, sh), transcript) })

	if got := len(mock.Requests()); got != 2 {
		t.Fatalf("mock recorded %d requests, want 2 follow-up turns", got)
	}
	if strings.Contains(out.String(), "cmd ·") {
		t.Fatalf("conversation question should not render command confirmation, got:\n%s", out.String())
	}
}

func TestHandleAgentFullStreaming_QuestionShowsReplyHintWithoutAutoStartingConversation(t *testing.T) {
	var out strings.Builder
	mock := agent.NewMockTransport(agent.Response{
		Type:        agent.ResponseTypeExplanation,
		Explanation: "Which directory should I inspect?",
	})
	sh := &Shell{
		config:       config.Default(),
		agentHandler: NewAgentHandler(agent.NewClient(mock)),
		responseUI:   NewResponseUI(&out),
		agentOutput:  NewAgentOutputCoordinator(&out),
	}
	keys := typeAhead(t, sh, "internal/shell\r")

	sh.handleAgentFullStreaming(startTurn(t, sh), parser.ParseResult{
		Type:        parser.CommandTypeAgent,
		AgentPrompt: "help me choose a file",
	})

	if got := readKeys(t, keys, 1); got[0] != (editor.Key{Rune: 'i'}) {
		t.Fatal("plain agent questions should not automatically enter conversation mode")
	}
	if got := len(mock.Requests()); got != 1 {
		t.Fatalf("mock recorded %d requests, want only the initial request", got)
	}
	if !strings.Contains(out.String(), "r to reply") {
		t.Fatalf("question response should show reply hint, got:\n%s", out.String())
	}
}

func TestHandleAgentFullStreaming_ExplicitConversationPromptStillWaitsForReplyAction(t *testing.T) {
	var out strings.Builder
	mock := agent.NewMockTransport(agent.Response{
		Type:        agent.ResponseTypeExplanation,
		Explanation: "Question 1: Is it a physical object?",
	})
	sh := &Shell{
		config:       config.Default(),
		agentHandler: NewAgentHandler(agent.NewClient(mock)),
		responseUI:   NewResponseUI(&out),
		agentOutput:  NewAgentOutputCoordinator(&out),
	}
	keys := typeAhead(t, sh, "internal/shell\r")

	sh.handleAgentFullStreaming(startTurn(t, sh), parser.ParseResult{
		Type:        parser.CommandTypeAgent,
		AgentPrompt: "Let's play twenty questions. Ask one question at a time.",
	})

	if got := readKeys(t, keys, 1); got[0] != (editor.Key{Rune: 'i'}) {
		t.Fatal("conversation prompt text should not bypass the r: reply action")
	}
	if got := len(mock.Requests()); got != 1 {
		t.Fatalf("mock recorded %d requests, want only the initial request", got)
	}
	if !strings.Contains(out.String(), "r to reply") {
		t.Fatalf("explicit conversation prompt should still show reply hint first, got:\n%s", out.String())
	}
}

func TestHandleAgentFullStreaming_CommandClassifiedQuestionShowsReplyHint(t *testing.T) {
	var out strings.Builder
	mock := agent.NewMockTransport(agent.Response{
		Type:    agent.ResponseTypeCommand,
		Command: "Go ahead and answer Question 1: Is it a physical object you could touch?",
	})
	sh := &Shell{
		config:       config.Default(),
		agentHandler: NewAgentHandler(agent.NewClient(mock)),
		responseUI:   NewResponseUI(&out),
		agentOutput:  NewAgentOutputCoordinator(&out),
	}

	sh.handleAgentFullStreaming(startTurn(t, sh), parser.ParseResult{
		Type:        parser.CommandTypeAgent,
		AgentPrompt: "I'll think of something, and you ask up to 20 yes/no questions to guess it.",
	})

	output := out.String()
	if strings.Contains(output, "cmd ·") {
		t.Fatalf("natural-language question should not show command confirmation, got:\n%s", output)
	}
	if !strings.Contains(output, "r to reply") {
		t.Fatalf("natural-language question should show reply hint, got:\n%s", output)
	}
}

func TestHandleAgentFullStreaming_EmptyStreamShowsErrorWithoutRetryPrompt(t *testing.T) {
	var out bytes.Buffer
	mock := agent.NewMockTransport()
	sh := &Shell{
		config:       config.Default(),
		agentHandler: NewAgentHandler(agent.NewClient(mock)),
		responseUI:   NewResponseUI(&out),
		agentOutput:  NewAgentOutputCoordinator(&out),
	}

	sh.handleAgentFullStreaming(startTurn(t, sh), parser.ParseResult{
		Type:        parser.CommandTypeAgent,
		AgentPrompt: "hello",
	})

	output := out.String()
	if !strings.Contains(output, emptyAgentResponseMessage) {
		t.Fatalf("expected empty response error, got:\n%s", output)
	}
	if strings.Contains(output, "enter to retry") {
		t.Fatalf("empty response should not show retry confirmation, got:\n%s", output)
	}
}

func TestStreamAgentFollowUpTurn_EmptyStreamShowsErrorWithoutResponse(t *testing.T) {
	var out bytes.Buffer
	mock := agent.NewMockTransport()
	sh := &Shell{
		config:       config.Default(),
		agentHandler: NewAgentHandler(agent.NewClient(mock)),
		responseUI:   NewResponseUI(&out),
		agentOutput:  NewAgentOutputCoordinator(&out),
	}

	_, ok := sh.streamAgentFollowUpTurn(
		startTurn(t, sh),
		"ready",
		[]agentConversationMessage{{Role: "user", Text: "play twenty questions"}},
	)

	if ok {
		t.Fatal("expected empty follow-up stream to fail")
	}
	output := out.String()
	if !strings.Contains(output, emptyAgentResponseMessage) {
		t.Fatalf("expected empty response error, got:\n%s", output)
	}
	if strings.Contains(output, "enter to retry") {
		t.Fatalf("empty response should not show retry confirmation, got:\n%s", output)
	}
}

// The reply box takes prose for the agent, not a shell command: Tab must not
// run shell completion there, whichever completion hook the base editor has.
func TestAgentConversationEditorConfigDisablesShellCompletion(t *testing.T) {
	calls := 0
	base := editor.Config{
		CompleteOutcomeFunc: func(string, int) editor.CompletionOutcome {
			calls++
			return editor.MenuOutcome([]editor.Completion{{Text: "github"}}, 0)
		},
	}
	cfg := agentConversationEditorConfig(base, true)
	cfg.InputFrame = nil

	in := strings.NewReader("gi\t\r")
	result, err := editor.New(cfg, in, io.Discard).Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if calls != 0 || result.Text != "gi" {
		t.Fatalf("completion calls = %d, text = %q; want 0 calls and the typed %q", calls, result.Text, "gi")
	}
}
