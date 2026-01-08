package shell

import (
	"context"
	"fmt"
	"os"

	"github.com/tfcace/hash/internal/agent"
	"github.com/tfcace/hash/internal/clipboard"
	hashcontext "github.com/tfcace/hash/internal/context"
	"github.com/tfcace/hash/internal/parser"
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

	// Build context - use selected context if available, otherwise auto-detect
	var agentCtx agent.Context
	if h.selectedContext != nil && len(h.selectedContext.SelectedItems()) > 0 {
		// Use user-selected context from picker
		agentCtx = h.buildContextFromSelection()
	} else {
		// Default auto-detect behavior
		ctxBuilder := agent.NewContextBuilder().
			DetectGitBranch().
			DetectKubeContext()

		// Add last output/error from clipboard buffer if available
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
		prompt = fmt.Sprintf("Given the output of '%s', %s", parsed.Command, parsed.AgentPrompt)
	case parser.CommandTypeAgentInline:
		prompt = fmt.Sprintf(`Complete this shell command argument.

Partial command: %s
What the user wants: %s

Respond with ONLY the value to append.
- No explanations
- Quote values with spaces (e.g., '%%h %%s' not %%h %%s)
- Shell-safe output only`, parsed.Command, parsed.AgentPrompt)
	default:
		return agent.Response{}, fmt.Errorf("not an agent request")
	}

	req := agent.Request{
		Prompt:      prompt,
		CommandLine: parsed.Command,
		Context:     agentCtx,
	}

	return h.client.Ask(ctx, req)
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
			if item.Category == hashcontext.CategoryHistory {
				agentCtx.History = append(agentCtx.History, item.Value)
			} else if item.Category == hashcontext.CategoryEnv {
				agentCtx.EnvVars[item.Key] = item.Value
			}
		}
	}

	return agentCtx
}
