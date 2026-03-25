package shell

import (
	"fmt"
	"io"
	"os"
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
	mu             sync.Mutex
	out            io.Writer
	state          AgentOutputState
	streamBuffer   strings.Builder // Buffers text when permission is active
	wasStreaming    bool            // Track if we were streaming before permission
	pendingCommand string          // Command currently being prompted for permission
	accentColorFn  func() string   // Callback to get current accent color
}

// NewAgentOutputCoordinator creates a new agent output coordinator.
func NewAgentOutputCoordinator(out io.Writer) *AgentOutputCoordinator {
	return &AgentOutputCoordinator{
		out:   out,
		state: AgentOutputStateIdle,
	}
}

// SetAccentColorFunc sets a callback to get the current accent color.
// This allows the color to be updated dynamically (e.g., after starship extraction).
func (aoc *AgentOutputCoordinator) SetAccentColorFunc(fn func() string) {
	aoc.mu.Lock()
	defer aoc.mu.Unlock()
	aoc.accentColorFn = fn
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
	fmt.Fprint(aoc.out, "\r\x1b[K")
}

// ANSI escape codes for permission prompt rendering.
const (
	ansiReset     = "\x1b[0m"
	ansiBold      = "\x1b[1m"
	ansiClearLine = "\x1b[2K"
	ansiCursorUp  = "\x1b[1A"
	permissionPad = "  "
)

// RenderPermissionPrompt displays the permission request UI.
// Automatically enters permission state and pauses streaming.
// toolName is optional -- if non-empty, it's shown as context (e.g., "Bash", "Write").
func (aoc *AgentOutputCoordinator) RenderPermissionPrompt(command, toolName, accentColor string) {
	aoc.EnterPermission()

	aoc.mu.Lock()
	defer aoc.mu.Unlock()

	// Store command for feedback when cleared
	aoc.pendingCommand = command

	// Use callback for accent color if available, otherwise use passed-in color
	color := accentColor
	if aoc.accentColorFn != nil {
		if c := aoc.accentColorFn(); c != "" {
			color = c
		}
	}

	// Build accent color ANSI code
	accentCode := "\x1b[36m" // Default to cyan
	if color != "" && len(color) == 7 && color[0] == '#' {
		var r, g, b int
		if _, err := fmt.Sscanf(color[1:], "%02x%02x%02x", &r, &g, &b); err == nil {
			accentCode = fmt.Sprintf("\x1b[38;2;%d;%d;%dm", r, g, b)
		}
	}

	// Build the header line with optional tool name context
	header := ansiBold + "Agent wants to run:" + ansiReset
	if toolName != "" {
		header = ansiBold + "Agent wants to run " + ansiReset + "\x1b[90m(" + toolName + ")\x1b[0m" + ansiReset + ":"
	}

	// Render the prompt box with colored bar
	barStyle := accentCode + ansiBold
	lines := []string{
		"",
		barStyle + "│" + ansiReset + " " + permissionPad + header,
		barStyle + "│" + ansiReset + " " + permissionPad + accentCode + ansiBold + command + ansiReset,
		barStyle + "│" + ansiReset + " " + permissionPad,
		barStyle + "│" + ansiReset + " " + permissionPad + "[y]allow  [n]deny  [a]always allow",
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
// Shows feedback with the command that was allowed/denied.
func (aoc *AgentOutputCoordinator) ClearPermissionPrompt(allowed bool) {
	aoc.mu.Lock()

	// Truncate command if too long
	cmd := aoc.pendingCommand
	if len(cmd) > 60 {
		cmd = cmd[:57] + "..."
	}

	// First clear the current line (line 5 - keybindings), then move up and clear the rest
	var sb strings.Builder
	sb.WriteString("\r")
	sb.WriteString(ansiClearLine)
	for i := 0; i < 4; i++ {
		sb.WriteString(ansiCursorUp)
		sb.WriteString("\r")
		sb.WriteString(ansiClearLine)
	}
	if allowed {
		sb.WriteString(fmt.Sprintf("%s\x1b[32m✓\x1b[0m \x1b[90m%s\x1b[0m\n", permissionPad, cmd))
	} else {
		sb.WriteString(fmt.Sprintf("%s\x1b[31m✗\x1b[0m \x1b[90m%s\x1b[0m\n", permissionPad, cmd))
	}
	aoc.out.Write([]byte(sb.String()))

	aoc.pendingCommand = "" // Clear for next prompt

	// Flush output to ensure clear sequences are sent immediately
	aoc.flushLocked()

	// Resume streaming inline (instead of releasing lock and calling ExitPermission,
	// which would create a TOCTOU race on permission state)
	if aoc.wasStreaming {
		aoc.state = AgentOutputStateStreaming
		if aoc.streamBuffer.Len() > 0 {
			fmt.Fprint(aoc.out, aoc.streamBuffer.String())
			aoc.streamBuffer.Reset()
		}
	} else {
		aoc.state = AgentOutputStateIdle
	}
	aoc.wasStreaming = false

	aoc.mu.Unlock()
}

// flushLocked flushes the output if it supports syncing.
// Must be called with mu held.
func (aoc *AgentOutputCoordinator) flushLocked() {
	if f, ok := aoc.out.(*os.File); ok {
		f.Sync()
	}
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

	fmt.Fprintf(aoc.out, "  \x1b[90m%s\x1b[0m\n", hint)
}

// ExitConfirming returns to idle state.
func (aoc *AgentOutputCoordinator) ExitConfirming() {
	aoc.mu.Lock()
	defer aoc.mu.Unlock()
	aoc.state = AgentOutputStateIdle
}

// Cancel aborts current operation and returns to idle state.
// Clears any buffered content without writing it.
// If a permission prompt was visible, clears all 5 lines.
func (aoc *AgentOutputCoordinator) Cancel() {
	aoc.mu.Lock()
	defer aoc.mu.Unlock()

	wasPermission := aoc.state == AgentOutputStatePermission

	aoc.state = AgentOutputStateIdle
	aoc.streamBuffer.Reset()
	aoc.wasStreaming = false

	if wasPermission {
		// Clear all 5 lines of the permission prompt
		var sb strings.Builder
		for i := 0; i < 5; i++ {
			sb.WriteString(ansiCursorUp)
			sb.WriteString("\r")
			sb.WriteString(ansiClearLine)
		}
		aoc.out.Write([]byte(sb.String()))
	} else {
		// Just clear current line to clean up any partial output
		fmt.Fprint(aoc.out, "\r\x1b[K")
	}

	aoc.flushLocked()
}
