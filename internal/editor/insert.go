// internal/editor/insert.go
package editor

import "unicode"

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
	// Alt+Enter or Shift+Enter: insert newline (must check before general Alt handling)
	// Some terminals send ESC+Enter for Shift+Enter
	if key.Special == KeyEnter && (key.Shift || key.Alt) {
		m.insertChar(state, '\n')
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
	switch key.Special {
	case KeyEscape:
		return ModeResult{NewMode: NewNormalMode(), Action: ActionModeChange}

	case KeyEnter:
		// Enter: submit command
		return ModeResult{Submit: true}

	case KeyTab:
		return ModeResult{Complete: true}

	case KeyBackspace:
		m.deleteBack(state)
		return ModeResult{Action: ActionDelete}

	case KeyDelete:
		m.deleteForward(state)
		return ModeResult{Action: ActionDelete}

	case KeyLeft:
		m.moveLeft(state)
		return ModeResult{}

	case KeyRight:
		m.moveRight(state)
		return ModeResult{}

	case KeyUp:
		if state.Cursor.Pos.Row == 0 {
			return ModeResult{HistoryPrev: true}
		}
		m.moveUp(state)
		return ModeResult{}

	case KeyDown:
		if state.Cursor.Pos.Row == state.Buffer.LineCount()-1 {
			return ModeResult{HistoryNext: true}
		}
		m.moveDown(state)
		return ModeResult{}

	case KeyHome:
		state.Cursor.Pos.Col = 0
		return ModeResult{}

	case KeyEnd:
		state.Cursor.Pos.Col = len(state.Buffer.Line(state.Cursor.Pos.Row))
		return ModeResult{}
	}

	// Printable character
	if key.Rune != 0 && unicode.IsPrint(key.Rune) {
		m.insertChar(state, key.Rune)
		// Trigger prefetch when space is typed (for Cobra completions)
		if key.Rune == ' ' {
			return ModeResult{Action: ActionInsert, Prefetch: true}
		}
		return ModeResult{Action: ActionInsert}
	}

	return ModeResult{}
}

func (m *InsertMode) handleCtrl(key Key, state *EditorState) ModeResult {
	switch key.Rune {
	case 'a': // Ctrl+A: line start
		state.Cursor.Pos.Col = 0
	case 'e': // Ctrl+E: line end
		state.Cursor.Pos.Col = len(state.Buffer.Line(state.Cursor.Pos.Row))
	case 'b': // Ctrl+B: back one char (emacs)
		m.moveLeft(state)
	case 'f': // Ctrl+F: forward one char (emacs)
		m.moveRight(state)
	case 'p': // Ctrl+P: context picker
		return ModeResult{ContextPicker: true}
	case 'w': // Ctrl+W: delete word back
		m.deleteWordBack(state)
		return ModeResult{Action: ActionDelete}
	case 'u': // Ctrl+U: delete to line start
		m.deleteToLineStart(state)
		return ModeResult{Action: ActionDelete}
	case 'k': // Ctrl+K: delete to line end
		m.deleteToLineEnd(state)
		return ModeResult{Action: ActionDelete}
	case 'r': // Ctrl+R: history search
		return ModeResult{HistorySearch: true}
	}
	return ModeResult{}
}

func (m *InsertMode) handleAlt(key Key, state *EditorState) ModeResult {
	switch key.Special {
	case KeyLeft: // Alt+Left: word back
		m.moveWordBack(state)
	case KeyRight: // Alt+Right: word forward
		m.moveWordForward(state)
	}
	switch key.Rune {
	case 'b': // Alt+B: word back
		m.moveWordBack(state)
	case 'f': // Alt+F: word forward
		m.moveWordForward(state)
	}
	return ModeResult{}
}

func (m *InsertMode) insertChar(state *EditorState, r rune) {
	row, col := state.Cursor.Pos.Row, state.Cursor.Pos.Col
	state.Buffer.Insert(row, col, string(r))
	if r == '\n' {
		state.Cursor.Pos.Row++
		state.Cursor.Pos.Col = 0
	} else {
		state.Cursor.Pos.Col++
	}
}

func (m *InsertMode) deleteBack(state *EditorState) {
	row, col := state.Cursor.Pos.Row, state.Cursor.Pos.Col
	if col > 0 {
		state.Buffer.Delete(Position{row, col - 1}, Position{row, col})
		state.Cursor.Pos.Col--
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
		state.Buffer.Delete(Position{row, col}, Position{row, col + 1})
	} else if row < state.Buffer.LineCount()-1 {
		// Join with next line
		state.Buffer.Delete(Position{row, lineLen}, Position{row + 1, 0})
	}
}

func (m *InsertMode) moveLeft(state *EditorState) {
	if state.Cursor.Pos.Col > 0 {
		state.Cursor.Pos.Col--
	} else if state.Cursor.Pos.Row > 0 {
		state.Cursor.Pos.Row--
		state.Cursor.Pos.Col = len(state.Buffer.Line(state.Cursor.Pos.Row))
	}
}

func (m *InsertMode) moveRight(state *EditorState) {
	lineLen := len(state.Buffer.Line(state.Cursor.Pos.Row))
	if state.Cursor.Pos.Col < lineLen {
		state.Cursor.Pos.Col++
	} else if state.Cursor.Pos.Row < state.Buffer.LineCount()-1 {
		state.Cursor.Pos.Row++
		state.Cursor.Pos.Col = 0
	}
}

func (m *InsertMode) moveUp(state *EditorState) {
	if state.Cursor.Pos.Row > 0 {
		state.Cursor.Pos.Row--
		lineLen := len(state.Buffer.Line(state.Cursor.Pos.Row))
		if state.Cursor.Pos.Col > lineLen {
			state.Cursor.Pos.Col = lineLen
		}
	}
}

func (m *InsertMode) moveDown(state *EditorState) {
	if state.Cursor.Pos.Row < state.Buffer.LineCount()-1 {
		state.Cursor.Pos.Row++
		lineLen := len(state.Buffer.Line(state.Cursor.Pos.Row))
		if state.Cursor.Pos.Col > lineLen {
			state.Cursor.Pos.Col = lineLen
		}
	}
}

func (m *InsertMode) moveWordBack(state *EditorState) {
	line := state.Buffer.Line(state.Cursor.Pos.Row)
	col := state.Cursor.Pos.Col

	// Skip spaces
	for col > 0 && (col > len(line) || line[col-1] == ' ') {
		col--
	}
	// Skip word
	for col > 0 && col <= len(line) && line[col-1] != ' ' {
		col--
	}
	state.Cursor.Pos.Col = col
}

func (m *InsertMode) moveWordForward(state *EditorState) {
	line := state.Buffer.Line(state.Cursor.Pos.Row)
	col := state.Cursor.Pos.Col

	// Skip word
	for col < len(line) && line[col] != ' ' {
		col++
	}
	// Skip spaces
	for col < len(line) && line[col] == ' ' {
		col++
	}
	state.Cursor.Pos.Col = col
}

func (m *InsertMode) deleteWordBack(state *EditorState) {
	startCol := state.Cursor.Pos.Col
	m.moveWordBack(state)
	endCol := state.Cursor.Pos.Col
	row := state.Cursor.Pos.Row
	state.Buffer.Delete(Position{row, endCol}, Position{row, startCol})
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
