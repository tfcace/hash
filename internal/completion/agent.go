package completion

import (
	"context"
	"strings"

	"github.com/tfcace/hash/internal/agent"
)

// AgentCompleter provides AI-assisted completions via ??.
type AgentCompleter struct {
	client *agent.Client
}

// NewAgentCompleter creates a new agent completer.
func NewAgentCompleter(client *agent.Client) *AgentCompleter {
	return &AgentCompleter{client: client}
}

// Name returns the completer name.
func (c *AgentCompleter) Name() string {
	return "agent"
}

// Complete returns AI-assisted completions for ?? inline patterns.
func (c *AgentCompleter) Complete(ctx context.Context, line string, pos int) (Result, error) {
	// Only handle lines with ?? (but not at the start - that's full agent mode)
	lineUpToPos := line[:pos]

	// Find ?? in the line
	idx := strings.Index(lineUpToPos, "??")
	if idx == -1 || idx == 0 {
		return Result{}, nil // No ?? or starts with ?? (full agent mode)
	}

	// Extract the command before ?? and the prompt after
	cmdPart := strings.TrimSpace(lineUpToPos[:idx])
	promptPart := strings.TrimSpace(lineUpToPos[idx+2:])

	if c.client == nil {
		return Result{}, nil // No client configured
	}

	// Build request
	req := agent.Request{
		Prompt:      "Complete this command argument: " + promptPart,
		CommandLine: cmdPart,
		Context:     agent.NewContextBuilder().DetectGitBranch().Build(),
	}

	resp, err := c.client.Ask(ctx, req)
	if err != nil {
		return Result{}, err
	}

	if resp.Type != agent.ResponseTypeCommand {
		return Result{}, nil
	}

	// Return the suggested completion
	return Result{
		Items: []Item{
			{
				Value:       resp.Command,
				Display:     resp.Command,
				Description: "AI suggestion",
				Icon:        "🤖",
			},
		},
	}, nil
}
