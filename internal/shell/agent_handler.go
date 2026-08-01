package shell

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/tfcace/hash/internal/agent"
	"github.com/tfcace/hash/internal/clipboard"
	hashcontext "github.com/tfcace/hash/internal/context"
	"github.com/tfcace/hash/internal/parser"
	"github.com/tfcace/hash/internal/trace"
)

// LastError holds context from the most recent failed command.
type LastError struct {
	Command  string
	Stderr   string
	ExitCode int
}

// AgentHandler handles agent requests.
type AgentHandler struct {
	client          *agent.Client
	clipboardBuf    *clipboard.Buffer
	selectedContext *hashcontext.Collection
	lastError       *LastError
}

// NewAgentHandler creates a new agent handler.
func NewAgentHandler(client *agent.Client) *AgentHandler {
	return &AgentHandler{client: client}
}

// SetClipboard sets the clipboard buffer for context.
func (h *AgentHandler) SetClipboard(buf *clipboard.Buffer) {
	h.clipboardBuf = buf
}

// SetSelectedContext sets the user-selected context.
func (h *AgentHandler) SetSelectedContext(ctx *hashcontext.Collection) {
	h.selectedContext = ctx
}

// SetLastError records the last failed command for agent context.
func (h *AgentHandler) SetLastError(le *LastError) {
	h.lastError = le
}

// CurrentModel returns the active agent model's display name, or "" if none.
func (h *AgentHandler) CurrentModel() string {
	if h == nil || h.client == nil {
		return ""
	}
	return h.client.CurrentModel()
}

// AvailableModels returns the models the agent advertises, or nil.
func (h *AgentHandler) AvailableModels() []agent.ModelOption {
	if h == nil || h.client == nil {
		return nil
	}
	return h.client.AvailableModels()
}

// SetModel selects a model by value and remembers it for the session.
func (h *AgentHandler) SetModel(ctx context.Context, value string) error {
	if h == nil || h.client == nil {
		return fmt.Errorf("no agent configured")
	}
	return h.client.SetModel(ctx, value)
}

// EnsureModelInfo populates the agent's cached model information.
func (h *AgentHandler) EnsureModelInfo(ctx context.Context) error {
	if h == nil || h.client == nil {
		return fmt.Errorf("no agent configured")
	}
	return h.client.EnsureModelInfo(ctx)
}

// AskText sends a raw prompt to the agent and returns its reply as plain
// text, regardless of whether the agent classified it as a command or an
// explanation. Used by builtins that drive the agent directly (e.g.
// `completions generate`).
func (h *AgentHandler) AskText(ctx context.Context, prompt string) (string, error) {
	if h == nil || h.client == nil {
		return "", fmt.Errorf("no agent configured")
	}

	resp, err := h.client.Ask(ctx, agent.Request{Prompt: prompt})
	if err != nil {
		return "", err
	}
	if resp.Type == agent.ResponseTypeError {
		return "", fmt.Errorf("%s", resp.Error)
	}
	if resp.Explanation != "" {
		return resp.Explanation, nil
	}
	return resp.Command, nil
}

// HandleRequest processes a parsed agent request and returns the response.
func (h *AgentHandler) HandleRequest(ctx context.Context, parsed parser.ParseResult) (agent.Response, error) {
	if h.client == nil {
		return agent.Response{}, fmt.Errorf("no agent configured")
	}

	req, err := h.buildRequest(parsed)
	if err != nil {
		return agent.Response{}, err
	}

	return h.client.Ask(ctx, req)
}

// StreamRequest processes a parsed agent request and returns streaming channels.
// Text chunks arrive on the text channel, errors on the error channel.
//
//nolint:gocritic // unnamedResult: can't name receive-only channel returns
func (h *AgentHandler) StreamRequest(ctx context.Context, parsed parser.ParseResult) (<-chan string, <-chan error) {
	trace.AgentHigh("ghost_start", map[string]any{
		"type":   parsed.Type.String(),
		"prompt": parsed.AgentPrompt,
	})

	if h.client == nil {
		errCh := make(chan error, 1)
		errCh <- fmt.Errorf("no agent configured")
		close(errCh)
		trace.AgentHigh("ghost_state", map[string]any{
			"from":   "init",
			"to":     "error",
			"reason": "no_client",
		})
		return nil, errCh
	}

	req, err := h.buildRequest(parsed)
	if err != nil {
		errCh := make(chan error, 1)
		errCh <- err
		close(errCh)
		return nil, errCh
	}

	textCh, errCh := h.client.StreamRequest(ctx, req)

	// Wrap channels to add tracing - single goroutine to avoid race between
	// "complete" and "error" state transitions
	tracedTextCh := make(chan string, 1)
	tracedErrCh := make(chan error, 1)

	go func() {
		defer close(tracedTextCh)
		defer close(tracedErrCh)

		totalLen := 0
		var streamErr error
		textDone := false
		errDone := false

		for !textDone || !errDone {
			select {
			case text, ok := <-textCh:
				if !ok {
					textDone = true
					textCh = nil // Stop selecting on this channel
					continue
				}
				totalLen += len(text)
				trace.Agent("ghost_chunk", map[string]any{
					"text":      text,
					"total_len": totalLen,
				})
				tracedTextCh <- text

			case err, ok := <-errCh:
				if !ok {
					errDone = true
					errCh = nil // Stop selecting on this channel
					continue
				}
				if err != nil {
					streamErr = err
					trace.AgentHigh("ghost_state", map[string]any{
						"from":   "streaming",
						"to":     "error",
						"reason": err.Error(),
					})
				}
				tracedErrCh <- err
			}
		}

		// Only log complete if there was no error
		if streamErr == nil {
			trace.AgentDetailed("ghost_state", map[string]any{
				"from":      "streaming",
				"to":        "complete",
				"total_len": totalLen,
			})
		}
	}()

	return tracedTextCh, tracedErrCh
}

