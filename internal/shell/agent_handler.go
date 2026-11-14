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
	client       *agent.Client
	clipboardBuf *clipboard.Buffer
}

// requestFacts is what a ?? turn's requests tell the agent about the shell
// beside the prompt. They are fixed when the turn starts and passed with
// each request, the first and its follow-ups alike.
type requestFacts struct {
	selection *hashcontext.Collection // what Ctrl+P picked, nil = auto-detect
	lastError *LastError              // the failed line a bare ?? explains, nil when the last line did not fail
}

// NewAgentHandler creates a new agent handler.
func NewAgentHandler(client *agent.Client) *AgentHandler {
	return &AgentHandler{client: client}
}

// SetClipboard sets the clipboard buffer for context.
func (h *AgentHandler) SetClipboard(buf *clipboard.Buffer) {
	h.clipboardBuf = buf
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

// Restart drops the agent process and session and reconnects, so a freshly
// installed adapter is the one answering. The model list is reloaded, and a
// model the user pinned survives: the transport keeps the pin across Close
// and re-applies it to the new session.
func (h *AgentHandler) Restart(ctx context.Context) error {
	if h == nil || h.client == nil {
		return fmt.Errorf("no agent configured")
	}
	return h.client.Restart(ctx)
}

// Close ends the agent session and stops its process, if any.
func (h *AgentHandler) Close() error {
	if h == nil || h.client == nil {
		return nil
	}
	return h.client.Close()
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
		return "", readableAgentErr(err)
	}
	if resp.Type == agent.ResponseTypeError {
		return "", fmt.Errorf("%s", resp.Error)
	}
	if resp.Explanation != "" {
		return resp.Explanation, nil
	}
	return resp.Command, nil
}

// HandleRequest sends a parsed request and returns the agent's whole
// response. The e2e suites drive the agent through it. An inline reply is the
// fill appended to the partial command, as the ghost text shows it (markdown
// wrapper stripped), so it is returned as the command whatever it looks like
// on its own.
func (h *AgentHandler) HandleRequest(ctx context.Context, parsed parser.ParseResult) (agent.Response, error) {
	if h == nil || h.client == nil {
		return agent.Response{}, fmt.Errorf("no agent configured")
	}
	req, err := h.buildRequest(ctx, parsed, requestFacts{})
	if err != nil {
		return agent.Response{}, err
	}
	resp, err := h.client.Ask(ctx, req)
	if err != nil || !req.Inline || resp.Type == agent.ResponseTypeError {
		return resp, err
	}
	reply := resp.Command
	if resp.Type == agent.ResponseTypeExplanation {
		reply = resp.Explanation
	}
	return agent.Response{Type: agent.ResponseTypeCommand, Command: unwrapInlineFill(reply)}, nil
}

// StreamEvents processes a parsed request and exposes typed lifecycle events
// when the underlying agent supports them. Text-only transports are adapted by
// agent.Client, so callers do not need vendor-specific branches.
//
//nolint:gocritic // unnamedResult: can't name receive-only channel returns
func (h *AgentHandler) StreamEvents(ctx context.Context, parsed parser.ParseResult, facts requestFacts) (<-chan agent.StreamEvent, <-chan error) {
	if h.client == nil {
		errCh := make(chan error, 1)
		errCh <- fmt.Errorf("no agent configured")
		close(errCh)
		return nil, errCh
	}

	req, err := h.buildRequest(ctx, parsed, facts)
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
				select {
				case traced <- event:
				case <-ctx.Done():
					return // the request ended: no one reads on
				}
			case err, ok := <-errCh:
				if !ok {
					errCh = nil
					continue
				}
				if err != nil {
					select {
					case tracedErrs <- err:
					case <-ctx.Done():
						return
					}
				}
			}
		}
	}()
	return traced, tracedErrs
}

