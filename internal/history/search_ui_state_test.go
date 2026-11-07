package history

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/mattn/go-runewidth"
	"github.com/tfcace/hash/internal/prompt"
)

func newStateSearchUI(t *testing.T, commands ...string) *SearchUI {
	t.Helper()
	store, err := NewStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	base := time.Now().Add(-time.Hour)
	for i, c := range commands { // later commands are more recent
		store.Add(Command{Command: c, Timestamp: base.Add(time.Duration(i) * time.Minute)})
	}
	ui := NewSearchUI(store, prompt.DefaultPalette())
	ui.Init()
	return ui
}

func typeKey(ui *SearchUI, text string) tea.Cmd {
	r := []rune(text)[0]
	_, cmd := ui.Update(tea.KeyPressMsg{Code: r, Text: text})
	return cmd
}

func pressKey(ui *SearchUI, code rune) tea.Cmd {
	_, cmd := ui.Update(tea.KeyPressMsg{Code: code})
	return cmd
}

// Enter right after typing, before the debounced search ran, must pick from
// the typed query, not from the previous query's results.
func TestSearchUI_EnterDuringDebounceUsesTypedQuery(t *testing.T) {
	ui := newStateSearchUI(t, "ls -la", "git status")
	typeKey(ui, "l")
	pressKey(ui, tea.KeyEnter)
	if got := ui.chosen(); got != "ls -la" {
		t.Fatalf("chosen = %q, want %q", got, "ls -la")
	}
}

// A slow search for an older query must not replace newer results.
func TestSearchUI_StaleResultsAreDropped(t *testing.T) {
	ui := newStateSearchUI(t, "ls -la", "git status", "git log")
	typeKey(ui, "g")
	_, first := ui.Update(debounceMsg{id: ui.debounceID})
	stale := first() // the search for "g", finishing late

	typeKey(ui, "i")
	typeKey(ui, "t")
	typeKey(ui, " ")
	typeKey(ui, "s")
	_, second := ui.Update(debounceMsg{id: ui.debounceID})
	ui.Update(second())
	ui.Update(stale)

	if len(ui.results) != 1 || ui.results[0].Command != "git status" {
		t.Fatalf("results = %v, want only the match for %q", ui.results, ui.query)
	}
}

// The search command must not read the model from its goroutine while
// Update edits the query (run with -race).
func TestSearchUI_SearchDoesNotRaceQueryEdits(t *testing.T) {
	ui := newStateSearchUI(t, "ls -la")
	cmd := ui.search()
	done := make(chan struct{})
	go func() {
		cmd()
		close(done)
	}()
	typeKey(ui, "x")
	<-done
}

// Cancel and accept are outcomes of their own, apart from the selection.
func TestSearchUI_OutcomeIsExplicit(t *testing.T) {
	ui := newStateSearchUI(t, "ls -la")
	pressKey(ui, tea.KeyEscape)
	if got := ui.chosen(); got != "" {
		t.Fatalf("Esc chose %q", got)
	}

	ui = newStateSearchUI(t, "ls -la")
	pressKey(ui, tea.KeyEnter)
	if got := ui.chosen(); got != "ls -la" {
		t.Fatalf("Enter chose %q, want %q", got, "ls -la")
	}
}

// The query is edited by character, not byte.
func TestSearchUI_MultibyteQuery(t *testing.T) {
	ui := newStateSearchUI(t, "echo café")
	typeKey(ui, "é")
	if ui.query != "é" {
		t.Fatalf("query = %q, want %q", ui.query, "é")
	}
	typeKey(ui, "x")
	pressKey(ui, tea.KeyBackspace)
	pressKey(ui, tea.KeyBackspace)
	if ui.query != "" {
		t.Fatalf("query after two backspaces = %q, want empty", ui.query)
	}
}

// A long command of multibyte characters is cut on a character boundary, to
// the row's width.
func TestSearchUI_TruncatesByDisplayWidth(t *testing.T) {
	ui := newStateSearchUI(t, "ls "+strings.Repeat("日本", 60))
	ui.width = 60
	line := ui.formatResultLine(ui.results[0], 1, lipgloss.NewStyle(), lipgloss.NewStyle(), lipgloss.NewStyle(), lipgloss.NewStyle())
	if !utf8.ValidString(line) {
		t.Fatalf("row is not valid UTF-8: %q", line)
	}
	if w := runewidth.StringWidth(line); w > ui.width {
		t.Fatalf("row is %d columns wide, want at most %d", w, ui.width)
	}
}

