// internal/editor/normal.go
package editor

// NormalMode handles Helix-style normal mode keybindings.
type NormalMode struct {
	pendingYank   bool
	pendingDelete bool
}

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
	// Universal bindings (Ctrl+A, Ctrl+E, etc.)
	if key.Ctrl {
		return m.handleCtrl(key, state)
	}
	if key.Alt {
		return m.handleAlt(key, state)
	}

	// Special keys
	switch key.Special {
	case KeyEnter:
		return ModeResult{Submit: true}
	case KeyEscape:
		state.Cursor.ClearSelection()
		return ModeResult{}
	case KeyUp:
		if state.Cursor.Pos.Row == 0 {
			return ModeResult{HistoryPrev: true}
		}
		m.moveUp(state)
	case KeyDown:
		if state.Cursor.Pos.Row == state.Buffer.LineCount()-1 {
			return ModeResult{HistoryNext: true}
		}
		m.moveDown(state)
	case KeyLeft:
		m.moveLeft(state)
	case KeyRight:
		m.moveRight(state)
	}

	// Normal mode commands
	switch key.Rune {
	// Movement
	case 'h':
		m.moveLeft(state)
	case 'l':
		m.moveRight(state)
	case 'j':
		if state.Cursor.Pos.Row == state.Buffer.LineCount()-1 {
			return ModeResult{HistoryNext: true}
		}
		m.moveDown(state)
	case 'k':
		if state.Cursor.Pos.Row == 0 {
			return ModeResult{HistoryPrev: true}
		}
		m.moveUp(state)
	case 'w':
		m.moveWordForward(state)
	case 'b':
		m.moveWordBack(state)
	case 'e':
		m.moveWordEnd(state)
	case '0':
		state.Cursor.Pos.Col = 0
	case '$':
		state.Cursor.Pos.Col = len(state.Buffer.Line(state.Cursor.Pos.Row))

	// Insert mode entry
	case 'i':
		return ModeResult{NewMode: NewInsertMode(), Action: ActionModeChange}
	case 'a':
		m.moveRight(state)
		return ModeResult{NewMode: NewInsertMode(), Action: ActionModeChange}
	case 'I':
		state.Cursor.Pos.Col = 0
		return ModeResult{NewMode: NewInsertMode(), Action: ActionModeChange}
	case 'A':
		state.Cursor.Pos.Col = len(state.Buffer.Line(state.Cursor.Pos.Row))
		return ModeResult{NewMode: NewInsertMode(), Action: ActionModeChange}
	case 'o':
		m.openLineBelow(state)
		return ModeResult{NewMode: NewInsertMode(), Action: ActionModeChange}
	case 'O':
		m.openLineAbove(state)
		return ModeResult{NewMode: NewInsertMode(), Action: ActionModeChange}

	// Selection
	case 'v':
		if !state.Cursor.HasSelection() {
			state.Cursor.StartSelection()
		}
	case 'x':
		m.selectLine(state)
	case ';':
		state.Cursor.ClearSelection()

	// Editing
	case 'd':
		if state.Cursor.HasSelection() {
			m.deleteSelection(state)
			return ModeResult{Action: ActionDelete}
		}
	case 'c':
		if state.Cursor.HasSelection() {
			m.deleteSelection(state)
			return ModeResult{NewMode: NewInsertMode(), Action: ActionDelete}
		}
	case 'y':
		// Yank selection to clipboard
		if state.Cursor.HasSelection() {
			return ModeResult{Yank: true}
		}
	case 'p':
		// Paste from clipboard after cursor
		return ModeResult{Paste: true}
	case 'P':
		// Paste from clipboard before cursor
		return ModeResult{PasteBefore: true}

	// Undo/Redo
	case 'u':
		if state.UndoStack.CanUndo() {
			buf, cur := state.UndoStack.Undo()
			state.Buffer = buf
			state.Cursor = cur
		}
	case 'U':
		if state.UndoStack.CanRedo() {
			buf, cur := state.UndoStack.Redo()
			state.Buffer = buf
			state.Cursor = cur
		}
	}

	return ModeResult{}
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
		state.Cursor.Pos.Col--
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
		state.Cursor.Pos.Col++
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
	}
}

func (m *NormalMode) moveDown(state *EditorState) {
	if state.Cursor.Pos.Row < state.Buffer.LineCount()-1 {
		state.Cursor.Pos.Row++
		lineLen := len(state.Buffer.Line(state.Cursor.Pos.Row))
		if state.Cursor.Pos.Col > lineLen {
			state.Cursor.Pos.Col = lineLen
		}
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
		col--
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
