package shell

import (
	"bytes"
	"testing"
)

func TestAgentOutputCoordinator_InitialState(t *testing.T) {
	var buf bytes.Buffer
	aoc := NewAgentOutputCoordinator(&buf)

	if aoc.State() != AgentOutputStateIdle {
		t.Errorf("expected initial state IDLE, got %v", aoc.State())
	}
}

func TestAgentOutputCoordinator_StreamingState(t *testing.T) {
	var buf bytes.Buffer
	aoc := NewAgentOutputCoordinator(&buf)

	aoc.StartStreaming()
	if aoc.State() != AgentOutputStateStreaming {
		t.Errorf("expected STREAMING state, got %v", aoc.State())
	}

	aoc.WriteStream("hello ")
	aoc.WriteStream("world")
	aoc.EndStreaming()

	if aoc.State() != AgentOutputStateIdle {
		t.Errorf("expected IDLE state after end, got %v", aoc.State())
	}

	output := buf.String()
	if output != "hello world" {
		t.Errorf("expected 'hello world', got %q", output)
	}
}

func TestAgentOutputCoordinator_PermissionPausesStreaming(t *testing.T) {
	var buf bytes.Buffer
	aoc := NewAgentOutputCoordinator(&buf)

	aoc.StartStreaming()
	aoc.WriteStream("before ")

	// Permission request arrives
	aoc.EnterPermission()
	if aoc.State() != AgentOutputStatePermission {
		t.Errorf("expected PERMISSION state, got %v", aoc.State())
	}

	// Text arriving during permission should be buffered
	aoc.WriteStream("during ")

	// Permission resolved
	aoc.ExitPermission()
	if aoc.State() != AgentOutputStateStreaming {
		t.Errorf("expected STREAMING state after permission, got %v", aoc.State())
	}

	aoc.WriteStream("after")
	aoc.EndStreaming()

	output := buf.String()
	// Should see: "before " (written) + clear line + "during after" (buffered + resumed)
	// The exact output depends on ANSI codes, but text should be preserved
	if !bytes.Contains([]byte(output), []byte("before")) {
		t.Errorf("missing 'before' in output: %q", output)
	}
	if !bytes.Contains([]byte(output), []byte("during")) {
		t.Errorf("missing 'during' in output: %q", output)
	}
	if !bytes.Contains([]byte(output), []byte("after")) {
		t.Errorf("missing 'after' in output: %q", output)
	}
}
