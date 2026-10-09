package shell

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/tfcace/hash/internal/agent"
	"github.com/tfcace/hash/internal/config"
	"github.com/tfcace/hash/internal/parser"
)

func TestHandleAgentStreamError_NoResponseStartupShowsTroubleshooting(t *testing.T) {
	var out bytes.Buffer
	sh := &Shell{
		config:      &config.Config{},
		responseUI:  NewResponseUI(&out),
		agentOutput: NewAgentOutputCoordinator(&out),
	}
	sh.config.Agent.Transport = "stdio"
	sh.config.Agent.Command = "claude-agent-acp"

	err := sh.handleAgentStreamError(
		context.Background(),
		parser.ParseResult{},
		"claude-agent-acp",
		errors.Join(agent.ErrACPStartFailed, exec.ErrNotFound),
		0,
		0,
	)

	if err != nil {
		t.Fatalf("handleAgentStreamError() error = %v, want nil once handled", err)
	}

	output := out.String()
	if !strings.Contains(output, "Troubleshooting") {
		t.Fatalf("expected troubleshooting output, got:\n%s", output)
	}
	if !strings.Contains(output, "installed") {
		t.Fatalf("expected install hint in output, got:\n%s", output)
	}
}

func TestHandleAgentStreamError_NoResponseTimeoutDoesNotShowTroubleshooting(t *testing.T) {
	var out bytes.Buffer
	sh := &Shell{
		config:      &config.Config{},
		responseUI:  NewResponseUI(&out),
		agentOutput: NewAgentOutputCoordinator(&out),
	}

	err := sh.handleAgentStreamError(
		context.Background(),
		parser.ParseResult{},
		"claude-agent-acp",
		errors.Join(agent.ErrACPIdleTimeout, context.DeadlineExceeded),
		0,
		0,
	)

	if err != nil {
		t.Fatalf("handleAgentStreamError() error = %v, want nil once handled", err)
	}

	output := out.String()
	if strings.Contains(output, "Troubleshooting") {
		t.Fatalf("did not expect troubleshooting hints for timeout, got:\n%s", output)
	}
	if !strings.Contains(output, "[Enter: retry]") {
		t.Fatalf("expected retry hint for timeout, got:\n%s", output)
	}
}

func TestHandleAgentStreamError_NoOutputUsesUserFacingRetry(t *testing.T) {
	var out bytes.Buffer
	sh := &Shell{
		config:      &config.Config{},
		responseUI:  NewResponseUI(&out),
		agentOutput: NewAgentOutputCoordinator(&out),
	}

	err := sh.handleAgentStreamError(
		context.Background(),
		parser.ParseResult{},
		"claude-agent-acp",
		errors.Join(agent.ErrACPNoOutput, errors.New("prompt completed without displayable text (stopReason=end_turn)")),
		0,
		0,
	)

	if err != nil {
		t.Fatalf("handleAgentStreamError() error = %v, want nil once handled", err)
	}

	output := out.String()
	if !strings.Contains(output, "agent ended the turn without text") {
		t.Fatalf("expected user-facing no-output message, got:\n%s", output)
	}
	if strings.Contains(output, "acp prompt completed") || strings.Contains(output, "stopReason=end_turn") {
		t.Fatalf("expected protocol details to stay out of the UI, got:\n%s", output)
	}
	if !strings.Contains(output, "[Enter: retry]") {
		t.Fatalf("expected retry hint for no-output error, got:\n%s", output)
	}
}

func TestHandleAgentStreamError_ModelErrorPointsAtModelBuiltin(t *testing.T) {
	var out bytes.Buffer
	sh := &Shell{
		config:      &config.Config{},
		responseUI:  NewResponseUI(&out),
		agentOutput: NewAgentOutputCoordinator(&out),
	}

	err := sh.handleAgentStreamError(
		context.Background(),
		parser.ParseResult{},
		"claude-agent-acp",
		errors.New("rpc error -32603: Internal error: There's an issue with the selected model (fable[1m]). It may not exist or you may not have access to it. Run --model to pick a different model."),
		0,
		0,
	)

	if err != nil {
		t.Fatalf("handleAgentStreamError() error = %v, want nil once handled", err)
	}
	output := out.String()
	if !strings.Contains(output, "model --list") {
		t.Fatalf("expected a pointer to the model builtin, got:\n%s", output)
	}
	if strings.Contains(output, "Troubleshooting") || strings.Contains(output, "[Enter: retry]") {
		t.Fatalf("model errors should get neither install hints nor a retry prompt, got:\n%s", output)
	}
}
