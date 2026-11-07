package shell

import (
	"context"
	"strings"
	"testing"

	"github.com/tfcace/hash/internal/agent"
	"github.com/tfcace/hash/internal/parser"
)

type namedTestTransport struct {
	name string
}

func (t namedTestTransport) Connect(ctx context.Context) error {
	return nil
}

func (t namedTestTransport) SendStreaming(ctx context.Context, req agent.Request) (<-chan string, <-chan error) {
	textCh := make(chan string)
	errCh := make(chan error)
	close(textCh)
	close(errCh)
	return textCh, errCh
}

func (t namedTestTransport) Close() error {
	return nil
}

func (t namedTestTransport) Name() string {
	return t.name
}

func (t namedTestTransport) CurrentModel() string                   { return "" }
func (t namedTestTransport) AvailableModels() []agent.ModelOption   { return nil }
func (t namedTestTransport) SetModel(context.Context, string) error { return nil }
func (t namedTestTransport) EnsureModelInfo(context.Context) error  { return nil }

func TestBuildRequest_BareAgentAfterError(t *testing.T) {
	handler := &AgentHandler{}
	facts := requestFacts{lastError: &LastError{
		Command:  "hash-upgrrade",
		Stderr:   "hash-upgrrade: command not found",
		ExitCode: 127,
	}}

	parsed := parser.ParseResult{
		Type:        parser.CommandTypeAgent,
		AgentPrompt: "", // bare ??
	}

	req, err := handler.buildRequest(parsed, facts)
	if err != nil {
		t.Fatalf("buildRequest() error = %v", err)
	}

	// Prompt should contain the failed command and error
	if !strings.Contains(req.Prompt, "hash-upgrrade") {
		t.Errorf("prompt should contain failed command, got %q", req.Prompt)
	}
	if !strings.Contains(req.Prompt, "command not found") {
		t.Errorf("prompt should contain stderr, got %q", req.Prompt)
	}
	if !strings.Contains(req.Prompt, "127") {
		t.Errorf("prompt should contain exit code, got %q", req.Prompt)
	}

	// Context.LastError should also be populated
	if !strings.Contains(req.Context.LastError, "hash-upgrrade") {
		t.Errorf("Context.LastError should contain failed command, got %q", req.Context.LastError)
	}
}

func TestBuildRequest_BareAgentNoError(t *testing.T) {
	handler := &AgentHandler{}
	// No last error set (last command succeeded)

	parsed := parser.ParseResult{
		Type:        parser.CommandTypeAgent,
		AgentPrompt: "", // bare ??
	}

	req, err := handler.buildRequest(parsed, requestFacts{})
	if err != nil {
		t.Fatalf("buildRequest() error = %v", err)
	}

	// Bare ?? with no error: prompt stays empty
	if req.Prompt != "" {
		t.Errorf("prompt should be empty when no error, got %q", req.Prompt)
	}
	if req.Context.LastError != "" {
		t.Errorf("Context.LastError should be empty, got %q", req.Context.LastError)
	}
}

func TestBuildRequest_AgentWithPromptAfterError(t *testing.T) {
	handler := &AgentHandler{}
	facts := requestFacts{lastError: &LastError{
		Command:  "make build",
		Stderr:   "undefined reference to main",
		ExitCode: 2,
	}}

	parsed := parser.ParseResult{
		Type:        parser.CommandTypeAgent,
		AgentPrompt: "how do I fix this",
	}

	req, err := handler.buildRequest(parsed, facts)
	if err != nil {
		t.Fatalf("buildRequest() error = %v", err)
	}

	// Explicit prompt should be used as-is, not replaced
	if req.Prompt != "how do I fix this" {
		t.Errorf("prompt should be user's prompt, got %q", req.Prompt)
	}

	// But Context.LastError should still be populated
	if !strings.Contains(req.Context.LastError, "make build") {
		t.Errorf("Context.LastError should contain failed command, got %q", req.Context.LastError)
	}
	if !strings.Contains(req.Context.LastError, "undefined reference") {
		t.Errorf("Context.LastError should contain stderr, got %q", req.Context.LastError)
	}
}

