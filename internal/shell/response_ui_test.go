package shell

import (
	"bytes"
	"testing"

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

	// Test different states
	ui.ShowState(AgentStateConnecting)
	if !bytes.Contains(buf.Bytes(), []byte("Connecting")) {
		t.Error("Should show Connecting state")
	}

	buf.Reset()
	ui.ShowState(AgentStateSending)
	if !bytes.Contains(buf.Bytes(), []byte("Sending")) {
		t.Error("Should show Sending state")
	}

	buf.Reset()
	ui.ShowState(AgentStateThinking)
	if !bytes.Contains(buf.Bytes(), []byte("thinking")) {
		t.Error("Should show Thinking state")
	}

	buf.Reset()
	ui.ShowState(AgentStateReceiving)
	if !bytes.Contains(buf.Bytes(), []byte("Receiving")) {
		t.Error("Should show Receiving state")
	}
}
