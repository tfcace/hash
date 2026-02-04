package shell

import (
	"strings"
	"sync"
)

// ConversationSubState represents the sub-state within an active conversation.
type ConversationSubState int

const (
	ConversationStreaming     ConversationSubState = iota // Agent is responding
	ConversationPermission                                 // Tool permission prompt active
	ConversationAwaitingInput                              // User's turn to reply
	ConversationExecutingShell                             // Running !cmd
)

func (s ConversationSubState) String() string {
	switch s {
	case ConversationStreaming:
		return "streaming"
	case ConversationPermission:
		return "permission"
	case ConversationAwaitingInput:
		return "awaiting_input"
	case ConversationExecutingShell:
		return "executing_shell"
	default:
		return "unknown"
	}
}

// ConversationState manages the conversation mode state machine.
type ConversationState struct {
	mu       sync.RWMutex
	active   bool
	subState ConversationSubState
}

// NewConversationState creates a new conversation state (initially inactive).
func NewConversationState() *ConversationState {
	return &ConversationState{
		active:   false,
		subState: ConversationStreaming,
	}
}

// IsActive returns whether conversation mode is active.
func (cs *ConversationState) IsActive() bool {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	return cs.active
}

// SubState returns the current sub-state.
func (cs *ConversationState) SubState() ConversationSubState {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	return cs.subState
}

// Activate enters conversation mode, starting in streaming state.
func (cs *ConversationState) Activate() {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.active = true
	cs.subState = ConversationStreaming
}

// Deactivate exits conversation mode.
func (cs *ConversationState) Deactivate() {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.active = false
	cs.subState = ConversationStreaming
}

// SetSubState transitions to a new sub-state.
func (cs *ConversationState) SetSubState(state ConversationSubState) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.subState = state
}

// IsShellEscape checks if the input is a shell escape command (starts with !).
func (cs *ConversationState) IsShellEscape(input string) bool {
	return len(input) > 1 && input[0] == '!'
}

// ExtractShellCommand extracts the command from a shell escape (removes leading !).
func (cs *ConversationState) ExtractShellCommand(input string) string {
	if cs.IsShellEscape(input) {
		return input[1:]
	}
	return input
}

// IsExitCommand checks if the input is an exit command.
// Accepts: /done, /exit, /quit
func (cs *ConversationState) IsExitCommand(input string) bool {
	trimmed := strings.TrimSpace(input)
	return trimmed == "/done" || trimmed == "/exit" || trimmed == "/quit"
}
