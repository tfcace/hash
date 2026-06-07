package shell

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

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

func TestAgentConversationInputFrame(t *testing.T) {
	frame := agentConversationInputFrame(true)
	if frame == nil {
		t.Fatal("agentConversationInputFrame returned nil")
	}
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
	base := editor.Config{Keybindings: "helix", Gutter: true}
	cfg := agentConversationEditorConfig(base, true)
	if !cfg.CancelOnEscape {
		t.Fatal("conversation reply editor should let Escape leave the conversation")
	}
	if cfg.InputFrame == nil || !strings.Contains(cfg.InputFrame.TopLine, "Esc/Ctrl+C leaves") {
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
		"│ agent › Question 1:",
		"\n│         Yes or no?",
		"\n│         Answer yes/no.",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("expected %q in threaded output, got %q", expected, output)
		}
	}
	if strings.Contains(output, "\n│ agent › Yes or no?") {
		t.Fatalf("continuation lines should align under the agent message, got %q", output)
	}
	if strings.Contains(output, "\n│ agent › Answer yes/no.") {
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

func TestAgentTurnReplyDecisions(t *testing.T) {
	explanation := agent.Response{Type: agent.ResponseTypeExplanation, Explanation: "Which directory?"}
	command := agent.Response{Type: agent.ResponseTypeCommand, Command: "ls"}
	commandLikeQuestion := agent.Response{
		Type:    agent.ResponseTypeCommand,
		Command: "Go ahead and answer Question 1: Is it a physical object you could touch?",
	}

	if !agentTurnAllowsReply(parser.CommandTypeAgent, explanation) {
		t.Fatal("full agent explanations should allow explicit reply")
	}
	if agentTurnAllowsReply(parser.CommandTypeAgentPipe, explanation) {
		t.Fatal("pipe explanations should remain single-turn")
	}
	if agentTurnAllowsReply(parser.CommandTypeAgent, command) {
		t.Fatal("command suggestions should keep run/edit/cancel flow")
	}
	if !agentTurnShouldPromptForReply(parser.CommandTypeAgent, explanation, "Which directory?") {
		t.Fatal("full agent explanation ending with a question should prompt automatically")
	}
	if agentTurnShouldPromptForReply(parser.CommandTypeAgent, explanation, "The config is in ./internal.") {
		t.Fatal("plain explanations should not prompt automatically")
	}
	if !agentTurnShouldPromptForReply(parser.CommandTypeAgent, commandLikeQuestion, commandLikeQuestion.Command) {
		t.Fatal("natural-language questions misclassified as commands should keep the conversation prompt")
	}
	if agentTurnShouldPromptForReply(parser.CommandTypeAgent, command, "ls ?") {
		t.Fatal("command-shaped responses ending with ? should keep command confirmation")
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
	responseUI.in = replyIn
	sh := &Shell{
		config:       config.Default(),
		agentHandler: NewAgentHandler(agent.NewClient(mock)),
		responseUI:   responseUI,
		agentOutput:  NewAgentOutputCoordinator(&strings.Builder{}),
		agentReplyInputHook: func(ctx context.Context) (string, error) {
			return "internal/shell", nil
		},
	}
	transcript := []agentConversationMessage{
		{Role: "user", Text: "Find my config files"},
		{Role: "assistant", Text: "Which directory should I inspect?"},
	}

	sh.runAgentConversationLoop(context.Background(), "test-model", transcript)

	requests := mock.Requests()
	if len(requests) != 1 {
		t.Fatalf("mock recorded %d requests, want 1", len(requests))
	}
	if !strings.Contains(requests[0].Prompt, "Latest user message: internal/shell") {
		t.Fatalf("follow-up prompt did not include reply:\n%s", requests[0].Prompt)
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
		agentReplyInputHook: func(ctx context.Context) (string, error) {
			return "ok, I've had enough", nil
		},
	}
	transcript := []agentConversationMessage{
		{Role: "user", Text: "play twenty questions"},
		{Role: "assistant", Text: "Question 3: Is it found indoors?"},
	}

	sh.runAgentConversationLoop(context.Background(), "test-model", transcript)

	if got := len(mock.Requests()); got != 0 {
		t.Fatalf("mock recorded %d requests, want none", got)
	}
}

func TestRunAgentConversationLoop_CommandClassifiedQuestionKeepsReplyPrompt(t *testing.T) {
	var out strings.Builder
	replies := []string{"you ask the questions", "yes"}
	mock := agent.NewMockTransport(agent.Response{
		Type:    agent.ResponseTypeCommand,
		Command: "Question --answer?",
	})
	sh := &Shell{
		config:       config.Default(),
		agentHandler: NewAgentHandler(agent.NewClient(mock)),
		responseUI:   NewResponseUI(&out),
		agentOutput:  NewAgentOutputCoordinator(&out),
		agentReplyInputHook: func(ctx context.Context) (string, error) {
			if len(replies) == 0 {
				return "", context.Canceled
			}
			reply := replies[0]
			replies = replies[1:]
			return reply, nil
		},
	}
	transcript := []agentConversationMessage{
		{Role: "user", Text: "play twenty questions"},
		{Role: "assistant", Text: "Ready."},
	}

	sh.runAgentConversationLoop(context.Background(), "test-model", transcript)

	if got := len(mock.Requests()); got != 2 {
		t.Fatalf("mock recorded %d requests, want 2 follow-up turns", got)
	}
	if strings.Contains(out.String(), "cmd ·") {
		t.Fatalf("conversation question should not render command confirmation, got:\n%s", out.String())
	}
}

func TestHandleAgentFullStreaming_QuestionShowsReplyHintWithoutAutoStartingConversation(t *testing.T) {
	var out strings.Builder
	replyCalled := false
	mock := agent.NewMockTransport(agent.Response{
		Type:        agent.ResponseTypeExplanation,
		Explanation: "Which directory should I inspect?",
	})
	sh := &Shell{
		config:       config.Default(),
		agentHandler: NewAgentHandler(agent.NewClient(mock)),
		responseUI:   NewResponseUI(&out),
		agentOutput:  NewAgentOutputCoordinator(&out),
		agentReplyInputHook: func(ctx context.Context) (string, error) {
			replyCalled = true
			return "internal/shell", nil
		},
	}

	sh.handleAgentFullStreaming(context.Background(), parser.ParseResult{
		Type:        parser.CommandTypeAgent,
		AgentPrompt: "help me choose a file",
	}, "test-model")

	if replyCalled {
		t.Fatal("plain agent questions should not automatically enter conversation mode")
	}
	if got := len(mock.Requests()); got != 1 {
		t.Fatalf("mock recorded %d requests, want only the initial request", got)
	}
	if !strings.Contains(out.String(), "[r: reply]") {
		t.Fatalf("question response should show reply hint, got:\n%s", out.String())
	}
}

func TestHandleAgentFullStreaming_ExplicitConversationPromptStillWaitsForReplyAction(t *testing.T) {
	var out strings.Builder
	replyCalled := false
	mock := agent.NewMockTransport(agent.Response{
		Type:        agent.ResponseTypeExplanation,
		Explanation: "Question 1: Is it a physical object?",
	})
	sh := &Shell{
		config:       config.Default(),
		agentHandler: NewAgentHandler(agent.NewClient(mock)),
		responseUI:   NewResponseUI(&out),
		agentOutput:  NewAgentOutputCoordinator(&out),
		agentReplyInputHook: func(ctx context.Context) (string, error) {
			replyCalled = true
			return "", context.Canceled
		},
	}

	sh.handleAgentFullStreaming(context.Background(), parser.ParseResult{
		Type:        parser.CommandTypeAgent,
		AgentPrompt: "Let's play twenty questions. Ask one question at a time.",
	}, "test-model")

	if replyCalled {
		t.Fatal("conversation prompt text should not bypass the r: reply action")
	}
	if got := len(mock.Requests()); got != 1 {
		t.Fatalf("mock recorded %d requests, want only the initial request", got)
	}
	if !strings.Contains(out.String(), "[r: reply]") {
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

	sh.handleAgentFullStreaming(context.Background(), parser.ParseResult{
		Type:        parser.CommandTypeAgent,
		AgentPrompt: "I'll think of something, and you ask up to 20 yes/no questions to guess it.",
	}, "test-model")

	output := out.String()
	if strings.Contains(output, "cmd ·") {
		t.Fatalf("natural-language question should not show command confirmation, got:\n%s", output)
	}
	if !strings.Contains(output, "[r: reply]") {
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

	sh.handleAgentFullStreaming(context.Background(), parser.ParseResult{
		Type:        parser.CommandTypeAgent,
		AgentPrompt: "hello",
	}, "test-model")

	output := out.String()
	if !strings.Contains(output, emptyAgentResponseMessage) {
		t.Fatalf("expected empty response error, got:\n%s", output)
	}
	if strings.Contains(output, "[Enter: retry]") {
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

	_, _, _, ok := sh.streamAgentFollowUpTurn(
		context.Background(),
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
	if strings.Contains(output, "[Enter: retry]") {
		t.Fatalf("empty response should not show retry confirmation, got:\n%s", output)
	}
}
