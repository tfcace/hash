package history

import (
	tea "charm.land/bubbletea/v2"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mattn/go-runewidth"

	"github.com/tfcace/hash/internal/prompt"
)

func framedSearch(t *testing.T, width int) (*SearchUI, []string) {
	t.Helper()
	ui := NewSearchUI(nil, prompt.Palette{})
	ui.width = width
	ui.query = "go"
	now := time.Now()
	ui.setResults(0, []Command{
		{Command: "go test ./...", Timestamp: now, Cwd: "/src", GitBranch: "main"},
		{Command: "false", Timestamp: now, ExitCode: 1},
	})
	return ui, strings.Split(stripANSI(ui.View().Content), "\n")
}

// History search is drawn on the frame every hash surface shares: a title
// row with the query, the results and the preview behind a bar, and the
// keys on the last row.
func TestSearchUI_IsDrawnOnTheSharedFrame(t *testing.T) {
	ui, rows := framedSearch(t, 100)
	date := ui.results[0].Timestamp.Format("2006-01-02 15:04")
	want := []struct{ prefix, suffix string }{
		{"┌ history · go", "go"},
		{"│ › go test ./...", "just now · main"},
		{"│   false", "✗ just now"},
		{"├ 1 of 2 · " + date + " · /src · main · ✓", "✓"},
		{"└─ ↑/↓ · pgup/pgdn · enter select · esc cancel · ctrl+y copy cmd · ctrl+o copy output", "output"},
	}
	if len(rows) != len(want) {
		t.Fatalf("got %d rows, want %d:\n%s", len(rows), len(want), strings.Join(rows, "\n"))
	}
	for i, w := range want {
		if !strings.HasPrefix(rows[i], w.prefix) || !strings.HasSuffix(rows[i], w.suffix) {
			t.Errorf("row %d = %q, want %q … %q", i, rows[i], w.prefix, w.suffix)
		}
	}
	if a, b := runewidth.StringWidth(rows[1]), runewidth.StringWidth(rows[2]); a != b {
		t.Errorf("result rows end at columns %d and %d, want the same", a, b)
	}
}

func TestSearchUI_NoRowIsAsWideAsThePane(t *testing.T) {
	for _, width := range []int{40, 60, 100} {
		_, rows := framedSearch(t, width)
		for _, row := range rows {
			if n := runewidth.StringWidth(row); n >= width {
				t.Errorf("width %d: row %q is %d columns", width, row, n)
			}
		}
	}
}

func TestSearchUI_TheFootDropsKeysThatDoNotFit(t *testing.T) {
	_, rows := framedSearch(t, 40)
	if got := rows[len(rows)-1]; got != "└─ ↑/↓ · pgup/pgdn · enter select" {
		t.Errorf("foot = %q", got)
	}
}

func TestSearchUI_EmptyStatesSitBehindTheBar(t *testing.T) {
	ui := NewSearchUI(nil, prompt.Palette{})
	ui.setResults(0, nil)
	rows := strings.Split(stripANSI(ui.View().Content), "\n")
	if rows[0] != "┌ history · type to search..." || rows[1] != "│ No history" || !strings.HasPrefix(rows[2], "└─ ") {
		t.Errorf("got:\n%s", strings.Join(rows, "\n"))
	}
}

func TestSearchUI_AStatusMessageIsTheLastBodyRow(t *testing.T) {
	ui, _ := framedSearch(t, 100)
	ui.statusMessage = "Copied command"
	rows := strings.Split(stripANSI(ui.View().Content), "\n")
	if got := rows[len(rows)-2]; got != "│ Copied command" {
		t.Errorf("row above the foot = %q", got)
	}
}

// The current row is bold behind an accent cursor; a failed command is
// marked in the error color.
func TestSearchUI_ColorsComeFromThePalette(t *testing.T) {
	ui := NewSearchUI(nil, prompt.Palette{Primary: "#010203", Error: "#040506"})
	ui.width = 100
	ui.setResults(0, []Command{{Command: "true", Timestamp: time.Now()}, {Command: "false", Timestamp: time.Now(), ExitCode: 1}})
	view := ui.View().Content
	for name, want := range map[string]string{
		"accent title":  "38;2;1;2;3mhistory",
		"accent cursor": "38;2;1;2;3m›",
		"error mark":    "38;2;4;5;6m✗",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("%s: no %q in %q", name, want, view)
		}
	}
}

// With nothing to move through, select or copy, the foot offers only the
// way out.
func TestSearchUI_WithNoResultsTheFootOffersOnlyTheWayOut(t *testing.T) {
	ui := NewSearchUI(nil, prompt.Palette{})
	ui.width = 100
	ui.query = "zzz"
	ui.setResults(0, nil)
	rows := strings.Split(stripANSI(ui.View().Content), "\n")
	if got := rows[len(rows)-1]; got != "└─ esc cancel" {
		t.Errorf("foot = %q", got)
	}
}