func TestBuildRequest_PipeModeIgnoresLastError(t *testing.T) {
	handler := &AgentHandler{}
	facts := requestFacts{lastError: &LastError{
		Command:  "bad-cmd",
		Stderr:   "not found",
		ExitCode: 127,
	}}

	parsed := parser.ParseResult{
		Type:        parser.CommandTypeAgentPipe,
		Command:     "ls -la",
		AgentPrompt: "filter hidden files",
	}

	req, err := handler.buildRequest(parsed, facts)
	if err != nil {
		t.Fatalf("buildRequest() error = %v", err)
	}

	// Pipe mode uses its own prompt format, not the error context
	if !strings.Contains(req.Prompt, "ls -la") {
		t.Errorf("pipe prompt should contain command, got %q", req.Prompt)
	}
	if !strings.Contains(req.Prompt, "filter hidden files") {
		t.Errorf("pipe prompt should contain user prompt, got %q", req.Prompt)
	}

	// But Context.LastError is still available for the agent
	if !strings.Contains(req.Context.LastError, "bad-cmd") {
		t.Errorf("Context.LastError should still be set, got %q", req.Context.LastError)
	}
}

func TestBuildFollowUpRequest_ACPFramesSideRequestsWithoutTranscript(t *testing.T) {
	handler := NewAgentHandler(agent.NewClient(namedTestTransport{name: "acp"}))
	transcript := []agentConversationMessage{
		{Role: "user", Text: "Let's play twenty questions."},
		{Role: "assistant", Text: "Is it in the repository?"},
	}

	req, err := handler.buildFollowUpRequest("let's pause the game and list my kubernetes contexts", transcript, requestFacts{})
	if err != nil {
		t.Fatalf("buildFollowUpRequest() error = %v", err)
	}

	want := []string{
		"Treat this as the user's next turn in the current conversation.",
		"handle it normally, including tool use",
		"preserve the prior conversation state",
		"Latest user message: let's pause the game and list my kubernetes contexts",
	}
	for _, substr := range want {
		if !strings.Contains(req.Prompt, substr) {
			t.Fatalf("Prompt missing %q:\n%s", substr, req.Prompt)
		}
	}
	if strings.Contains(req.Prompt, "Let's play twenty questions.") {
		t.Fatalf("ACP follow-up should not duplicate transcript, got %q", req.Prompt)
	}
}

func TestBuildFollowUpRequest_StatelessTransportIncludesTranscript(t *testing.T) {
	handler := NewAgentHandler(agent.NewClient(agent.NewMockTransport()))
	transcript := []agentConversationMessage{
		{Role: "user", Text: "Find my config files"},
		{Role: "assistant", Text: "Which directory should I inspect?"},
	}

	req, err := handler.buildFollowUpRequest("internal/shell", transcript, requestFacts{})
	if err != nil {
		t.Fatalf("buildFollowUpRequest() error = %v", err)
	}

	want := []string{
		"Continue this conversation.",
		"Side requests are allowed.",
		"including tool use",
		"resume or ask whether to resume",
		"User: Find my config files",
		"Assistant: Which directory should I inspect?",
		"Latest user message: internal/shell",
	}
	for _, substr := range want {
		if !strings.Contains(req.Prompt, substr) {
			t.Fatalf("Prompt missing %q:\n%s", substr, req.Prompt)
		}
	}
}

func TestBuildRequest_InlineIsMarkedInline(t *testing.T) {
	handler := &AgentHandler{}
	parsed := parser.ParseResult{
		Type:        parser.CommandTypeAgentInline,
		Command:     "git log --format=",
		AgentPrompt: "oneline with hash",
	}

	req, err := handler.buildRequest(parsed, requestFacts{})
	if err != nil {
		t.Fatalf("buildRequest() error = %v", err)
	}
	if !req.Inline {
		t.Error("inline completion request should be marked Inline")
	}
}

func TestBuildRequest_FullAndPipeAreNotInline(t *testing.T) {
	handler := &AgentHandler{}
	for _, typ := range []parser.CommandType{parser.CommandTypeAgent, parser.CommandTypeAgentPipe} {
		parsed := parser.ParseResult{Type: typ, Command: "cat go.mod", AgentPrompt: "list deps"}
		req, err := handler.buildRequest(parsed, requestFacts{})
		if err != nil {
			t.Fatalf("buildRequest(%v) error = %v", typ, err)
		}
		if req.Inline {
			t.Errorf("%v request should not be marked Inline", typ)
		}
	}
}

