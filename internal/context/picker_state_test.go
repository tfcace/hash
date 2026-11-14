package context

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/mattn/go-runewidth"
)

// The picker shows items grouped by category; the highlighted row must be
// the one Space toggles, whatever order the items were added in.
func TestPicker_ToggleMatchesDisplayOrder(t *testing.T) {
	c := NewCollection()
	c.Add(Item{Category: CategoryHistory, Key: "ls", Value: "ls"})
	c.Add(Item{Category: CategoryHistory, Key: "pwd", Value: "pwd"})
	c.Add(Item{Category: CategoryAutoDetect, Key: "last output", Value: "out"})

	p := NewPicker(c)
	// Display order: last output (auto-detected), then ls, pwd.
	if got := p.CurrentItem().Key; got != "last output" {
		t.Fatalf("first row = %q, want the auto-detected item listed first", got)
	}
	p.MoveDown()
	p.ToggleCurrent()
	if got := p.CurrentItem().Key; got != "ls" {
		t.Fatalf("second row = %q, want ls", got)
	}
	for _, it := range c.Items {
		if it.Selected != (it.Key == "ls") {
			t.Fatalf("toggled the wrong item: %+v", c.Items)
		}
	}
}

// Canceling is not confirming an empty selection: the caller keeps what it
// had.
func TestPickerUI_CancelIsExplicit(t *testing.T) {
	c := NewCollection()
	c.Add(Item{Category: CategoryHistory, Key: "ls", Value: "ls", Selected: true})
	ui := NewPickerUI(c)
	ui.Update(tea.KeyPressMsg{Code: tea.KeyEscape})

	if _, err := ui.result(); !errors.Is(err, ErrPickerCanceled) {
		t.Fatalf("result err = %v, want ErrPickerCanceled", err)
	}
	if !c.Items[0].Selected {
		t.Error("cancel changed the selection")
	}

	ui = NewPickerUI(c)
	ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if got, err := ui.result(); err != nil || got.Defaults() {
		t.Fatalf("confirm = (%+v, %v), want a picked selection", got, err)
	}
}

// A long multibyte key is cut on a character boundary to fit the row.
func TestPickerUI_TruncatesKeyByDisplayWidth(t *testing.T) {
	c := NewCollection()
	c.Add(Item{Category: CategoryHistory, Key: "echo " + strings.Repeat("日本", 60), Value: "x"})
	ui := NewPickerUI(c)
	ui.width = 40
	view := ui.View().Content
	if !utf8.ValidString(view) {
		t.Fatalf("view is not valid UTF-8")
	}
	for _, line := range strings.Split(view, "\n") {
		if !strings.Contains(line, "[ ]") {
			continue // header and footer are fixed text
		}
		if w := runewidth.StringWidth(ansiStrip(line)); w > ui.width {
			t.Fatalf("row %q is %d columns, want at most %d", line, w, ui.width)
		}
	}
}

func ansiStrip(s string) string {
	var b strings.Builder
	in := false
	for _, r := range s {
		switch {
		case r == '\x1b':
			in = true
		case in && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'):
			in = false
		case !in:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Esc puts back the marks the picker opened on: a toggle made before
// leaving must not stick to the collection.
func TestPickerUI_CancelRestoresToggles(t *testing.T) {
	c := NewCollection()
	c.Add(Item{Category: CategoryHistory, Key: "ls", Value: "ls", Selected: true})
	ui := NewPickerUI(c)
	ui.Update(tea.KeyPressMsg{Code: tea.KeySpace})
	ui.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if !c.Items[0].Selected {
		t.Error("cancel left the toggle in the collection")
	}
}

// "n for none" then Enter is a choice of no context, not the defaults.
func TestPickerUI_NoneIsNotDefaults(t *testing.T) {
	c := NewBuilder().AutoDetect().Build()
	c.Add(Item{Category: CategoryHistory, Key: "ls", Value: "ls"})
	ui := NewPickerUI(c)
	ui.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	sel, err := ui.result()
	if err != nil {
		t.Fatal(err)
	}
	if sel.Defaults() {
		t.Fatal("none read as the defaults")
	}
	fresh := NewBuilder().AutoDetect().Build() // defaults marked
	sel.Apply(fresh)
	if got := fresh.SelectedItems(); len(got) != 0 {
		t.Errorf("none applied still selects %v", got)
	}
}

// A selection is reapplied by identity to a collection built later, so the
// picker opens on what was picked and Enter keeps it.
func TestSelection_AppliesByIdentity(t *testing.T) {
	c := NewBuilder().WithHistory([]string{"ls", "pwd"}).WithEnvVars([]string{"HOME"}).Build()
	c.Items[1].Selected = true // pwd
	sel := SelectionOf(c)

	later := NewBuilder().WithHistory([]string{"make", "pwd", "ls"}).WithEnvVars([]string{"HOME"}).Build()
	sel.Apply(later)
	var got []string
	for _, it := range later.SelectedItems() {
		got = append(got, it.Key)
	}
	if len(got) != 1 || got[0] != "pwd" {
		t.Fatalf("selected after apply = %v, want [pwd]", got)
	}
	var zero Selection
	if !zero.Defaults() {
		t.Error("the zero Selection is the defaults")
	}
	defaults := NewBuilder().AutoDetect().Build()
	zero.Apply(defaults)
	if len(defaults.SelectedItems()) == 0 && len(defaults.Items) > 0 {
		t.Error("applying the defaults cleared the default marks")
	}
}

// History items are keyed by the whole command: two long commands that
// share their first 40 characters are two items.
func TestBuilder_LongHistoryCommandsKeepDistinctKeys(t *testing.T) {
	a := "kubectl get pods --namespace production-cluster-a"
	b := "kubectl get pods --namespace production-cluster-b"
	c := NewBuilder().WithHistory([]string{a, b}).Build()
	c.Items[0].Selected = true
	sel := SelectionOf(c)
	later := NewBuilder().WithHistory([]string{a, b}).Build()
	sel.Apply(later)
	if got := later.SelectedItems(); len(got) != 1 || got[0].Value != a {
		t.Fatalf("picking %q selected %v", a, got)
	}
}
