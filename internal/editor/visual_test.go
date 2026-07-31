package editor

import (
	"testing"
)

// fixedLayout is a test LineLayout with a fixed wrap width, a prompt prefix
// on row 0 and a continuation prefix on later rows.
type fixedLayout struct {
	width   int
	promptW int
	contW   int
}

func (f fixedLayout) WrapWidth() int { return f.width }

func (f fixedLayout) PrefixWidth(row int) int {
	if row == 0 {
		return f.promptW
	}
	return f.contW
}

func newVisualState(layout LineLayout, content string) *EditorState {
	state := NewEditorState()
	state.Buffer = NewBufferFromString(content)
	state.Layout = layout
	return state
}

func TestVisualUp_MovesWithinWrappedLine(t *testing.T) {
	// width 10, prompt 2: "abcdefghijklmnop" renders as two visual rows
	state := newVisualState(fixedLayout{10, 2, 3}, "abcdefghijklmnop")
	state.Cursor.MoveTo(0, 12) // abs 14: visual row 1, col 4

	if !visualUp(state) {
		t.Fatal("visualUp on the second visual row must move, not signal history")
	}
	if state.Cursor.Pos.Row != 0 || state.Cursor.Pos.Col != 2 {
		t.Errorf("cursor = (%d,%d), want (0,2) (same screen column, one visual row up)",
			state.Cursor.Pos.Row, state.Cursor.Pos.Col)
	}
}

func TestVisualDown_MovesWithinWrappedLine(t *testing.T) {
	state := newVisualState(fixedLayout{10, 2, 3}, "abcdefghijklmnop")
	state.Cursor.MoveTo(0, 2) // abs 4: visual row 0, col 4

	if !visualDown(state) {
		t.Fatal("visualDown with a second visual row below must move")
	}
	if state.Cursor.Pos.Row != 0 || state.Cursor.Pos.Col != 12 {
		t.Errorf("cursor = (%d,%d), want (0,12)", state.Cursor.Pos.Row, state.Cursor.Pos.Col)
	}
}

func TestVisualUp_AtTopSignalsHistory(t *testing.T) {
	state := newVisualState(fixedLayout{10, 2, 3}, "abcdefghijklmnop")
	state.Cursor.MoveTo(0, 5) // visual row 0

	if visualUp(state) {
		t.Fatal("visualUp on the first visual row must return false (history)")
	}
	if state.Cursor.Pos.Row != 0 || state.Cursor.Pos.Col != 5 {
		t.Error("failed visualUp must not move the cursor")
	}
}

func TestVisualDown_AtBottomSignalsHistory(t *testing.T) {
	state := newVisualState(fixedLayout{10, 2, 3}, "abcdefghijklmnop")
	state.Cursor.MoveTo(0, 12) // last visual row

	if visualDown(state) {
		t.Fatal("visualDown on the last visual row must return false (history)")
	}
}

func TestVisual_GoalColumnAcrossShortLine(t *testing.T) {
	layout := fixedLayout{40, 0, 0}
	state := newVisualState(layout, "aaaaaaaaaaaaaaa\nab\nbbbbbbbbbbbbbbb")
	state.Cursor.MoveTo(0, 10)

	if !visualDown(state) {
		t.Fatal("first visualDown must move")
	}
	if state.Cursor.Pos.Row != 1 || state.Cursor.Pos.Col != 2 {
		t.Fatalf("after first down cursor = (%d,%d), want clamped (1,2)",
			state.Cursor.Pos.Row, state.Cursor.Pos.Col)
	}
	if !visualDown(state) {
		t.Fatal("second visualDown must move")
	}
	if state.Cursor.Pos.Row != 2 || state.Cursor.Pos.Col != 10 {
		t.Errorf("goal column not restored: cursor = (%d,%d), want (2,10)",
			state.Cursor.Pos.Row, state.Cursor.Pos.Col)
	}
}

func TestVisual_GoalInvalidatedByOtherMovement(t *testing.T) {
	layout := fixedLayout{40, 0, 0}
	state := newVisualState(layout, "aaaaaaaaaaaaaaa\nab\nbbbbbbbbbbbbbbb")
	state.Cursor.MoveTo(0, 10)

	visualDown(state)        // (1,2), goal 10 remembered
	state.Cursor.Pos.Col = 1 // simulate a horizontal move
	visualDown(state)

	if state.Cursor.Pos.Row != 2 || state.Cursor.Pos.Col != 1 {
		t.Errorf("stale goal should be dropped: cursor = (%d,%d), want (2,1)",
			state.Cursor.Pos.Row, state.Cursor.Pos.Col)
	}
}

