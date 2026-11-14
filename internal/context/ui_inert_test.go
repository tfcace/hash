package context

import (
	"strings"
	"testing"
)

// A history item is the whole command: the picker draws it inert and on
// one row.
func TestPickerUI_DrawsItemsInertOnOneRow(t *testing.T) {
	c := NewCollection()
	c.Add(Item{Category: CategoryHistory, Key: "echo \x1b]0;pwned\x07", Value: "x"})
	c.Add(Item{Category: CategoryHistory, Key: "for i in 1\ndo echo\ndone", Value: "y"})
	ui := NewPickerUI(c)
	ui.width = 100
	s := ui.View().Content
	if strings.Contains(s, "\x1b]0;pwned") || strings.Contains(s, "\x07") {
		t.Errorf("picker draws the OSC sequence raw: %q", s)
	}
	found := false
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, "for i in 1") && strings.Contains(line, "done") {
			found = true
		}
	}
	if !found {
		t.Errorf("the multi-line command is not on one row:\n%s", s)
	}
}
