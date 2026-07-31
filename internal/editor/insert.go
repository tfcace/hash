// internal/editor/insert.go
package editor

import (
	"strings"
	"unicode"
)

// InsertMode is the default text entry mode.
type InsertMode struct{}

// NewInsertMode creates an insert mode handler.
func NewInsertMode() *InsertMode {
	return &InsertMode{}
}

// Name returns the mode name.
func (m *InsertMode) Name() string {
	return "insert"
}

// HandleKey processes a key in insert mode.
func (m *InsertMode) HandleKey(key Key, state *EditorState) ModeResult {
	// Handle bracketed paste
	if key.Special == KeyPaste {
		m.deleteSelection(state)
		insertPasteContent(state, key.PasteText)
		return ModeResult{Action: ActionPaste}
	}

	// Alt+Enter or Shift+Enter: insert newline (with optional shell continuation)
	if key.Special == KeyEnter && (key.Shift || key.Alt) {
		if state.LineContinuation {
			m.insertNewlineWithContinuation(state)
		} else {
			m.insertNewline(state)
		}
		return ModeResult{Action: ActionInsert}
	}

	// Universal bindings first (Ctrl+A, Ctrl+E, etc.)
	if key.Ctrl {
		return m.handleCtrl(key, state)
	}
	if key.Alt {
		return m.handleAlt(key, state)
	}

	// Special keys
	if result, handled := m.handleSpecialKey(key, state); handled {
		return result
	}

	// Printable character
	if key.Rune != 0 && unicode.IsPrint(key.Rune) {
		m.deleteSelection(state)
		m.insertChar(state, key.Rune)
		if key.Rune == ' ' {
			return ModeResult{Action: ActionInsert, Prefetch: true}
		}
		return ModeResult{Action: ActionInsert}
	}

	return ModeResult{}
}

// handleSpecialKey processes special keys (arrows, home, end, etc.).
func (m *InsertMode) handleSpecialKey(key Key, state *EditorState) (ModeResult, bool) {
	switch key.Special {
	case KeyEscape:
		state.Cursor.ClearSelection()
		return ModeResult{NewMode: NewNormalMode(), Action: ActionModeChange}, true
	case KeyEnter:
		return ModeResult{Submit: true}, true
	case KeyTab:
		return ModeResult{Complete: true}, true
	case KeyBackspace:
		if state.Cursor.HasSelection() {
			m.deleteSelection(state)
		} else {
			m.deleteBack(state)
		}
		return ModeResult{Action: ActionDelete}, true
	case KeyDelete:
		if state.Cursor.HasSelection() {
			m.deleteSelection(state)
		} else {
			m.deleteForward(state)
		}
		return ModeResult{Action: ActionDelete}, true
	case KeyLeft, KeyRight, KeyUp, KeyDown, KeyHome, KeyEnd:
		return m.handleArrowKey(key, state)
	}
	return ModeResult{}, false
}

// handleArrowKey processes arrow and navigation keys with Shift selection support.
func (m *InsertMode) handleArrowKey(key Key, state *EditorState) (ModeResult, bool) {
	// Update selection state
	m.updateSelectionForShift(key.Shift, state)

	// Up/Down move by visual (soft-wrapped) row; at the buffer's visual
	// boundaries they navigate history instead
	if key.Special == KeyUp || key.Special == KeyDown {
		moved := false
		if key.Special == KeyUp {
			moved = visualUp(state)
		} else {
			moved = visualDown(state)
		}
		if !moved {
			state.Cursor.ClearSelection()
			if key.Special == KeyUp {
				return ModeResult{HistoryPrev: true}, true
			}
			return ModeResult{HistoryNext: true}, true
		}
		return ModeResult{}, true
	}

	// Perform the movement
	switch key.Special {
	case KeyLeft:
		m.moveLeft(state)
	case KeyRight:
		m.moveRight(state)
	case KeyHome:
		state.Cursor.Pos.Col = 0
	case KeyEnd:
		state.Cursor.Pos.Col = len(state.Buffer.Line(state.Cursor.Pos.Row))
	}
	return ModeResult{}, true
}

// updateSelectionForShift starts or clears selection based on the Shift modifier.
func (m *InsertMode) updateSelectionForShift(shift bool, state *EditorState) {
	if shift {
		m.startOrExtendSelection(state)
	} else {
		state.Cursor.ClearSelection()
	}
}

