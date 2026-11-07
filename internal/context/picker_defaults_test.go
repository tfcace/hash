package context

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// Once the picker was confirmed, the selection is a choice: an item that
// appears later (the git branch after cd into a repo) is never picked. The
// "d" key goes back to the defaults, which pick such items again.
func TestPickerUI_DefaultsKeyReturnsToDefaults(t *testing.T) {
	c := NewCollection()
	c.Add(Item{Category: CategoryHistory, Key: "ls", Value: "ls", Selected: true})
	ui := NewPickerUI(c)
	_, cmd := ui.Update(tea.KeyPressMsg{Code: 'd', Text: "d"})
	if cmd == nil {
		t.Fatal("d did not end the picker")
	}
	got, err := ui.result()
	if err != nil || !got.Defaults() {
		t.Fatalf("d = (%+v, %v), want the defaults", got, err)
	}

	// A later auto-detected item is picked under the defaults.
	later := NewCollection()
	later.Add(Item{Category: CategoryAutoDetect, Key: "git branch", Value: "main", Selected: true})
	got.Apply(later)
	if !later.Items[0].Selected {
		t.Error("the defaults dropped an item detected later")
	}
}
