package shell

import (
	"bytes"
	"testing"
	"time"

	"github.com/tfcace/hash/internal/agent"
)

func TestResponseUI_FormatCommand(t *testing.T) {
	var buf bytes.Buffer
	ui := NewResponseUI(&buf)

	resp := agent.Response{
		Type:    agent.ResponseTypeCommand,
		Command: "find . -size +100M",
	}

	ui.ShowResponse(resp)
	output := buf.String()

	// Should contain the command
	if !bytes.Contains(buf.Bytes(), []byte("find . -size +100M")) {
		t.Errorf("Output missing command, got: %s", output)
	}
}

func TestResponseUI_FormatExplanation(t *testing.T) {
	var buf bytes.Buffer
	ui := NewResponseUI(&buf)

	resp := agent.Response{
		Type:        agent.ResponseTypeExplanation,
		Explanation: "This finds large files",
	}

	ui.ShowResponse(resp)
	output := buf.String()

	if !bytes.Contains(buf.Bytes(), []byte("This finds large files")) {
		t.Errorf("Output missing explanation, got: %s", output)
	}
}

func TestResponseUI_FormatError(t *testing.T) {
	var buf bytes.Buffer
	ui := NewResponseUI(&buf)

	resp := agent.Response{
		Type:  agent.ResponseTypeError,
		Error: "Connection failed",
	}

	ui.ShowResponse(resp)
	output := buf.String()

	if !bytes.Contains(buf.Bytes(), []byte("Connection failed")) {
		t.Errorf("Output missing error, got: %s", output)
	}
}

func TestResponseUI_LoadingStates(t *testing.T) {
	var buf bytes.Buffer
	ui := NewResponseUI(&buf)

	// Helper to wait for spinner output and stop it
	waitForSpinner := func() {
		time.Sleep(100 * time.Millisecond) // Wait for at least one spinner frame
		ui.StopSpinner()
	}

	// Test different states
	ui.ShowState(AgentStateConnecting)
	waitForSpinner()
	if !bytes.Contains(buf.Bytes(), []byte("agent · connecting")) {
		t.Errorf("Should show Connecting state, got: %s", buf.String())
	}

	buf.Reset()
	ui.ShowState(AgentStateSending)
	waitForSpinner()
	if !bytes.Contains(buf.Bytes(), []byte("agent · sending context")) {
		t.Errorf("Should show Sending state, got: %s", buf.String())
	}

	buf.Reset()
	ui.ShowState(AgentStateThinking)
	waitForSpinner()
	if !bytes.Contains(buf.Bytes(), []byte("thinking")) {
		t.Errorf("Should show Thinking state, got: %s", buf.String())
	}

	buf.Reset()
	ui.ShowState(AgentStateReceiving)
	waitForSpinner()
	if !bytes.Contains(buf.Bytes(), []byte("agent · receiving")) {
		t.Errorf("Should show Receiving state, got: %s", buf.String())
	}
}

func TestAgentStateStringUsesScopedConversationStyle(t *testing.T) {
	got := AgentStateThinking.String()
	if !bytes.Contains([]byte(got), []byte("agent ·")) {
		t.Fatalf("thinking state should include scoped agent label, got %q", got)
	}
	if !bytes.Contains([]byte(got), []byte("thinking")) {
		t.Fatalf("thinking state should use lower-case status copy, got %q", got)
	}
}