// StreamEvents processes a parsed request and exposes typed lifecycle events
// when the underlying agent supports them. Text-only transports are adapted by
// agent.Client, so callers do not need vendor-specific branches.
//
//nolint:gocritic // unnamedResult: can't name receive-only channel returns
func (h *AgentHandler) StreamEvents(ctx context.Context, parsed parser.ParseResult) (<-chan agent.StreamEvent, <-chan error) {
	if h.client == nil {
		errCh := make(chan error, 1)
		errCh <- fmt.Errorf("no agent configured")
		close(errCh)
		return nil, errCh
	}

	req, err := h.buildRequest(parsed)
	if err != nil {
		errCh := make(chan error, 1)
		errCh <- err
		close(errCh)
		return nil, errCh
	}

	events, errCh := h.client.StreamEvents(ctx, req)
	traced := make(chan agent.StreamEvent, 16)
	tracedErrs := make(chan error, 1)
	go func() {
		defer close(traced)
		defer close(tracedErrs)
		for events != nil || errCh != nil {
			select {
			case event, ok := <-events:
				if !ok {
					events = nil
					continue
				}
				if event.Type == agent.StreamEventToolCall {
					trace.Agent("tool_lifecycle", map[string]any{
						"id":     event.ToolCall.ID,
						"kind":   event.ToolCall.Kind,
						"status": event.ToolCall.Status,
					})
				}
				traced <- event
			case err, ok := <-errCh:
				if !ok {
					errCh = nil
					continue
				}
				if err != nil {
					tracedErrs <- err
				}
			}
		}
	}()
	return traced, tracedErrs
}

// StreamFollowUp sends a conversation follow-up turn to the agent.
//
//nolint:gocritic // unnamedResult: receive-only channels match Transport style
func (h *AgentHandler) StreamFollowUp(ctx context.Context, reply string, transcript []agentConversationMessage) (<-chan string, <-chan error) {
	if h.client == nil {
		errCh := make(chan error, 1)
		errCh <- fmt.Errorf("no agent configured")
		close(errCh)
		return nil, errCh
	}

	req, err := h.buildFollowUpRequest(reply, transcript)
	if err != nil {
		errCh := make(chan error, 1)
		errCh <- err
		close(errCh)
		return nil, errCh
	}

	return h.client.StreamRequest(ctx, req)
}

//nolint:gocritic // unnamedResult: can't name receive-only channel returns
func (h *AgentHandler) StreamFollowUpEvents(ctx context.Context, reply string, transcript []agentConversationMessage) (<-chan agent.StreamEvent, <-chan error) {
	if h.client == nil {
		errCh := make(chan error, 1)
		errCh <- fmt.Errorf("no agent configured")
		close(errCh)
		return nil, errCh
	}
	req, err := h.buildFollowUpRequest(reply, transcript)
	if err != nil {
		errCh := make(chan error, 1)
		errCh <- err
		close(errCh)
		return nil, errCh
	}
	return h.client.StreamEvents(ctx, req)
}

// buildRequest constructs an agent.Request from a parsed result.
func (h *AgentHandler) buildRequest(parsed parser.ParseResult) (agent.Request, error) {
	agentCtx := h.buildAgentContext()

	// Build the prompt based on parse type
	var prompt string
	switch parsed.Type {
	case parser.CommandTypeAgent:
		prompt = parsed.AgentPrompt
		// Bare ?? after a failed command: auto-inject error context
		if prompt == "" && h.lastError != nil {
			prompt = fmt.Sprintf("Explain this error and suggest a fix:\n$ %s\n%s\nExit code: %d",
				h.lastError.Command, h.lastError.Stderr, h.lastError.ExitCode)
		}
	case parser.CommandTypeAgentPipe:
		if h.clipboardBuf != nil && h.clipboardBuf.LastOutput() == "" {
			prompt = fmt.Sprintf("The command '%s' produced no output (empty). %s", parsed.Command, parsed.AgentPrompt)
		} else {
			prompt = fmt.Sprintf("Given the output of '%s', %s", parsed.Command, parsed.AgentPrompt)
		}
	case parser.CommandTypeAgentInline:
		prompt = fmt.Sprintf(`Complete this shell command argument.

Partial command: %s
What the user wants: %s

Respond with ONLY the completion to append (single line).
- No explanations or multiple lines
- Quote values with spaces (e.g., '%%h %%s' not %%h %%s)
- Shell-safe output only
- Single line response required`, parsed.Command, parsed.AgentPrompt)
	default:
		return agent.Request{}, fmt.Errorf("not an agent request")
	}

	return agent.Request{
		Prompt:      prompt,
		CommandLine: parsed.Command,
		Context:     agentCtx,
	}, nil
}

