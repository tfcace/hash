// internal/editor/normal.go
package editor

// NormalMode handles Helix-style normal mode keybindings.
type NormalMode struct{}

// NewNormalMode creates a normal mode handler.
func NewNormalMode() *NormalMode {
	return &NormalMode{}
}

// Name returns the mode name.
func (m *NormalMode) Name() string {
	return "normal"
}

// HandleKey processes a key in normal mode.
func (m *NormalMode) HandleKey(key Key, state *EditorState) ModeResult {
	// Handle bracketed paste: insert literally, same as insert mode
	if key.Special == KeyPaste {
		if state.Cursor.HasSelection() {
			m.deleteSelection(state)
		}
		insertPasteContent(state, key.PasteText)
		return ModeResult{Action: ActionPaste}
	}

	// Universal bindings (Ctrl+A, Ctrl+E, etc.)
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

	// Normal mode commands by category
	if result, handled := m.handleMovement(key, state); handled {
		return result
	}
	if result, handled := m.handleInsertMode(key, state); handled {
		return result
	}
	if result, handled := m.handleSelection(key, state); handled {
		return result
	}
	if result, handled := m.handleEditing(key, state); handled {
		return result
	}
	if result, handled := m.handleUndoRedo(key, state); handled {
		return result
	}

	return ModeResult{}
}

// handleSpecialKey handles arrow keys and special keys.
func (m *NormalMode) handleSpecialKey(key Key, state *EditorState) (ModeResult, bool) {
	switch key.Special {
	case KeyEnter:
		return ModeResult{Submit: true}, true
	case KeyEscape:
		state.Cursor.ClearSelection()
		return ModeResult{}, true
	case KeyUp:
		if state.Cursor.Pos.Row == 0 {
			return ModeResult{HistoryPrev: true}, true
		}
		m.moveUp(state)
		return ModeResult{}, true
	case KeyDown:
		if state.Cursor.Pos.Row == state.Buffer.LineCount()-1 {
			return ModeResult{HistoryNext: true}, true
		}
		m.moveDown(state)
		return ModeResult{}, true
	case KeyLeft:
		m.moveLeft(state)
		return ModeResult{}, true
	case KeyRight:
		m.moveRight(state)
		return ModeResult{}, true
	}
	return ModeResult{}, false
}

// handleMovement handles hjkl and word movement keys.
func (m *NormalMode) handleMovement(key Key, state *EditorState) (ModeResult, bool) {
	switch key.Rune {
	case 'h':
		m.moveLeft(state)
		return ModeResult{}, true
	case 'l':
		m.moveRight(state)
		return ModeResult{}, true
	case 'j':
		if state.Cursor.Pos.Row == state.Buffer.LineCount()-1 {
			return ModeResult{HistoryNext: true}, true
		}
		m.moveDown(state)
		return ModeResult{}, true
	case 'k':
		if state.Cursor.Pos.Row == 0 {
			return ModeResult{HistoryPrev: true}, true
		}
		m.moveUp(state)
		return ModeResult{}, true
	case 'w':
		m.moveWordForward(state)
		return ModeResult{}, true
	case 'b':
		m.moveWordBack(state)
		return ModeResult{}, true
	case 'e':
		m.moveWordEnd(state)
		return ModeResult{}, true
	case '0':
		state.Cursor.Pos.Col = 0
		return ModeResult{}, true
	case '$':
		state.Cursor.Pos.Col = len(state.Buffer.Line(state.Cursor.Pos.Row))
		return ModeResult{}, true
	}
	return ModeResult{}, false
}

// handleInsertMode handles keys that enter insert mode.
func (m *NormalMode) handleInsertMode(key Key, state *EditorState) (ModeResult, bool) {
	switch key.Rune {
	case 'i':
		return ModeResult{NewMode: NewInsertMode(), Action: ActionModeChange}, true
	case 'a':
		m.moveRight(state)
		return ModeResult{NewMode: NewInsertMode(), Action: ActionModeChange}, true
	case 'I':
		state.Cursor.Pos.Col = 0
		return ModeResult{NewMode: NewInsertMode(), Action: ActionModeChange}, true
	case 'A':
		state.Cursor.Pos.Col = len(state.Buffer.Line(state.Cursor.Pos.Row))
		return ModeResult{NewMode: NewInsertMode(), Action: ActionModeChange}, true
	case 'o':
		m.openLineBelow(state)
		return ModeResult{NewMode: NewInsertMode(), Action: ActionModeChange}, true
	case 'O':
		m.openLineAbove(state)
		return ModeResult{NewMode: NewInsertMode(), Action: ActionModeChange}, true
	}
	return ModeResult{}, false
}

