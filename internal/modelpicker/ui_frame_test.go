package modelpicker

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/mattn/go-runewidth"

	"github.com/tfcace/hash/internal/agent"
	"github.com/tfcace/hash/internal/prompt"
)

var sgr = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func rows(ui *PickerUI) []string {
	return strings.Split(sgr.ReplaceAllString(ui.View().Content, ""), "\n")
}

func threeModels() []agent.ModelOption {
	return []agent.ModelOption{
		{Value: "opus", Name: "Opus 5.5", Description: "Most capable"},
		{Value: "sonnet", Name: "Sonnet 5.5", Description: "Balanced"},
		{Value: "haiku", Name: "Haiku 5.5"},
	}
}

func manyModels(n int) []agent.ModelOption {
	models := make([]agent.ModelOption, n)
	for i := range models {
		models[i] = agent.ModelOption{Value: fmt.Sprintf("m%02d", i), Name: fmt.Sprintf("model %02d", i)}
	}
	return models
}

// The model picker is drawn on the frame every hash surface shares: the
// model in use on the title row and marked in the list, the cursor on it,
// the keys on the last row.
func TestPickerUI_IsDrawnOnTheSharedFrame(t *testing.T) {
	ui := NewPickerUI(threeModels(), "sonnet")
	ui.height = 0
	want := []string{
		"┌ model · Sonnet 5.5",
		"│   ○ Opus 5.5    Most capable",
		"│ › ● Sonnet 5.5  Balanced",
		"│   ○ Haiku 5.5",
		"└─ ↑/↓ · enter select · esc cancel",
	}
	if got := rows(ui); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestPickerUI_WithNoModelInUseTheTitleIsBare(t *testing.T) {
	ui := NewPickerUI(threeModels(), "")
	if got := rows(ui)[0]; got != "┌ model" {
		t.Errorf("title = %q", got)
	}
}

// The picker draws inline, by the prompt, and takes its rows with it.
func TestPickerUI_DrawsInlineAndLeavesNothingBehind(t *testing.T) {
	ui := NewPickerUI(threeModels(), "sonnet")
	if ui.View().AltScreen {
		t.Error("the picker takes over the screen")
	}
	ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if got := ui.View().Content; got != "" {
		t.Errorf("a closed picker still draws %q", got)
	}
}

func TestPickerUI_ColorsComeFromThePalette(t *testing.T) {
	ui := NewPickerUI(threeModels(), "sonnet")
	ui.Palette = prompt.Palette{Primary: "#010203", Dim: "8"}
	view := ui.View().Content
	for name, want := range map[string]string{
		"accent title":  "38;2;1;2;3mmodel",
		"accent cursor": "38;2;1;2;3m›",
		"accent mark":   "38;2;1;2;3m●",
		"gray chrome":   "\x1b[90m┌",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("%s: no %q in %q", name, want, view)
		}
	}
}

func TestPickerUI_TakesAtMostFortyPercentOfThePaneAndKeepsTheCursorInView(t *testing.T) {
	ui := NewPickerUI(manyModels(60), "m00")
	ui.width, ui.height = 80, 50
	for step := range 60 {
		got := rows(ui)
		if len(got) != 20 {
			t.Fatalf("cursor %d: the frame is %d rows, want 20", step, len(got))
		}
		if want := fmt.Sprintf("› ○ model %02d", step); step > 0 && !strings.Contains(strings.Join(got, "\n"), want) {
			t.Fatalf("cursor %d: its row is not shown:\n%s", step, strings.Join(got, "\n"))
		}
		ui.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	}
}

func TestPickerUI_PageKeysMoveAPageAndHomeEndJump(t *testing.T) {
	ui := NewPickerUI(manyModels(60), "m00")
	ui.width, ui.height = 80, 50
	press := func(code rune) { ui.Update(tea.KeyPressMsg{Code: code}) }

	press(tea.KeyPgDown)
	if ui.cursor < 10 || ui.cursor > 18 {
		t.Errorf("page down moved the cursor to %d, want about a frame's worth", ui.cursor)
	}
	press(tea.KeyPgUp)
	if ui.cursor != 0 {
		t.Errorf("page up: cursor %d", ui.cursor)
	}
	press(tea.KeyEnd)
	if ui.cursor != 59 {
		t.Errorf("end: cursor %d", ui.cursor)
	}
	press(tea.KeyPgDown)
	if ui.cursor != 59 {
		t.Errorf("page down at the end: cursor %d", ui.cursor)
	}
	press(tea.KeyHome)
	if ui.cursor != 0 {
		t.Errorf("home: cursor %d", ui.cursor)
	}
}

func TestPickerUI_NoRowIsAsWideAsThePane(t *testing.T) {
	models := []agent.ModelOption{{Value: "a", Name: strings.Repeat("name ", 12), Description: strings.Repeat("a long description ", 10)}, {Value: "b", Name: "b"}}
	for _, width := range []int{24, 40, 80} {
		ui := NewPickerUI(models, "a")
		ui.width = width
		for _, row := range rows(ui) {
			if n := runewidth.StringWidth(row); n >= width {
				t.Errorf("width %d: row %q is %d columns", width, row, n)
			}
		}
	}
}
