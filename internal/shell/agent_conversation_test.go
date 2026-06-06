package shell

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/tfcace/hash/internal/agent"
	"github.com/tfcace/hash/internal/config"
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
	if agentConversationReplyPrompt != "you> " {
		t.Fatalf("agentConversationReplyPrompt = %q, want user-labeled prompt", agentConversationReplyPrompt)
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
	if !strings.Contains(output, "empty agent response") {
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
	if !strings.Contains(output, "empty agent response") {
		t.Fatalf("expected empty response error, got:\n%s", output)
	}
	if strings.Contains(output, "[Enter: retry]") {
		t.Fatalf("empty response should not show retry confirmation, got:\n%s", output)
	}
}
