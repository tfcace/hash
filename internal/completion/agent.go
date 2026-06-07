package completion

import (
	"context"
	"strings"

	"github.com/tfcace/hash/internal/agent"
)

// AgentCompleter provides AI-assisted completions via ??.
type AgentCompleter struct {
	client       *agent.Client
	buildContext func(context.Context) agent.Context
}

// NewAgentCompleter creates a new agent completer.
func NewAgentCompleter(client *agent.Client) *AgentCompleter {
	return &AgentCompleter{
		client:       client,
		buildContext: buildInlineAgentContext,
	}
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

	agentCtx, err := c.contextForRequest(ctx)
	if err != nil {
		return Result{}, err
	}

	// Build request
	req := agent.Request{
		Prompt:      "Complete this command argument: " + promptPart,
		CommandLine: cmdPart,
		Context:     agentCtx,
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

func (c *AgentCompleter) contextForRequest(ctx context.Context) (agent.Context, error) {
	buildContext := c.buildContext
	if buildContext == nil {
		buildContext = buildInlineAgentContext
	}

	ch := make(chan agent.Context, 1)
	go func() {
		ch <- buildContext(ctx)
	}()

	select {
	case <-ctx.Done():
		return agent.Context{}, ctx.Err()
	case agentCtx := <-ch:
		if err := ctx.Err(); err != nil {
			return agent.Context{}, err
		}
		return agentCtx, nil
	}
}

func buildInlineAgentContext(ctx context.Context) agent.Context {
	if ctx.Err() != nil {
		return agent.Context{}
	}
	return agent.NewContextBuilder().Build()
}