func (m *InsertMode) handleCtrl(key Key, state *EditorState) ModeResult {
	switch key.Rune {
	case 'a': // Ctrl+A: line start
		state.Cursor.ClearSelection()
		state.Cursor.Pos.Col = 0
	case 'e': // Ctrl+E: line end
		state.Cursor.ClearSelection()
		state.Cursor.Pos.Col = len(state.Buffer.Line(state.Cursor.Pos.Row))
	case 'b': // Ctrl+B: back one char (emacs)
		state.Cursor.ClearSelection()
		m.moveLeft(state)
	case 'f': // Ctrl+F: forward one char (emacs)
		state.Cursor.ClearSelection()
		m.moveRight(state)
	case 'p': // Ctrl+P: context picker
		if state.AllowContextPicker {
			return ModeResult{ContextPicker: true}
		}
	case 'w': // Ctrl+W: delete whitespace-delimited word back (bash werase)
		if state.Cursor.HasSelection() {
			m.deleteSelection(state)
		} else {
			m.deleteTokenBack(state)
		}
		return ModeResult{Action: ActionDelete}
	case 'u': // Ctrl+U: delete to line start
		if state.Cursor.HasSelection() {
			m.deleteSelection(state)
		} else {
			m.deleteToLineStart(state)
		}
		return ModeResult{Action: ActionDelete}
	case 'k': // Ctrl+K: delete to line end
		if state.Cursor.HasSelection() {
			m.deleteSelection(state)
		} else {
			m.deleteToLineEnd(state)
		}
		return ModeResult{Action: ActionDelete}
	case 'r': // Ctrl+R: history search
		if state.AllowHistorySearch {
			return ModeResult{HistorySearch: true}
		}
	}
	return ModeResult{}
}

func (m *InsertMode) handleAlt(key Key, state *EditorState) ModeResult {
	switch key.Special {
	case KeyLeft: // Alt+Left: word back
		if key.Shift {
			m.startOrExtendSelection(state)
		} else {
			state.Cursor.ClearSelection()
		}
		m.moveWordBack(state)
	case KeyRight: // Alt+Right: word forward
		if key.Shift {
			m.startOrExtendSelection(state)
		} else {
			state.Cursor.ClearSelection()
		}
		m.moveWordForward(state)
	case KeyBackspace: // Alt+Backspace: delete whole token
		if state.Cursor.HasSelection() {
			m.deleteSelection(state)
		} else {
			m.deleteTokenBack(state)
		}
		return ModeResult{Action: ActionDelete}
	}
	switch key.Rune {
	case 'b': // Alt+B: word back
		state.Cursor.ClearSelection()
		m.moveWordBack(state)
	case 'f': // Alt+F: word forward
		state.Cursor.ClearSelection()
		m.moveWordForward(state)
	}
	return ModeResult{}
}

// deleteSelection deletes the selected text and moves the cursor to the start of the selection.
func (m *InsertMode) deleteSelection(state *EditorState) {
	if !state.Cursor.HasSelection() {
		return
	}
	start, end := state.Cursor.SelectionRange()
	state.Buffer.Delete(start, end)
	state.Cursor.MoveTo(start.Row, start.Col)
	state.Cursor.ClearSelection()
}

// startOrExtendSelection begins a selection if none exists.
func (m *InsertMode) startOrExtendSelection(state *EditorState) {
	if !state.Cursor.HasSelection() {
		state.Cursor.StartSelection()
	}
}

func (m *InsertMode) insertChar(state *EditorState, r rune) {
	row, col := state.Cursor.Pos.Row, state.Cursor.Pos.Col
	state.Buffer.Insert(row, col, string(r))
	if r == '\n' {
		state.Cursor.Pos.Row++
		state.Cursor.Pos.Col = 0
	} else {
		state.Cursor.Pos.Col += len(string(r))
	}
}

func (m *InsertMode) deleteBack(state *EditorState) {
	row, col := state.Cursor.Pos.Row, state.Cursor.Pos.Col
	if col > 0 {
		prevCol := previousRuneBoundary(state.Buffer.Line(row), col)
		state.Buffer.Delete(Position{row, prevCol}, Position{row, col})
		state.Cursor.Pos.Col = prevCol
	} else if row > 0 {
		// Join with previous line
		prevLen := len(state.Buffer.Line(row - 1))
		state.Buffer.Delete(Position{row - 1, prevLen}, Position{row, 0})
		state.Cursor.Pos.Row--
		state.Cursor.Pos.Col = prevLen
	}
}

func (m *InsertMode) deleteForward(state *EditorState) {
	row, col := state.Cursor.Pos.Row, state.Cursor.Pos.Col
	lineLen := len(state.Buffer.Line(row))
	if col < lineLen {
		nextCol := nextRuneBoundary(state.Buffer.Line(row), col)
		state.Buffer.Delete(Position{row, col}, Position{row, nextCol})
	} else if row < state.Buffer.LineCount()-1 {
		// Join with next line
		state.Buffer.Delete(Position{row, lineLen}, Position{row + 1, 0})
	}
}

func (m *InsertMode) moveLeft(state *EditorState) {
	if state.Cursor.Pos.Col > 0 {
		state.Cursor.Pos.Col = previousRuneBoundary(state.Buffer.Line(state.Cursor.Pos.Row), state.Cursor.Pos.Col)
	} else if state.Cursor.Pos.Row > 0 {
		state.Cursor.Pos.Row--
		state.Cursor.Pos.Col = len(state.Buffer.Line(state.Cursor.Pos.Row))
	}
}

