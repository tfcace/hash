package modelpicker

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/tfcace/hash/internal/agent"
)

func models() []agent.ModelOption {
	return []agent.ModelOption{
		{Value: "default", Name: "Default"},
		{Value: "sonnet", Name: "Sonnet"},
		{Value: "haiku", Name: "Haiku"},
	}
}

func TestNewPickerUIStartsOnCurrent(t *testing.T) {
	// current by value
	if ui := NewPickerUI(models(), "sonnet"); ui.cursor != 1 {
		t.Errorf("cursor = %d, want 1 (sonnet by value)", ui.cursor)
	}
	// current by display name
	if ui := NewPickerUI(models(), "Haiku"); ui.cursor != 2 {
		t.Errorf("cursor = %d, want 2 (haiku by name)", ui.cursor)
	}
	// unknown current → top
	if ui := NewPickerUI(models(), "nope"); ui.cursor != 0 {
		t.Errorf("cursor = %d, want 0 (unknown current)", ui.cursor)
	}
}

func press(ui *PickerUI, key string) *PickerUI {
	model, _ := ui.Update(keyMsg(key))
	return model.(*PickerUI)
}

// keyMsg builds a KeyPressMsg whose String() matches the key name. Special keys
// carry only a Code; printable keys carry Text.
func keyMsg(key string) tea.KeyPressMsg {
	switch key {
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	default:
		return tea.KeyPressMsg{Code: rune(key[0]), Text: key}
	}
}

func TestPickerNavigationClamps(t *testing.T) {
	ui := NewPickerUI(models(), "default") // cursor 0
	ui = press(ui, "up")                   // clamp at top
	if ui.cursor != 0 {
		t.Fatalf("cursor after up at top = %d, want 0", ui.cursor)
	}
	ui = press(ui, "down")
	ui = press(ui, "down")
	ui = press(ui, "down") // clamp at bottom (len 3)
	if ui.cursor != 2 {
		t.Fatalf("cursor after 3x down = %d, want 2", ui.cursor)
	}
}

func TestPickerEnterConfirms(t *testing.T) {
	ui := NewPickerUI(models(), "default")
	ui = press(ui, "down") // → sonnet
	ui = press(ui, "enter")
	if !ui.confirmed {
		t.Fatal("confirmed = false after enter, want true")
	}
	if ui.models[ui.cursor].Value != "sonnet" {
		t.Fatalf("selected value = %q, want sonnet", ui.models[ui.cursor].Value)
	}
}

func TestPickerEscCancels(t *testing.T) {
	ui := NewPickerUI(models(), "default")
	ui = press(ui, "esc")
	if ui.confirmed {
		t.Fatal("confirmed = true after esc, want false")
	}
}