func TestVisualUp_EntersPreviousLineLastVisualRow(t *testing.T) {
	state := newVisualState(fixedLayout{10, 2, 3}, "abcdefghijklmnop\nzz")
	state.Cursor.MoveTo(1, 1) // abs 4 on row 1

	if !visualUp(state) {
		t.Fatal("visualUp from second logical line must move")
	}
	// Lands on the LAST visual row of the wrapped previous line, same screen col
	if state.Cursor.Pos.Row != 0 || state.Cursor.Pos.Col != 12 {
		t.Errorf("cursor = (%d,%d), want (0,12)", state.Cursor.Pos.Row, state.Cursor.Pos.Col)
	}
}

func TestVisual_WideRunesAreNotSplit(t *testing.T) {
	// Wide CJK runes: goal column landing inside a wide rune stays before it
	layout := fixedLayout{40, 0, 0}
	state := newVisualState(layout, "abc\n日本語")
	state.Cursor.MoveTo(0, 3) // screen col 3

	visualDown(state)
	// 日=2 cols, 本=2 cols; screen col 3 falls inside 本 -> land before it
	if state.Cursor.Pos.Row != 1 || state.Cursor.Pos.Col != len("日") {
		t.Errorf("cursor = (%d,%d), want (1,%d) (before 本)",
			state.Cursor.Pos.Row, state.Cursor.Pos.Col, len("日"))
	}
}

func TestInsertMode_UpOnWrappedLineDoesNotHijackHistory(t *testing.T) {
	state := newVisualState(fixedLayout{10, 2, 3}, "abcdefghijklmnop")
	state.Cursor.MoveTo(0, 12) // second visual row
	mode := NewInsertMode()

	result, handled := mode.handleSpecialKey(Key{Special: KeyUp}, state)
	if !handled {
		t.Fatal("KeyUp should be handled")
	}
	if result.HistoryPrev {
		t.Fatal("Up on a lower visual row must move within the line, not open history")
	}
	if state.Cursor.Pos.Col != 2 {
		t.Errorf("cursor col = %d, want 2", state.Cursor.Pos.Col)
	}

	// Now on the first visual row: Up opens history
	result, _ = mode.handleSpecialKey(Key{Special: KeyUp}, state)
	if !result.HistoryPrev {
		t.Fatal("Up on the first visual row must signal HistoryPrev")
	}
}

func TestNormalMode_KOnWrappedLineDoesNotHijackHistory(t *testing.T) {
	state := newVisualState(fixedLayout{10, 2, 3}, "abcdefghijklmnop")
	state.Cursor.MoveTo(0, 12)
	mode := NewNormalMode()

	result := mode.HandleKey(Key{Rune: 'k'}, state)
	if result.HistoryPrev {
		t.Fatal("k on a lower visual row must move within the line")
	}
	if state.Cursor.Pos.Col != 2 {
		t.Errorf("cursor col = %d, want 2", state.Cursor.Pos.Col)
	}
}

func TestVisualUp_PromptWiderThanTerminalStillReachesHistory(t *testing.T) {
	// Prompt (25) wider than the terminal (10): the cursor's visual row is
	// never 0, but Up from the line's first character must still reach
	// history instead of looping in place.
	state := newVisualState(fixedLayout{10, 25, 3}, "abc")
	state.Cursor.MoveTo(0, 1)

	if !visualUp(state) {
		t.Fatal("first Up should still move (to the line start)")
	}
	if state.Cursor.Pos.Col != 0 {
		t.Fatalf("cursor col = %d, want 0", state.Cursor.Pos.Col)
	}
	if visualUp(state) {
		t.Fatal("Up at the line's first character must signal history")
	}
}

func TestVisual_NilLayoutFallsBackToLogicalRows(t *testing.T) {
	state := NewEditorState()
	state.Buffer = NewBufferFromString("aaaa\nbb")
	state.Cursor.MoveTo(1, 2)

	if !visualUp(state) {
		t.Fatal("logical fallback should move up between logical rows")
	}
	if state.Cursor.Pos.Row != 0 || state.Cursor.Pos.Col != 2 {
		t.Errorf("cursor = (%d,%d), want (0,2)", state.Cursor.Pos.Row, state.Cursor.Pos.Col)
	}
	if visualUp(state) {
		t.Fatal("logical fallback at row 0 should signal history")
	}
}