// The folder a command ran in is shown as the prompt shows it: under ~.
func TestSearchUI_ThePreviewShowsTheFolderUnderHome(t *testing.T) {
	t.Setenv("HOME", "/Users/someone")
	ui := NewSearchUI(nil, prompt.Palette{})
	ui.width = 100
	ui.setResults(0, []Command{{Command: "ls", Timestamp: time.Now(), Cwd: "/Users/someone/projects/app"}})
	out := stripANSI(ui.View().Content)
	if !strings.Contains(out, " · ~/projects/app · ") || strings.Contains(out, "/Users/someone") {
		t.Errorf("got:\n%s", out)
	}
}

// A long command beside a long branch name must still fit: the search is
// drawn inline, and a row that wraps is left behind when it closes.
func TestSearchUI_ALongCommandBesideALongBranchFitsThePane(t *testing.T) {
	for _, width := range []int{30, 40, 60, 100} {
		ui := NewSearchUI(nil, prompt.Palette{})
		ui.width = width
		ui.setResults(0, []Command{{
			Command:   "kubectl get pods --all-namespaces -o json | jq '.items[] | select(.status.phase != \"Running\")'",
			Timestamp: time.Now().Add(-3 * time.Hour),
			Cwd:       "/srv/" + strings.Repeat("deeply/nested/", 12) + "app",
			GitBranch: "feature/JIRA-12345-rework-the-entire-billing-pipeline-for-q4",
			ExitCode:  1,
		}})
		rows := strings.Split(stripANSI(ui.View().Content), "\n")
		for _, row := range rows {
			if n := runewidth.StringWidth(row); n >= width {
				t.Errorf("width %d: row %q is %d columns", width, row, n)
			}
		}
		if !strings.Contains(rows[1], "kubectl") {
			t.Errorf("width %d: the result row lost its command: %q", width, rows[1])
		}
	}
}

func TestSearchUI_AResultKeepsItsBranchWhenThereIsRoom(t *testing.T) {
	_, rows := framedSearch(t, 100)
	if !strings.Contains(rows[1], "go test ./...") || !strings.HasSuffix(strings.TrimRight(rows[1], " "), "· main") {
		t.Errorf("got %q", rows[1])
	}
}

func manyResults(n int) []Command {
	cmds := make([]Command, n)
	for i := range cmds {
		cmds[i] = Command{Command: fmt.Sprintf("echo %02d", i), Timestamp: time.Now()}
	}
	return cmds
}

func resultRows(rows []string) int {
	n := 0
	for _, row := range rows {
		if strings.Contains(row, "echo ") && !strings.HasPrefix(row, "│ echo") {
			n++
		}
	}
	return n
}

// The search keeps to part of the pane: it sits by the prompt and leaves
// the scrollback above it in view.
func TestSearchUI_TakesAtMostFortyPercentOfThePane(t *testing.T) {
	for height, want := range map[int]int{20: 12, 50: 20, 100: 40, 10: 9} {
		ui := NewSearchUI(nil, prompt.Palette{})
		ui.width, ui.height = 100, height
		ui.setResults(0, manyResults(80))
		rows := strings.Split(stripANSI(ui.View().Content), "\n")
		if len(rows) != want {
			t.Errorf("height %d: the frame is %d rows, want %d", height, len(rows), want)
		}
		// The title, the selected result's row and the foot are the rest.
		if got := resultRows(rows); got != want-3 {
			t.Errorf("height %d: %d results listed, want %d", height, got, want-3)
		}
	}
}

// The selected command is not repeated under the list when its row shows
// all of it; one cut short there is shown whole.
func TestSearchUI_ShowsTheWholeCommandOnlyWhenItsRowCutIt(t *testing.T) {
	long := "kubectl get pods --all-namespaces -o json | jq '.items[] | select(.status.phase != \"Running\")'"
	ui := NewSearchUI(nil, prompt.Palette{})
	ui.width, ui.height = 60, 50
	ui.setResults(0, []Command{{Command: "ls", Timestamp: time.Now()}, {Command: long, Timestamp: time.Now()}})

	rows := strings.Split(stripANSI(ui.View().Content), "\n")
	if len(rows) != 5 || !strings.HasPrefix(rows[3], "├ 1 of 2 · ") {
		t.Fatalf("a short command got more than its row:\n%s", strings.Join(rows, "\n"))
	}

	pressKey(ui, tea.KeyDown)
	rows = strings.Split(stripANSI(ui.View().Content), "\n")
	var whole []string
	for _, row := range rows[4 : len(rows)-1] {
		whole = append(whole, strings.TrimPrefix(row, "│ "))
	}
	if got := strings.Join(whole, " "); got != long {
		t.Errorf("the cut command is not shown whole under the list: %q\n%s", got, strings.Join(rows, "\n"))
	}
}

