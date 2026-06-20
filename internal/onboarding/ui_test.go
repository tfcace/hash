package onboarding

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func two() []Agent { return []Agent{Known[0], Known[1]} }

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

func press(m *Model, key string) *Model {
	next, _ := m.Update(keyMsg(key))
	return next.(*Model)
}

// With agents detected, choices are the agents plus a trailing Skip row.
func TestEnterOnAgentSelectsIt(t *testing.T) {
	m := New(two(), "0.6.2")
	m = press(m, "down") // → second agent (Gemini)
	m = press(m, "enter")
	got, chosen := m.Result()
	if !chosen {
		t.Fatal("Result chosen=false after selecting an agent")
	}
	if got.Command != "gemini" {
		t.Errorf("selected %q, want gemini", got.Command)
	}
}

func TestSkipKeyChoosesNothing(t *testing.T) {
	m := New(two(), "0.6.2")
	m = press(m, "s")
	if _, chosen := m.Result(); chosen {
		t.Fatal("Result chosen=true after pressing s (skip)")
	}
	if !m.done {
		t.Error("model not done after skip")
	}
}

// The Skip row sits after the agents; Enter on it chooses nothing.
func TestEnterOnSkipRowChoosesNothing(t *testing.T) {
	m := New(two(), "0.6.2")
	m = press(m, "down")
	m = press(m, "down") // onto Skip row (index len(agents))
	m = press(m, "enter")
	if _, chosen := m.Result(); chosen {
		t.Fatal("Result chosen=true after Enter on Skip row")
	}
}

func TestCursorClampsAtSkipRow(t *testing.T) {
	m := New(two(), "0.6.2") // rows: agent0, agent1, skip → max index 2
	for i := 0; i < 5; i++ {
		m = press(m, "down")
	}
	if m.cursor != 2 {
		t.Fatalf("cursor = %d, want clamped at 2 (skip row)", m.cursor)
	}
	for i := 0; i < 5; i++ {
		m = press(m, "up")
	}
	if m.cursor != 0 {
		t.Fatalf("cursor = %d, want clamped at 0", m.cursor)
	}
}

// Empty state: no adapters detected. Only the Skip/continue row exists, and
// Enter chooses nothing (drops into a plain shell).
func TestEmptyStateEnterChoosesNothing(t *testing.T) {
	m := New(nil, "0.6.2")
	m = press(m, "enter")
	if _, chosen := m.Result(); chosen {
		t.Fatal("Result chosen=true in empty state")
	}
}

// The panel always carries the orientation content and a docs pointer.
func TestViewContainsTipsAndDocs(t *testing.T) {
	view := New(nil, "0.6.2").render()
	for _, want := range []string{"??", "Ctrl+R", "runhash.dev/docs"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q", want)
		}
	}
}

func TestEmptyStateViewMentionsNoAgent(t *testing.T) {
	view := New(nil, "0.6.2").render()
	if !strings.Contains(strings.ToLower(view), "no agent") {
		t.Errorf("empty-state view should say no agent found:\n%s", view)
	}
}
