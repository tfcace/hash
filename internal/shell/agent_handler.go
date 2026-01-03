package shell

import (
	"context"
	"fmt"

	"github.com/tfcace/hash/internal/agent"
	"github.com/tfcace/hash/internal/clipboard"
	"github.com/tfcace/hash/internal/parser"
)

// AgentHandler handles agent requests.
type AgentHandler struct {
	client       *agent.Client
	clipboardBuf *clipboard.Buffer
}

// NewAgentHandler creates a new agent handler.
func NewAgentHandler(client *agent.Client) *AgentHandler {
	return &AgentHandler{client: client}
}

// SetClipboard sets the clipboard buffer for context.
func (h *AgentHandler) SetClipboard(buf *clipboard.Buffer) {
	h.clipboardBuf = buf
}

// HandleRequest processes a parsed agent request and returns the response.
func (h *AgentHandler) HandleRequest(ctx context.Context, parsed parser.ParseResult) (agent.Response, error) {
	if h.client == nil {
		return agent.Response{}, fmt.Errorf("no agent configured")
	}

	// Build context with auto-detected info and clipboard data
	ctxBuilder := agent.NewContextBuilder().
		DetectGitBranch().
		DetectKubeContext()

	// Add last output/error from clipboard buffer if available
	if h.clipboardBuf != nil {
		if lastOutput := h.clipboardBuf.LastOutput(); lastOutput != "" {
			ctxBuilder.WithLastOutput(lastOutput)
		}
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
		Context:     ctxBuilder.Build(),
	}

	return h.client.Ask(ctx, req)
}