func (h *AgentHandler) buildFollowUpRequest(reply string, transcript []agentConversationMessage) (agent.Request, error) {
	reply = strings.TrimSpace(reply)
	if reply == "" {
		return agent.Request{}, fmt.Errorf("empty follow-up reply")
	}

	if h.client != nil && h.client.Name() == "acp" {
		return agent.Request{Prompt: buildACPFollowUpPrompt(reply)}, nil
	}

	return agent.Request{
		Prompt:  buildStatelessFollowUpPrompt(reply, transcript),
		Context: h.buildAgentContext(),
	}, nil
}

func buildACPFollowUpPrompt(reply string) string {
	var b strings.Builder
	writeFollowUpTurnInstructions(&b)
	b.WriteString("\nLatest user message: ")
	b.WriteString(reply)
	return b.String()
}

func buildStatelessFollowUpPrompt(reply string, transcript []agentConversationMessage) string {
	var b strings.Builder
	b.WriteString("Continue this conversation. Use the prior turns as context, then answer the latest user message.\n")
	writeFollowUpTurnInstructions(&b)
	b.WriteString("\n")
	b.WriteString("Conversation so far:\n")
	for _, msg := range transcript {
		role := strings.TrimSpace(msg.Role)
		text := strings.TrimSpace(msg.Text)
		if role == "" || text == "" {
			continue
		}
		b.WriteString(roleTitle(role))
		b.WriteString(": ")
		b.WriteString(text)
		b.WriteString("\n")
	}
	b.WriteString("\nLatest user message: ")
	b.WriteString(reply)
	return b.String()
}

func writeFollowUpTurnInstructions(b *strings.Builder) {
	b.WriteString("Treat this as the user's next turn in the current conversation.\n")
	b.WriteString("Side requests are allowed. If the user pauses or redirects, handle it normally, including tool use when appropriate.\n")
	b.WriteString("After a side request, preserve the prior conversation state and resume or ask whether to resume.")
	b.WriteString("\n")
}

func roleTitle(role string) string {
	switch strings.ToLower(role) {
	case "assistant":
		return "Assistant"
	case "user":
		return "User"
	default:
		return role
	}
}

func (h *AgentHandler) buildAgentContext() agent.Context {
	// Build context - use selected context if available, otherwise auto-detect
	var agentCtx agent.Context
	if h.selectedContext != nil && len(h.selectedContext.SelectedItems()) > 0 {
		agentCtx = h.buildContextFromSelection()
	} else {
		ctxBuilder := agent.NewContextBuilder().
			DetectGitBranch().
			DetectKubeContext()

		if h.clipboardBuf != nil {
			if lastOutput := h.clipboardBuf.LastOutput(); lastOutput != "" {
				ctxBuilder.WithLastOutput(lastOutput)
			}
		}
		agentCtx = ctxBuilder.Build()
	}

	// Populate last error context if available
	if h.lastError != nil {
		agentCtx.LastError = fmt.Sprintf("Command '%s' failed (exit %d):\n%s",
			h.lastError.Command, h.lastError.ExitCode, h.lastError.Stderr)
	}

	return agentCtx
}

// buildContextFromSelection converts user-selected context to agent.Context.
func (h *AgentHandler) buildContextFromSelection() agent.Context {
	cwd, _ := os.Getwd()
	agentCtx := agent.Context{
		Cwd:     cwd,
		EnvVars: make(map[string]string),
	}

	for _, item := range h.selectedContext.SelectedItems() {
		switch item.Key {
		case "cwd":
			agentCtx.Cwd = item.Value
		case "git branch":
			agentCtx.GitBranch = item.Value
		case "kube context":
			agentCtx.KubeContext = item.Value
		case "last output":
			agentCtx.LastOutput = item.Value
		case "last error":
			agentCtx.LastError = item.Value
		default:
			// History or env items go to appropriate fields
			switch item.Category {
			case hashcontext.CategoryHistory:
				agentCtx.History = append(agentCtx.History, item.Value)
			case hashcontext.CategoryEnv:
				agentCtx.EnvVars[item.Key] = item.Value
			}
		}
	}

	return agentCtx
}
