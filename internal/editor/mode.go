// internal/editor/mode.go
package editor

// Action describes what happened for undo grouping.
type Action int

const (
	ActionNone Action = iota
	ActionInsert
	ActionDelete
	ActionPaste
	ActionModeChange
)

// ModeResult is returned by mode key handlers.
type ModeResult struct {
	NewMode       Mode   // Nil means stay in current mode
	Action        Action
	Submit        bool // True = user wants to execute
	Complete      bool // True = trigger completion
	HistoryPrev   bool // True = navigate to previous history
	HistoryNext   bool // True = navigate to next history
	HistorySearch bool // True = launch history search (Ctrl+R)
	Yank          bool // True = yank selection to clipboard
	Paste         bool // True = paste from clipboard
	PasteBefore   bool // True = paste before cursor (P)
}

// Mode handles key input for a specific editing mode.
type Mode interface {
	Name() string
	HandleKey(key Key, state *EditorState) ModeResult
}

// EditorState holds the current editor state.
type EditorState struct {
	Buffer    *Buffer
	Cursor    *Cursor
	UndoStack *UndoStack
}

// NewEditorState creates a new editor state.
func NewEditorState() *EditorState {
	return &EditorState{
		Buffer:    NewBuffer(),
		Cursor:    NewCursor(),
		UndoStack: NewUndoStack(),
	}
}