func (m *InsertMode) moveRight(state *EditorState) {
	lineLen := len(state.Buffer.Line(state.Cursor.Pos.Row))
	if state.Cursor.Pos.Col < lineLen {
		state.Cursor.Pos.Col = nextRuneBoundary(state.Buffer.Line(state.Cursor.Pos.Row), state.Cursor.Pos.Col)
	} else if state.Cursor.Pos.Row < state.Buffer.LineCount()-1 {
		state.Cursor.Pos.Row++
		state.Cursor.Pos.Col = 0
	}
}

func (m *InsertMode) moveWordBack(state *EditorState) {
	line := state.Buffer.Line(state.Cursor.Pos.Row)
	state.Cursor.Pos.Col = prevWordStart(line, state.Cursor.Pos.Col)
}

func (m *InsertMode) moveTokenBack(state *EditorState) {
	line := state.Buffer.Line(state.Cursor.Pos.Row)
	col := state.Cursor.Pos.Col

	for col > 0 && (col > len(line) || line[col-1] == ' ') {
		col--
	}
	for col > 0 && col <= len(line) && line[col-1] != ' ' {
		col--
	}
	state.Cursor.Pos.Col = col
}

func (m *InsertMode) deleteTokenBack(state *EditorState) {
	startCol := state.Cursor.Pos.Col
	m.moveTokenBack(state)
	endCol := state.Cursor.Pos.Col
	row := state.Cursor.Pos.Row
	state.Buffer.Delete(Position{row, endCol}, Position{row, startCol})
}

func (m *InsertMode) moveWordForward(state *EditorState) {
	line := state.Buffer.Line(state.Cursor.Pos.Row)
	state.Cursor.Pos.Col = nextWordStart(line, state.Cursor.Pos.Col)
}

func (m *InsertMode) deleteToLineStart(state *EditorState) {
	row := state.Cursor.Pos.Row
	col := state.Cursor.Pos.Col
	state.Buffer.Delete(Position{row, 0}, Position{row, col})
	state.Cursor.Pos.Col = 0
}

func (m *InsertMode) deleteToLineEnd(state *EditorState) {
	row := state.Cursor.Pos.Row
	col := state.Cursor.Pos.Col
	lineLen := len(state.Buffer.Line(row))
	state.Buffer.Delete(Position{row, col}, Position{row, lineLen})
}

// insertNewlineWithContinuation inserts a newline with shell line continuation.
// Adds " \" before the newline if the line doesn't already end with "\".
func (m *InsertMode) insertNewlineWithContinuation(state *EditorState) {
	row, col := state.Cursor.Pos.Row, state.Cursor.Pos.Col
	line := state.Buffer.Line(row)

	// Check if we need to add continuation
	// Get the text before cursor on this line
	textBeforeCursor := line[:col]
	needsContinuation := !endsWithBackslash(textBeforeCursor)

	if needsContinuation {
		// Insert " \" before the newline
		state.Buffer.Insert(row, col, " \\\n")
		state.Cursor.Pos.Row++
		state.Cursor.Pos.Col = 0
	} else {
		// Already has backslash, just insert newline
		state.Buffer.Insert(row, col, "\n")
		state.Cursor.Pos.Row++
		state.Cursor.Pos.Col = 0
	}
}

// insertPasteContent inserts pasted text literally. Only line endings are
// normalized; rewriting the content (e.g. adding shell continuations) would
// corrupt quoted strings and merge pasted commands into one.
func insertPasteContent(state *EditorState, text string) {
	if text == "" {
		return
	}

	processed := normalizePastedText(text)

	row, col := state.Cursor.Pos.Row, state.Cursor.Pos.Col
	state.Buffer.Insert(row, col, processed)

	// Move cursor to end of inserted text
	for _, r := range processed {
		if r == '\n' {
			row++
			col = 0
		} else {
			col += len(string(r))
		}
	}
	state.Cursor.Pos.Row = row
	state.Cursor.Pos.Col = col
}

// normalizePastedText canonicalizes pasted line endings to \n.
func normalizePastedText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	return strings.ReplaceAll(text, "\r", "\n")
}

// insertNewline inserts a plain newline without continuation.
func (m *InsertMode) insertNewline(state *EditorState) {
	row, col := state.Cursor.Pos.Row, state.Cursor.Pos.Col
	state.Buffer.Insert(row, col, "\n")
	state.Cursor.Pos.Row++
	state.Cursor.Pos.Col = 0
}

// endsWithBackslash checks if a string ends with a backslash (ignoring trailing whitespace).
func endsWithBackslash(s string) bool {
	// Trim trailing whitespace
	trimmed := s
	for trimmed != "" && (trimmed[len(trimmed)-1] == ' ' || trimmed[len(trimmed)-1] == '\t') {
		trimmed = trimmed[:len(trimmed)-1]
	}
	return trimmed != "" && trimmed[len(trimmed)-1] == '\\'
}