//nolint:gocritic // unnamedResult: can't name receive-only channel returns
func (h *AgentHandler) StreamFollowUpEvents(ctx context.Context, reply string, transcript []agentConversationMessage, facts requestFacts) (<-chan agent.StreamEvent, <-chan error) {
	if h.client == nil {
		errCh := make(chan error, 1)
		errCh <- fmt.Errorf("no agent configured")
		close(errCh)
		return nil, errCh
	}
	req, err := h.buildFollowUpRequest(ctx, reply, transcript, facts)
	if err != nil {
		errCh := make(chan error, 1)
		errCh <- err
		close(errCh)
		return nil, errCh
	}
	return h.client.StreamEvents(ctx, req)
}

// buildRequest constructs an agent.Request from a parsed result.
func (h *AgentHandler) buildRequest(ctx context.Context, parsed parser.ParseResult, facts requestFacts) (agent.Request, error) {
	agentCtx := h.buildAgentContext(ctx, facts)
	if err := ctx.Err(); err != nil {
		return agent.Request{}, err
	}

	// Build the prompt based on parse type
	var prompt string
	switch parsed.Type {
	case parser.CommandTypeAgent:
		prompt = parsed.AgentPrompt
		// Bare ?? after a failed command: auto-inject error context
		if le := facts.lastError; prompt == "" && le != nil {
			prompt = fmt.Sprintf("Explain this error and suggest a fix:\n$ %s\n%s\nExit code: %d",
				le.Command, le.Stderr, le.ExitCode)
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
- Plain text: no markdown, no backticks, no code fences
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
		Inline:      parsed.Type == parser.CommandTypeAgentInline,
	}, nil
}

func (h *AgentHandler) buildFollowUpRequest(ctx context.Context, reply string, transcript []agentConversationMessage, facts requestFacts) (agent.Request, error) {
	reply = strings.TrimSpace(reply)
	if reply == "" {
		return agent.Request{}, fmt.Errorf("empty follow-up reply")
	}

	if h.client != nil && h.client.Name() == "acp" {
		return agent.Request{Prompt: buildACPFollowUpPrompt(reply)}, nil
	}

	return agent.Request{
		Prompt:  buildStatelessFollowUpPrompt(reply, transcript),
		Context: h.buildAgentContext(ctx, facts),
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

func (h *AgentHandler) buildAgentContext(ctx context.Context, facts requestFacts) agent.Context {
	// Build context: what the user picked with Ctrl+P (even nothing), or
	// else the auto-detected defaults
	var agentCtx agent.Context
	if facts.selection != nil {
		agentCtx = buildContextFromSelection(facts.selection)
	} else {
		ctxBuilder := agent.NewContextBuilder().
			DetectGitBranch(ctx).
			DetectKubeContext(ctx)

		if h.clipboardBuf != nil {
			if lastOutput := h.clipboardBuf.LastOutput(); lastOutput != "" {
				ctxBuilder.WithLastOutput(lastOutput)
			}
		}
		agentCtx = ctxBuilder.Build()
	}

	// Populate last error context if available
	if le := facts.lastError; le != nil {
		agentCtx.LastError = fmt.Sprintf("Command '%s' failed (exit %d):\n%s",
			le.Command, le.ExitCode, le.Stderr)
	}

	return agentCtx
}

// buildContextFromSelection converts user-selected context to agent.Context.
func buildContextFromSelection(selection *hashcontext.Collection) agent.Context {
	cwd, _ := os.Getwd()
	agentCtx := agent.Context{
		Cwd:     cwd,
		EnvVars: make(map[string]string),
	}

	// Category first: a history item's Key is the whole command, which can
	// read like a fact's name ("git branch").
	for _, item := range selection.SelectedItems() {
		switch item.Category {
		case hashcontext.CategoryHistory:
			agentCtx.History = append(agentCtx.History, item.Value)
		case hashcontext.CategoryEnv:
			agentCtx.EnvVars[item.Key] = item.Value
		case hashcontext.CategoryAutoDetect:
			applyContextFact(&agentCtx, item)
		}
	}

	return agentCtx
}

// applyContextFact sets the field an auto-detected item stands for.
func applyContextFact(agentCtx *agent.Context, item hashcontext.Item) {
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
	}
}