// modelsTestTransport exposes a model list that changes when the transport
// is closed and reopened, the way a freshly installed adapter would.
type modelsTestTransport struct {
	namedTestTransport
	models    []agent.ModelOption
	next      []agent.ModelOption
	current   string
	closes    int
	ensures   int
	ensureErr error
}

func (t *modelsTestTransport) Close() error {
	t.closes++
	if t.next != nil {
		t.models, t.next = t.next, nil
	}
	return nil
}

func (t *modelsTestTransport) EnsureModelInfo(context.Context) error {
	t.ensures++
	return t.ensureErr
}

func (t *modelsTestTransport) AvailableModels() []agent.ModelOption { return t.models }
func (t *modelsTestTransport) CurrentModel() string                 { return t.current }

func TestAgentHandler_RestartReloadsModels(t *testing.T) {
	tr := &modelsTestTransport{
		models: []agent.ModelOption{{Value: "default", Name: "Default"}},
		next:   []agent.ModelOption{{Value: "default", Name: "Default"}, {Value: "haiku", Name: "Haiku"}},
	}
	h := NewAgentHandler(agent.NewClient(tr))

	if err := h.Restart(context.Background()); err != nil {
		t.Fatalf("Restart() error = %v", err)
	}
	if tr.closes != 1 || tr.ensures != 1 {
		t.Errorf("closes = %d, ensures = %d, want 1 and 1", tr.closes, tr.ensures)
	}
	if len(h.AvailableModels()) != 2 {
		t.Errorf("AvailableModels() after restart = %v, want the new list", h.AvailableModels())
	}
}

func TestAgentHandler_RestartWithoutAgent(t *testing.T) {
	var h *AgentHandler
	if err := h.Restart(context.Background()); err == nil {
		t.Error("Restart() on nil handler = nil error, want an error")
	}
}

// replyTestTransport streams one fixed reply.
type replyTestTransport struct {
	namedTestTransport
	reply string
}

func (t replyTestTransport) SendStreaming(context.Context, agent.Request) (<-chan string, <-chan error) {
	textCh := make(chan string, 1)
	errCh := make(chan error)
	textCh <- t.reply
	close(textCh)
	close(errCh)
	return textCh, errCh
}

// An inline reply is the text to append to the partial command, exactly as
// the ghost text shows it. It is not a command on its own, so it must not be
// filed as an explanation and come back as an empty fill.
func TestAgentHandler_HandleRequestInlineReplyIsTheFill(t *testing.T) {
	h := NewAgentHandler(agent.NewClient(replyTestTransport{reply: "%h %s"}))
	parsed := parser.Parse("git log --format=?? oneline with hash")

	resp, err := h.HandleRequest(context.Background(), parsed)
	if err != nil {
		t.Fatalf("HandleRequest() error = %v", err)
	}
	if resp.Type != agent.ResponseTypeCommand || resp.Command != "%h %s" {
		t.Fatalf("HandleRequest() = %+v, want the command fill %q", resp, "%h %s")
	}
}

// The e2e suites read the fill through HandleRequest: it must be the same
// fill the ghost text shows, without the backticks the model wraps it in.
func TestAgentHandler_HandleRequestInlineFillIsUnwrapped(t *testing.T) {
	h := NewAgentHandler(agent.NewClient(replyTestTransport{reply: "`internal/shell/*.go`"}))
	parsed := parser.Parse("ls ?? only the go files in internal/shell")

	resp, err := h.HandleRequest(context.Background(), parsed)
	if err != nil {
		t.Fatalf("HandleRequest() error = %v", err)
	}
	if resp.Command != "internal/shell/*.go" {
		t.Fatalf("HandleRequest() = %+v, want the unwrapped fill", resp)
	}
}

func TestBuildRequest_InlineAsksForNoMarkdown(t *testing.T) {
	req, err := (&AgentHandler{}).buildRequest(parser.Parse("ls ?? go files"), requestFacts{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(req.Prompt, "no backticks") {
		t.Errorf("inline prompt does not rule out backticks:\n%s", req.Prompt)
	}
}