// handleSelection handles visual selection keys.
func (m *NormalMode) handleSelection(key Key, state *EditorState) (ModeResult, bool) {
	switch key.Rune {
	case 'v':
		if !state.Cursor.HasSelection() {
			state.Cursor.StartSelection()
		}
		return ModeResult{}, true
	case 'x':
		m.selectLine(state)
		return ModeResult{}, true
	case ';':
		state.Cursor.ClearSelection()
		return ModeResult{}, true
	}
	return ModeResult{}, false
}

// handleEditing handles delete, change, yank, and paste keys.
func (m *NormalMode) handleEditing(key Key, state *EditorState) (ModeResult, bool) {
	switch key.Rune {
	case 'd':
		if state.Cursor.HasSelection() {
			m.deleteSelection(state)
			return ModeResult{Action: ActionDelete}, true
		}
		return ModeResult{}, true
	case 'c':
		if state.Cursor.HasSelection() {
			m.deleteSelection(state)
			return ModeResult{NewMode: NewInsertMode(), Action: ActionDelete}, true
		}
		return ModeResult{}, true
	case 'y':
		if state.Cursor.HasSelection() {
			return ModeResult{Yank: true}, true
		}
		return ModeResult{}, true
	case 'p':
		return ModeResult{Paste: true}, true
	case 'P':
		return ModeResult{PasteBefore: true}, true
	}
	return ModeResult{}, false
}

// handleUndoRedo handles undo and redo keys.
func (m *NormalMode) handleUndoRedo(key Key, state *EditorState) (ModeResult, bool) {
	switch key.Rune {
	case 'u':
		if state.UndoStack.CanUndo() {
			buf, cur := state.UndoStack.Undo()
			state.Buffer = buf
			state.Cursor = cur
		}
		return ModeResult{}, true
	case 'U':
		if state.UndoStack.CanRedo() {
			buf, cur := state.UndoStack.Redo()
			state.Buffer = buf
			state.Cursor = cur
		}
		return ModeResult{}, true
	}
	return ModeResult{}, false
}

func (m *NormalMode) handleCtrl(key Key, state *EditorState) ModeResult {
	switch key.Rune {
	case 'a':
		state.Cursor.Pos.Col = 0
	case 'e':
		state.Cursor.Pos.Col = len(state.Buffer.Line(state.Cursor.Pos.Row))
	case 'p': // Ctrl+P: context picker
		return ModeResult{ContextPicker: true}
	case 'r': // Ctrl+R: history search
		return ModeResult{HistorySearch: true}
	}
	return ModeResult{}
}

func (m *NormalMode) handleAlt(key Key, state *EditorState) ModeResult {
	switch key.Special {
	case KeyLeft:
		m.moveWordBack(state)
	case KeyRight:
		m.moveWordForward(state)
	}
	switch key.Rune {
	case 'b':
		m.moveWordBack(state)
	case 'f':
		m.moveWordForward(state)
	}
	return ModeResult{}
}

func (m *NormalMode) moveLeft(state *EditorState) {
	if state.Cursor.Pos.Col > 0 {
		state.Cursor.Pos.Col = previousRuneBoundary(state.Buffer.Line(state.Cursor.Pos.Row), state.Cursor.Pos.Col)
	} else if state.Cursor.Pos.Row > 0 {
		// Wrap to end of previous line
		state.Cursor.Pos.Row--
		state.Cursor.Pos.Col = len(state.Buffer.Line(state.Cursor.Pos.Row))
	}
	state.Cursor.ClearSelection()
}