func TestSearchUI_TheSelectionStaysInViewInATallPane(t *testing.T) {
	ui := NewSearchUI(nil, prompt.Palette{})
	ui.width, ui.height = 100, 30
	ui.setResults(0, manyResults(80))
	for range 60 {
		ui.selected++
		ui.adjustScroll()
	}
	out := stripANSI(ui.View().Content)
	if !strings.Contains(out, "› echo 60") {
		t.Errorf("the selected result is not shown:\n%s", out)
	}
}

// A long multi-line command's preview must not push the results out.
func TestSearchUI_ALongPreviewLeavesRoomForResults(t *testing.T) {
	ui := NewSearchUI(nil, prompt.Palette{})
	ui.width, ui.height = 100, 20
	cmds := manyResults(30)
	cmds[0].Command = "echo 00\n" + strings.Repeat("  line\n", 40) + "done"
	ui.setResults(0, cmds)
	rows := strings.Split(stripANSI(ui.View().Content), "\n")
	if len(rows) > 12 {
		t.Errorf("the frame is %d rows, over its share of a 20-row pane", len(rows))
	}
	if resultRows(rows) < 5 || !strings.Contains(strings.Join(rows, "\n"), "more lines") {
		t.Errorf("got:\n%s", strings.Join(rows, "\n"))
	}
}

func TestSearchUI_BeforeThePaneIsKnownItListsTen(t *testing.T) {
	ui := NewSearchUI(nil, prompt.Palette{})
	ui.width, ui.height = 100, 0
	ui.setResults(0, manyResults(80))
	if got := resultRows(strings.Split(stripANSI(ui.View().Content), "\n")); got != 10 {
		t.Errorf("%d results listed, want 10", got)
	}
}

func TestSearchUI_ChromeIsTheTerminalsGray(t *testing.T) {
	ui := NewSearchUI(nil, prompt.DefaultPalette())
	ui.width = 100
	ui.setResults(0, manyResults(3))
	if view := ui.View().Content; !strings.Contains(view, "\x1b[90m┌") || strings.Contains(view, "38;2;107;114;128") {
		t.Errorf("got %q", view)
	}
}

// A long list is walked a page at a time and jumped to its ends.
func TestSearchUI_PageKeysMoveAPageAndHomeEndJump(t *testing.T) {
	ui := NewSearchUI(nil, prompt.Palette{})
	ui.width, ui.height = 100, 20
	ui.setResults(0, manyResults(80))
	page := ui.visibleResults()

	pressKey(ui, tea.KeyPgDown)
	if ui.selected != page {
		t.Errorf("page down: selected %d, want %d", ui.selected, page)
	}
	pressKey(ui, tea.KeyPgDown)
	pressKey(ui, tea.KeyPgUp)
	if ui.selected != page {
		t.Errorf("page down twice then up: selected %d, want %d", ui.selected, page)
	}
	pressKey(ui, tea.KeyEnd)
	if ui.selected != 79 || !strings.Contains(stripANSI(ui.View().Content), "› echo 79") {
		t.Errorf("end: selected %d", ui.selected)
	}
	pressKey(ui, tea.KeyPgDown)
	if ui.selected != 79 {
		t.Errorf("page down at the end: selected %d", ui.selected)
	}
	pressKey(ui, tea.KeyHome)
	if ui.selected != 0 || ui.scrollOffset != 0 {
		t.Errorf("home: selected %d, offset %d", ui.selected, ui.scrollOffset)
	}
	pressKey(ui, tea.KeyPgUp)
	if ui.selected != 0 {
		t.Errorf("page up at the top: selected %d", ui.selected)
	}
}

// The way out is the last key to go when the pane is narrow.
func TestSearchUI_TheFootKeepsTheWayOutBeforeTheCopyKeys(t *testing.T) {
	_, rows := framedSearch(t, 60)
	if got := rows[len(rows)-1]; got != "└─ ↑/↓ · pgup/pgdn · enter select · esc cancel" {
		t.Errorf("foot = %q", got)
	}
}

// A command shown whole is broken between its words where it can be.
func TestBreakColumns_BreaksAtSpaces(t *testing.T) {
	got := breakColumns("echo alpha beta gamma delta", 12)
	want := []string{"echo alpha", "beta gamma", "delta"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("got %q, want %q", got, want)
	}
	if got := breakColumns("a "+strings.Repeat("x", 30), 12); len(got) != 4 || got[1] != strings.Repeat("x", 12) {
		t.Errorf("a word longer than the row is not cut to it: %q", got)
	}
	for _, row := range breakColumns(strings.Repeat("日本 ", 20), 11) {
		if w := runewidth.StringWidth(row); w > 11 {
			t.Errorf("row %q is %d columns", row, w)
		}
	}
}
