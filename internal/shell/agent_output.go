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

// ANSI escape codes for permission prompt rendering.
const (
	ansiReset     = "\033[0m"
	ansiBold      = "\033[1m"
	ansiClearLine = "\033[2K"
	ansiCursorUp  = "\033[1A"
)

// RenderPermissionPrompt displays the permission request UI.
// Automatically enters permission state and pauses streaming.
func (aoc *AgentOutputCoordinator) RenderPermissionPrompt(command, accentColor string) {
	wasStreaming := aoc.EnterPermission()

	aoc.mu.Lock()
	defer aoc.mu.Unlock()

	// If we were streaming, clear the current line first
	if wasStreaming {
		fmt.Fprint(aoc.out, "\r\033[K")
	}

	// Build accent color ANSI code
	accentCode := ""
	if accentColor != "" && len(accentColor) == 7 && accentColor[0] == '#' {
		var r, g, b int
		fmt.Sscanf(accentColor[1:], "%02x%02x%02x", &r, &g, &b)
		accentCode = fmt.Sprintf("\033[38;2;%d;%d;%dm", r, g, b)
	} else {
		accentCode = "\033[36m" // Fallback to cyan
	}

	// Render the prompt box with colored bar
	barStyle := accentCode + ansiBold
	lines := []string{
		"",
		barStyle + "│" + ansiReset + " " + ansiBold + "Agent wants to run:" + ansiReset,
		barStyle + "│" + ansiReset + " " + accentCode + ansiBold + command + ansiReset,
		barStyle + "│" + ansiReset,
		barStyle + "│" + ansiReset + " [y]allow  [n]deny  [a]always allow",
	}

	var sb strings.Builder
	for _, line := range lines {
		sb.WriteString("\r\n")
		sb.WriteString(ansiClearLine)
		sb.WriteString(line)
	}

	aoc.out.Write([]byte(sb.String()))
}

// ClearPermissionPrompt removes the permission prompt and resumes streaming.
func (aoc *AgentOutputCoordinator) ClearPermissionPrompt() {
	aoc.mu.Lock()

	// Move up 5 lines and clear each
	var sb strings.Builder
	for i := 0; i < 5; i++ {
		sb.WriteString(ansiCursorUp)
		sb.WriteString("\r")
		sb.WriteString(ansiClearLine)
	}
	aoc.out.Write([]byte(sb.String()))

	aoc.mu.Unlock()

	// Resume streaming (this handles flushing buffered content)
	aoc.ExitPermission()
}

// EnterConfirming transitions to confirming state (after streaming completes).
func (aoc *AgentOutputCoordinator) EnterConfirming() {
	aoc.mu.Lock()
	defer aoc.mu.Unlock()
	aoc.state = AgentOutputStateConfirming
}

// ShowHints displays confirmation keybindings.
// Only works in CONFIRMING state to prevent hint overlap.
func (aoc *AgentOutputCoordinator) ShowHints(ct ConfirmationType) {
	aoc.mu.Lock()
	defer aoc.mu.Unlock()

	if aoc.state != AgentOutputStateConfirming {
		return // Don't show hints unless in confirming state
	}

	var hint string
	switch ct {
	case ConfirmTypeCommand:
		hint = "[Enter: run] [Tab: edit] [Esc: cancel]"
	case ConfirmTypeExplanation:
		hint = "[Enter: ok] [Tab: copy] [Esc: cancel]"
	case ConfirmTypeError:
		hint = "[Enter: retry] [Esc: cancel]"
	}

	fmt.Fprintf(aoc.out, "  \033[90m%s\033[0m\n", hint)
}

// ExitConfirming returns to idle state.
func (aoc *AgentOutputCoordinator) ExitConfirming() {
	aoc.mu.Lock()
	defer aoc.mu.Unlock()
	aoc.state = AgentOutputStateIdle
}
