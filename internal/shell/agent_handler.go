package shell

import (
	"context"
	"fmt"
	"os"

	"github.com/tfcace/hash/internal/agent"
	"github.com/tfcace/hash/internal/clipboard"
	hashcontext "github.com/tfcace/hash/internal/context"
	"github.com/tfcace/hash/internal/parser"
	"github.com/tfcace/hash/internal/trace"
)

// AgentHandler handles agent requests.
type AgentHandler struct {
	client          *agent.Client
	clipboardBuf    *clipboard.Buffer
	selectedContext *hashcontext.Collection
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

// buildRequest constructs an agent.Request from a parsed result.
func (h *AgentHandler) buildRequest(parsed parser.ParseResult) (agent.Request, error) {
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

	// Build the prompt based on parse type
	var prompt string
	switch parsed.Type {
	case parser.CommandTypeAgent:
		prompt = parsed.AgentPrompt
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
