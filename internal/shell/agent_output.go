package shell

import (
	"fmt"
	"io"
	"strings"
	"sync"
)

// AgentOutputState represents the current mode of the agent output coordinator.
type AgentOutputState int

const (
	AgentOutputStateIdle       AgentOutputState = iota // No active agent output
	AgentOutputStateStreaming                          // Streaming agent response
	AgentOutputStatePermission                         // Permission prompt active (streaming paused)
	AgentOutputStateConfirming                         // Waiting for confirmation input
)

func (s AgentOutputState) String() string {
	switch s {
	case AgentOutputStateIdle:
		return "IDLE"
	case AgentOutputStateStreaming:
		return "STREAMING"
	case AgentOutputStatePermission:
		return "PERMISSION"
	case AgentOutputStateConfirming:
		return "CONFIRMING"
	default:
		return "UNKNOWN"
	}
}

// AgentOutputCoordinator serializes all agent-related terminal output.
// It ensures permission prompts don't interleave with streaming text.
//
// This coordinator is specifically for agent interactions:
// - ?? streaming responses
// - Tool permission prompts
// - Response confirmation hints
//
// Other TUI components (ContextPicker, History search) are not managed here.
type AgentOutputCoordinator struct {
	mu           sync.Mutex
	out          io.Writer
	state        AgentOutputState
	streamBuffer strings.Builder // Buffers text when permission is active
	wasStreaming bool            // Track if we were streaming before permission
}

// NewAgentOutputCoordinator creates a new agent output coordinator.
func NewAgentOutputCoordinator(out io.Writer) *AgentOutputCoordinator {
	return &AgentOutputCoordinator{
		out:   out,
		state: AgentOutputStateIdle,
	}
}

// State returns the current output state.
func (aoc *AgentOutputCoordinator) State() AgentOutputState {
	aoc.mu.Lock()
	defer aoc.mu.Unlock()
	return aoc.state
}

// StartStreaming transitions to streaming state.
func (aoc *AgentOutputCoordinator) StartStreaming() {
	aoc.mu.Lock()
	defer aoc.mu.Unlock()
	aoc.state = AgentOutputStateStreaming
	aoc.streamBuffer.Reset()
}

// WriteStream writes streaming text, or buffers it if permission is active.
func (aoc *AgentOutputCoordinator) WriteStream(text string) {
	aoc.mu.Lock()
	defer aoc.mu.Unlock()

	switch aoc.state {
	case AgentOutputStateStreaming:
		// Write directly to output
		fmt.Fprint(aoc.out, text)
	case AgentOutputStatePermission:
		// Buffer for later
		aoc.streamBuffer.WriteString(text)
	default:
		// Ignore writes in other states (shouldn't happen, but be safe)
	}
}

// EndStreaming transitions back to idle state.
func (aoc *AgentOutputCoordinator) EndStreaming() {
	aoc.mu.Lock()
	defer aoc.mu.Unlock()
	aoc.state = AgentOutputStateIdle
	aoc.streamBuffer.Reset()
}

// EnterPermission pauses streaming and prepares for permission prompt.
// Returns true if streaming was active (caller should clear the line).
func (aoc *AgentOutputCoordinator) EnterPermission() bool {
	aoc.mu.Lock()
	defer aoc.mu.Unlock()

	aoc.wasStreaming = aoc.state == AgentOutputStateStreaming
	aoc.state = AgentOutputStatePermission
	return aoc.wasStreaming
}

// ExitPermission exits permission mode and resumes streaming if it was active.
// Flushes any buffered text.
func (aoc *AgentOutputCoordinator) ExitPermission() {
	aoc.mu.Lock()
	defer aoc.mu.Unlock()

	if aoc.wasStreaming {
		aoc.state = AgentOutputStateStreaming
		// Flush buffered content
		if aoc.streamBuffer.Len() > 0 {
			fmt.Fprint(aoc.out, aoc.streamBuffer.String())
			aoc.streamBuffer.Reset()
		}
	} else {
		aoc.state = AgentOutputStateIdle
	}
	aoc.wasStreaming = false
}

// Write implements io.Writer for direct output (permission prompts, hints).
// This bypasses buffering - caller is responsible for state management.
func (aoc *AgentOutputCoordinator) Write(p []byte) (n int, err error) {
	aoc.mu.Lock()
	defer aoc.mu.Unlock()
	return aoc.out.Write(p)
}

// ClearLine outputs ANSI escape to clear current line.
func (aoc *AgentOutputCoordinator) ClearLine() {
	aoc.mu.Lock()
	defer aoc.mu.Unlock()
	fmt.Fprint(aoc.out, "\r\033[K")
}
