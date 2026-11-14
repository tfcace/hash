// internal/editor/mode.go
package editor

import "strings"

// Action describes what a key did, for undo grouping. Typing (Insert) and
// deleting (Delete) group with neighboring keys of the same kind; any other
// change to the text is an undo step of its own.
type Action int

const (
	ActionNone   Action = iota
	ActionInsert        // typed text
	ActionDelete        // deleted text
	ActionPaste         // pasted text
	ActionUndo          // undo/redo moved through history; never recorded itself
)

// ModeCommand is the editor-level operation a key asks for, beyond the text
// edit the mode already made. A key asks for at most one.
type ModeCommand int

const (
	CommandNone          ModeCommand = iota
	CommandSubmit                    // run the line
	CommandComplete                  // Tab completion
	CommandPrefetch                  // warm completion caches after a space
	CommandHistoryPrev               // previous history entry
	CommandHistoryNext               // next history entry
	CommandHistorySearch             // Ctrl+R history search
	CommandContextPicker             // Ctrl+P context picker
	CommandYank                      // copy the selection to the clipboard
	CommandPaste                     // paste the clipboard after the cursor (p)
	CommandPasteBefore               // paste the clipboard before the cursor (P)
)

// ModeResult is returned by mode key handlers.
type ModeResult struct {
	NewMode Mode   // Nil means stay in current mode
	Action  Action // What the key did to the text, for undo grouping
	Command ModeCommand
}

// Mode handles key input for a specific editing mode.
type Mode interface {
	Name() string
	HandleKey(key Key, state *EditorState) ModeResult
}

// altKeys maps the Alt keys a mode binds, by the key without modifiers, to
// what they do. An Alt key a mode does not bind does nothing there (an Alt
// chord a terminal sent as ESC and the key included: it is no Esc).
type altKeys map[Key]func(key Key, state *EditorState) ModeResult

// altBase is key without its modifiers: what altKeys are keyed by.
func altBase(key Key) Key {
	return Key{Rune: key.Rune, Special: key.Special}
}

// handle runs key's binding, if it has one.
func (a altKeys) handle(key Key, state *EditorState) ModeResult {
	if do, ok := a[altBase(key)]; ok {
		return do(key, state)
	}
	return ModeResult{}
}

// EditorState holds the current editor state.
type EditorState struct {
	Buffer    *Buffer
	Cursor    *Cursor
	UndoStack *UndoStack
	// LineContinuation controls shell-style "\" continuation on a typed
	// newline. Pasted text is inserted literally.
	LineContinuation bool
	// Incomplete reports whether text is a shell line the shell reads as
	// unfinished (Config.Incomplete); nil: no text is.
	Incomplete func(text string) bool
	// AllowHistorySearch controls Ctrl+R history search.
	AllowHistorySearch bool
	// AllowContextPicker controls Ctrl+P context picker.
	AllowContextPicker bool
	// Layout provides wrap geometry for visual-row vertical movement.
	// Nil falls back to logical-line movement.
	Layout LineLayout

	// goal is the screen column vertical movement aims for, or nil when
	// none is remembered.
	goal *verticalGoal
}

// verticalGoal is a remembered goal column and the cursor position it was
// last applied to: any other movement makes it stale automatically.
type verticalGoal struct {
	col int
	at  Position
}

// NewEditorState creates a new editor state.
func NewEditorState() *EditorState {
	return &EditorState{
		Buffer:             NewBuffer(),
		Cursor:             NewCursor(),
		UndoStack:          NewUndoStack(),
		LineContinuation:   true,
		AllowHistorySearch: true,
		AllowContextPicker: true,
	}
}

// incomplete reports whether text is a shell line the shell reads as
// unfinished (Incomplete).
func (s *EditorState) incomplete(text string) bool {
	return s.Incomplete != nil && s.Incomplete(text)
}

// textBeforeCursor is the buffer's text up to the cursor.
func (s *EditorState) textBeforeCursor() string {
	var b strings.Builder
	for row := range s.Cursor.Pos.Row {
		b.WriteString(s.Buffer.Line(row))
		b.WriteByte('\n')
	}
	line := s.Buffer.Line(s.Cursor.Pos.Row)
	b.WriteString(line[:min(max(s.Cursor.Pos.Col, 0), len(line))])
	return b.String()
}

// setBuffer replaces the whole buffer and puts the cursor at pos. Any
// selection and vertical goal column are dropped: they describe positions in
// the old text, not the new.
func (s *EditorState) setBuffer(buf *Buffer, pos Position) {
	s.Buffer = buf
	s.Cursor = &Cursor{Pos: pos}
	s.Cursor.Clamp(buf)
	s.goal = nil
}