// highlighted is the command the view marks as selected, or "" for none.
func highlighted(ui *SearchUI) string {
	for _, line := range strings.Split(ui.View().Content, "\n") {
		if rest, ok := strings.CutPrefix(stripANSI(line), "> "); ok {
			for _, c := range ui.results {
				if strings.HasPrefix(rest, c.Command) {
					return c.Command
				}
			}
		}
	}
	return ""
}

func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			for i++; i < len(s) && (s[i] < 'a' || s[i] > 'z') && (s[i] < 'A' || s[i] > 'Z'); i++ {
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// While a newer query is pending, the previous query's rows may stay on
// screen (no flicker) but are never highlighted, previewed or chosen.
func TestSearchUI_StaleRowsAreNotSelectable(t *testing.T) {
	ui := newStateSearchUI(t, "ls -la", "git status", "git log")
	typeKey(ui, "g") // the debounced search for "g" has not run
	if got := highlighted(ui); got != "" {
		t.Errorf("a stale row is highlighted: %q", got)
	}
	if strings.Contains(ui.View().Content, "Preview") {
		t.Error("a stale row is previewed")
	}
}

// Enter picks what the view highlights. Moving the highlight while a query
// is pending moves it over that query's results; it used to move over the
// stale rows, and Enter then re-searched and silently took row 0.
func TestSearchUI_EnterPicksTheHighlightedRow(t *testing.T) {
	ui := newStateSearchUI(t, "ls -la", "git status", "git log")
	typeKey(ui, "g")
	pressKey(ui, tea.KeyDown)
	want := highlighted(ui)
	if want == "" {
		t.Fatal("nothing highlighted after Down")
	}
	pressKey(ui, tea.KeyEnter)
	if got := ui.chosen(); got != want {
		t.Fatalf("chosen = %q, the view highlighted %q", got, want)
	}
	if !strings.HasPrefix(want, "g") {
		t.Fatalf("highlighted %q is not a result for %q", want, ui.query)
	}
}

// Results that land for a query the UI already searched (a key brought them
// up to date first) must not reset the highlight the user moved.
func TestSearchUI_LateDuplicateResultsKeepTheHighlight(t *testing.T) {
	ui := newStateSearchUI(t, "ls -la", "git status", "git log")
	typeKey(ui, "g")
	_, search := ui.Update(debounceMsg{id: ui.debounceID})
	pressKey(ui, tea.KeyDown)
	want := highlighted(ui)
	ui.Update(search()) // the debounced search for "g" lands afterwards
	if got := highlighted(ui); got != want {
		t.Fatalf("highlight moved from %q to %q when the same query's results landed", want, got)
	}
}

// Once the search ends, Enter or Esc, its last frame is empty: bubbletea
// leaves an inline program's last frame on the screen, so the box, the
// list and the preview stayed under the next prompt.
func TestSearchUI_EndingLeavesNothingDrawn(t *testing.T) {
	for name, key := range map[string]rune{"Enter": tea.KeyEnter, "Esc": tea.KeyEscape} {
		t.Run(name, func(t *testing.T) {
			ui := newStateSearchUI(t, "ls -la", "git status")
			if ui.View().Content == "" {
				t.Fatal("the open search draws nothing")
			}
			pressKey(ui, key)
			if got := ui.View().Content; got != "" {
				t.Errorf("the ended search still draws %q", got)
			}
		})
	}
}

// A command run many times is listed once, at its most recent run.
func TestSearchUI_ListsEachCommandOnce(t *testing.T) {
	ui := newStateSearchUI(t, "echo one", "echo two", "echo one", "echo one")
	var got []string
	for _, c := range ui.results {
		got = append(got, c.Command)
	}
	if strings.Join(got, ",") != "echo one,echo two" {
		t.Errorf("results = %q, want each command once, most recent first", got)
	}
	if ui.totalResults != 2 {
		t.Errorf("totalResults = %d, want 2", ui.totalResults)
	}
}
