package context

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/mattn/go-runewidth"

	"github.com/tfcace/hash/internal/prompt"
)

func framedPicker() *PickerUI {
	c := NewCollection()
	c.Add(Item{Category: CategoryAutoDetect, Key: "cwd", Value: strings.Repeat("x", 24), Selected: true})
	c.Add(Item{Category: CategoryAutoDetect, Key: "git branch", Value: "master", Selected: true})
	c.Add(Item{Category: CategoryHistory, Key: "last error", Value: strings.Repeat("x", 2048)})
	ui := NewPickerUI(c)
	ui.Palette = prompt.Palette{}
	ui.width, ui.height = 80, 0 // no known height: the list alone, no preview under it
	return ui
}

func pickerRows(ui *PickerUI) []string {
	return strings.Split(ansiStrip(ui.View().Content), "\n")
}

// The picker is drawn on the frame every hash surface shares: what is
// picked against the budget on the title row, a section per category, each
// item with a circle, its size and its share of the budget, then the budget
// itself and the keys.
func TestPickerUI_IsDrawnOnTheSharedFrame(t *testing.T) {
	want := []string{
		"┌ context · 30 B of 8.0 kB  █" + strings.Repeat("░", 23),
		"├ auto-detected",
		fmt.Sprintf("│ › ● %-10s%7s", "cwd", "24 B"),
		fmt.Sprintf("│   ● %-10s%7s", "git branch", "6 B"),
		"├ history",
		fmt.Sprintf("│   ○ %-10s%7s", "last error", "2.0 kB"),
		"├ preview · cwd",
		"│ " + strings.Repeat("x", 24),
		"└─ space toggle · a all · n none · d defaults · enter confirm · esc skip",
	}
	got := pickerRows(framedPicker())
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// The meter on the title row shows what the row under the cursor would add
// before it is picked.
func TestPickerUI_TheMeterShowsWhatTheCursorRowWouldAdd(t *testing.T) {
	ui := framedPicker()
	ui.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	ui.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	rows := pickerRows(ui)
	if got, want := rows[0], "┌ context · 30 B of 8.0 kB  █▒▒▒▒▒"+strings.Repeat("░", 18); got != want {
		t.Errorf("title = %q, want %q", got, want)
	}
	if got, want := rows[5], fmt.Sprintf("│ › ○ %-10s%7s", "last error", "2.0 kB"); got != want {
		t.Errorf("cursor row = %q, want %q", got, want)
	}
}

func TestPickerUI_SaysWhenThePicksAreOverTheBudget(t *testing.T) {
	ui := framedPicker()
	ui.Palette = prompt.Palette{Primary: "#010203", Error: "#040506"}
	ui.picker.Collection().MaxSizeBytes = 1024
	ui.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	ui.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	// What the row would add is drawn in the error color when it does not fit.
	if view := ui.View().Content; !strings.Contains(view, "38;2;4;5;6m▒") {
		t.Errorf("the part that does not fit is not in the error color: %q", view)
	}
	ui.Update(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
	if got, want := pickerRows(ui)[0], "┌ context · 2.0 kB of 1.0 kB · over by 1.0 kB"; got != want {
		t.Errorf("title = %q, want %q", got, want)
	}
}

func TestPickerUI_NoRowIsAsWideAsThePane(t *testing.T) {
	for _, width := range []int{30, 50, 120} {
		ui := framedPicker()
		ui.picker.Collection().Add(Item{Category: CategoryHistory, Key: "kubectl get pods --all-namespaces --sort-by=.status.startTime", Value: "x"})
		ui.width = width
		for _, row := range pickerRows(ui) {
			if n := runewidth.StringWidth(row); n >= width {
				t.Errorf("width %d: row %q is %d columns", width, row, n)
			}
		}
	}
}

// The picker draws inline and takes its rows with it when it closes.
func TestPickerUI_DrawsInlineAndLeavesNothingBehind(t *testing.T) {
	ui := framedPicker()
	if ui.View().AltScreen {
		t.Error("the picker takes over the screen")
	}
	ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if got := ui.View().Content; got != "" {
		t.Errorf("a closed picker still draws %q", got)
	}
}

func TestPickerUI_ColorsComeFromThePalette(t *testing.T) {
	ui := framedPicker()
	ui.Palette = prompt.Palette{Primary: "#010203", Error: "#040506"}
	ui.picker.Collection().MaxSizeBytes = 16
	view := ui.View().Content
	for name, want := range map[string]string{
		"accent title":   "38;2;1;2;3mcontext",
		"accent cursor":  "38;2;1;2;3m›",
		"accent circle":  "38;2;1;2;3m●",
		"error overflow": "38;2;4;5;6m30 B of 16 B · over by 14 B",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("%s: no %q in %q", name, want, view)
		}
	}
}

func TestFormatSize_StaysWithinItsColumn(t *testing.T) {
	for bytes, want := range map[int]string{
		0: "0 B", 1023: "1023 B", 1024: "1.0 kB", 8192: "8.0 kB", 10188: "9.9 kB", 10189: "10 kB",
		512 << 10: "512 kB", 999 << 10: "999 kB", 1023 << 10: "1.0 MB", 1 << 20: "1.0 MB", 123 << 20: "123.0 MB",
	} {
		got := formatSize(bytes)
		if got != want {
			t.Errorf("formatSize(%d) = %q, want %q", bytes, got, want)
		}
		if bytes < 100<<20 && len(got) > sizeColumn {
			t.Errorf("formatSize(%d) = %q is wider than its column", bytes, got)
		}
	}
}

// The picker is drawn inline: one taller than the pane would lose its top
// rows, so the list is windowed around the cursor and says what is hidden.
func TestPickerUI_AListTallerThanThePaneIsWindowedAroundTheCursor(t *testing.T) {
	c := NewCollection()
	for i := range 20 {
		c.Add(Item{Category: CategoryHistory, Key: fmt.Sprintf("command %02d", i), Value: "x"})
	}
	ui := NewPickerUI(c)
	ui.Palette = prompt.Palette{}
	ui.width, ui.height = 80, 12

	for step := range 20 {
		rows := pickerRows(ui)
		if len(rows) >= ui.height {
			t.Fatalf("cursor %d: %d rows in a %d-row pane", step, len(rows), ui.height)
		}
		if !strings.HasPrefix(rows[0], "┌ context") || !strings.HasPrefix(rows[len(rows)-1], "└─ ") {
			t.Fatalf("cursor %d: the frame lost its title or foot:\n%s", step, strings.Join(rows, "\n"))
		}
		if want := fmt.Sprintf("› ○ command %02d", step); !strings.Contains(strings.Join(rows, "\n"), want) {
			t.Fatalf("cursor %d: its row is not shown:\n%s", step, strings.Join(rows, "\n"))
		}
		ui.picker.MoveDown()
	}

	ui.picker.cursor = 10
	out := strings.Join(pickerRows(ui), "\n")
	if !strings.Contains(out, "│ ↑ ") || !strings.Contains(out, "│ ↓ ") || !strings.Contains(out, " more") {
		t.Errorf("hidden rows are not announced:\n%s", out)
	}
}

func TestPickerUI_AListThatFitsIsShownWhole(t *testing.T) {
	ui := framedPicker()
	ui.height = 40
	if out := strings.Join(pickerRows(ui), "\n"); strings.Contains(out, " more") {
		t.Errorf("got:\n%s", out)
	}
}

// The picker keeps to part of the pane: it sits by the prompt and leaves
// the scrollback above it in view.
func TestPickerUI_TakesAtMostFortyPercentOfThePane(t *testing.T) {
	c := NewCollection()
	for i := range 40 {
		c.Add(Item{Category: CategoryHistory, Key: fmt.Sprintf("command %02d", i), Value: "x"})
	}
	ui := NewPickerUI(c)
	ui.Palette = prompt.Palette{}
	ui.width = 80
	for height, want := range map[int]int{50: 20, 100: 40, 20: 12, 10: 9} {
		ui.height = height
		rows := pickerRows(ui)
		if len(rows) != want {
			t.Errorf("height %d: the frame is %d rows, want %d", height, len(rows), want)
		}
		if !strings.HasPrefix(rows[0], "┌ context") || !strings.HasPrefix(rows[len(rows)-1], "└─ ") {
			t.Errorf("height %d: the frame lost its title or foot", height)
		}
	}
}

// Under the list is what the current item would send: a few lines of it,
// and no rows it has nothing to put on.
func TestPickerUI_PreviewsTheCurrentItemInAFewLines(t *testing.T) {
	c := NewCollection()
	c.Add(Item{Category: CategoryAutoDetect, Key: "cwd", Value: "/src/app", Selected: true})
	c.Add(Item{Category: CategoryHistory, Key: "last output", Value: strings.Repeat("a line\n", 9) + strings.Repeat("w", 300)})
	ui := NewPickerUI(c)
	ui.Palette = prompt.Palette{}
	ui.width, ui.height = 60, 50

	out := strings.Join(pickerRows(ui), "\n")
	if !strings.Contains(out, "├ preview · cwd\n│ /src/app\n└─ ") {
		t.Errorf("no short preview of the current item:\n%s", out)
	}

	ui.picker.MoveDown()
	rows := pickerRows(ui)
	out = strings.Join(rows, "\n")
	if !strings.Contains(out, "├ preview · last output\n│ a line\n│ a line\n│ a line\n│ … 7 more lines\n└─ ") {
		t.Errorf("a long value is not cut to a few lines:\n%s", out)
	}
	for _, row := range rows {
		if n := runewidth.StringWidth(row); n >= 60 {
			t.Errorf("row %q is %d columns", row, n)
		}
	}
}

// With no room for both, the list wins.
func TestPickerUI_ACrowdedFrameDropsThePreviewBeforeTheList(t *testing.T) {
	c := NewCollection()
	for i := range 40 {
		c.Add(Item{Category: CategoryHistory, Key: fmt.Sprintf("command %02d", i), Value: "x"})
	}
	ui := NewPickerUI(c)
	ui.Palette = prompt.Palette{}
	ui.width, ui.height = 80, 7
	if out := strings.Join(pickerRows(ui), "\n"); strings.Contains(out, "preview") || !strings.Contains(out, "› ○ command 00") {
		t.Errorf("got:\n%s", out)
	}
}

func TestPickerUI_APreviewIsDrawnInert(t *testing.T) {
	c := NewCollection()
	c.Add(Item{Category: CategoryHistory, Key: "last output", Value: "x\x1b]0;pwned\x07y"})
	ui := NewPickerUI(c)
	ui.width, ui.height = 80, 20
	if out := ui.View().Content; strings.Contains(out, "\x1b]0;pwned") || strings.Contains(out, "\x07") {
		t.Errorf("the value is drawn raw: %q", out)
	}
}

func TestPickerUI_ChromeIsTheTerminalsGray(t *testing.T) {
	ui := framedPicker()
	ui.Palette = prompt.DefaultPalette()
	if view := ui.View().Content; !strings.Contains(view, "\x1b[90m┌") || strings.Contains(view, "38;2;107;114;128") {
		t.Errorf("got %q", view)
	}
}

func TestPickerUI_PageKeysMoveAPageAndHomeEndJump(t *testing.T) {
	c := NewCollection()
	for i := range 40 {
		c.Add(Item{Category: CategoryHistory, Key: fmt.Sprintf("command %02d", i), Value: "x"})
	}
	ui := NewPickerUI(c)
	ui.width, ui.height = 80, 16
	press := func(code rune) { ui.Update(tea.KeyPressMsg{Code: code}) }

	press(tea.KeyPgDown)
	if got := ui.picker.Cursor(); got < 5 || got > 12 {
		t.Errorf("page down moved the cursor to %d, want about a pane's worth", got)
	}
	down := ui.picker.Cursor()
	press(tea.KeyPgUp)
	if got := ui.picker.Cursor(); got != 0 {
		t.Errorf("page up from %d: cursor %d, want 0", down, got)
	}
	press(tea.KeyEnd)
	if got := ui.picker.Cursor(); got != 39 {
		t.Errorf("end: cursor %d", got)
	}
	press(tea.KeyPgDown)
	if got := ui.picker.Cursor(); got != 39 {
		t.Errorf("page down at the end: cursor %d", got)
	}
	press(tea.KeyHome)
	if got := ui.picker.Cursor(); got != 0 {
		t.Errorf("home: cursor %d", got)
	}
}

// In a pane with room for a short frame only, the list is worth more than
// the preview: it keeps the rows.
func TestPickerUI_AShortFrameGivesItsRowsToTheList(t *testing.T) {
	c := NewCollection()
	for i := range 15 {
		c.Add(Item{Category: CategoryHistory, Key: fmt.Sprintf("command %02d", i), Value: "one\ntwo\nthree"})
	}
	ui := NewPickerUI(c)
	ui.Palette = prompt.Palette{}
	ui.width, ui.height = 80, 24 // a 12-row frame
	out := strings.Join(pickerRows(ui), "\n")
	if strings.Contains(out, "preview") || !strings.Contains(out, "command 07") {
		t.Errorf("got:\n%s", out)
	}

	ui.height = 60 // a 24-row frame: room for both
	if out := strings.Join(pickerRows(ui), "\n"); !strings.Contains(out, "├ preview · command 00") || !strings.Contains(out, "command 14") {
		t.Errorf("got:\n%s", out)
	}
}
