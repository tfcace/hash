package listui

import (
	"fmt"
	"strings"
	"testing"
)

func TestFrameRows(t *testing.T) {
	for height, want := range map[int]int{0: 0, -1: 0, 100: 40, 50: 20, 30: 12, 20: 12, 13: 12, 12: 11, 5: 4} {
		if got := FrameRows(height); got != want {
			t.Errorf("FrameRows(%d) = %d, want %d", height, got, want)
		}
	}
}

func more(hidden int, above bool) string {
	if above {
		return fmt.Sprintf("^%d", hidden)
	}
	return fmt.Sprintf("v%d", hidden)
}

func TestWindow(t *testing.T) {
	list := strings.Split("a b c d e f g h i j", " ")
	for _, tt := range []struct {
		cursor, room int
		want         string
	}{
		{0, 20, "a b c d e f g h i j"}, // it fits: whole
		{0, 4, "a b c v7"},             // top: only rows below are hidden
		{5, 4, "^4 e f v4"},            // middle: the cursor's row between the two counts
		{9, 4, "^7 h i j"},             // bottom
		{4, 1, "^4 e v5"},              // never fewer than three rows
	} {
		if got := strings.Join(Window(list, tt.cursor, tt.room, more), " "); got != tt.want {
			t.Errorf("Window(cursor %d, room %d) = %q, want %q", tt.cursor, tt.room, got, tt.want)
		}
	}
}