func (m *NormalMode) moveRight(state *EditorState) {
	lineLen := len(state.Buffer.Line(state.Cursor.Pos.Row))
	if state.Cursor.Pos.Col < lineLen {
		state.Cursor.Pos.Col = nextRuneBoundary(state.Buffer.Line(state.Cursor.Pos.Row), state.Cursor.Pos.Col)
	} else if state.Cursor.Pos.Row < state.Buffer.LineCount()-1 {
		// Wrap to start of next line
		state.Cursor.Pos.Row++
		state.Cursor.Pos.Col = 0
	}
	state.Cursor.ClearSelection()
}

func (m *NormalMode) moveUp(state *EditorState) {
	if state.Cursor.Pos.Row > 0 {
		state.Cursor.Pos.Row--
		lineLen := len(state.Buffer.Line(state.Cursor.Pos.Row))
		if state.Cursor.Pos.Col > lineLen {
			state.Cursor.Pos.Col = lineLen
		}
		state.Cursor.Pos.Col = clampByteIndexToRuneBoundary(state.Buffer.Line(state.Cursor.Pos.Row), state.Cursor.Pos.Col)
	}
}

func (m *NormalMode) moveDown(state *EditorState) {
	if state.Cursor.Pos.Row < state.Buffer.LineCount()-1 {
		state.Cursor.Pos.Row++
		lineLen := len(state.Buffer.Line(state.Cursor.Pos.Row))
		if state.Cursor.Pos.Col > lineLen {
			state.Cursor.Pos.Col = lineLen
		}
		state.Cursor.Pos.Col = clampByteIndexToRuneBoundary(state.Buffer.Line(state.Cursor.Pos.Row), state.Cursor.Pos.Col)
	}
}

func (m *NormalMode) moveWordForward(state *EditorState) {
	line := state.Buffer.Line(state.Cursor.Pos.Row)
	col := state.Cursor.Pos.Col

	// Skip current word
	for col < len(line) && line[col] != ' ' {
		col++
	}
	// Skip spaces
	for col < len(line) && line[col] == ' ' {
		col++
	}

	// If at end of line, try next line
	if col >= len(line) && state.Cursor.Pos.Row < state.Buffer.LineCount()-1 {
		state.Cursor.Pos.Row++
		state.Cursor.Pos.Col = 0
		return
	}

	state.Cursor.Pos.Col = col
}

func (m *NormalMode) moveWordBack(state *EditorState) {
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

func (m *NormalMode) moveWordEnd(state *EditorState) {
	line := state.Buffer.Line(state.Cursor.Pos.Row)
	col := state.Cursor.Pos.Col

	// Move forward one first
	if col < len(line) {
		col++
	}
	// Skip spaces
	for col < len(line) && line[col] == ' ' {
		col++
	}
	// Move to end of word
	for col < len(line) && line[col] != ' ' {
		col++
	}
	// Back one to be at last char
	if col > 0 {
		col = previousRuneBoundary(line, col)
	}
	state.Cursor.Pos.Col = col
}

func (m *NormalMode) selectLine(state *EditorState) {
	row := state.Cursor.Pos.Row
	lineLen := len(state.Buffer.Line(row))

	if state.Cursor.HasSelection() {
		// Extend to next line
		if row < state.Buffer.LineCount()-1 {
			state.Cursor.Pos.Row++
			state.Cursor.Pos.Col = len(state.Buffer.Line(state.Cursor.Pos.Row))
		}
	} else {
		// Select current line
		state.Cursor.Pos.Col = 0
		state.Cursor.StartSelection()
		state.Cursor.Pos.Col = lineLen
	}
}

func (m *NormalMode) openLineBelow(state *EditorState) {
	row := state.Cursor.Pos.Row
	lineLen := len(state.Buffer.Line(row))
	state.Buffer.Insert(row, lineLen, "\n")
	state.Cursor.Pos.Row++
	state.Cursor.Pos.Col = 0
}

func (m *NormalMode) openLineAbove(state *EditorState) {
	row := state.Cursor.Pos.Row
	state.Buffer.Insert(row, 0, "\n")
	state.Cursor.Pos.Col = 0
}

func (m *NormalMode) deleteSelection(state *EditorState) {
	if !state.Cursor.HasSelection() {
		return
	}
	start, end := state.Cursor.SelectionRange()
	state.Buffer.Delete(start, end)
	state.Cursor.MoveTo(start.Row, start.Col)
	state.Cursor.ClearSelection()
}
