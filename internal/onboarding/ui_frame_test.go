package onboarding

import (
	"regexp"
	"strings"
	"testing"

	"github.com/mattn/go-runewidth"

	"github.com/tfcace/hash/internal/prompt"
)

var sgr = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func rows(m *Model) []string {
	return strings.Split(sgr.ReplaceAllString(m.View().Content, ""), "\n")
}

func pair() []Agent {
	return []Agent{{Name: "Claude", Desc: "Anthropic's agent"}, {Name: "Gemini CLI", Desc: "Google's agent"}}
}

// The welcome is drawn on the frame every hash surface shares: the agents
// to pick from, the quick start under them, the keys on the last row.
func TestWelcome_IsDrawnOnTheSharedFrame(t *testing.T) {
	want := []string{
		"┌ welcome · hash 0.6.2",
		"│ Treat AI like a Unix pipe, not a magic wand.",
		"├ pick an agent to enable ??",
		"│ › Claude      Anthropic's agent · found",
		"│   Gemini CLI  Google's agent · found",
		"│   Skip        use hash as a plain shell",
		"├ quick start",
		"│ ??        ask the AI    ctrl+r    search history",
		"│ cmd | ??  pipe to AI    ctrl+p    pick context",
		"│ copy cmd  copy command  copy out  copy last output",
		"└─ ↑/↓ · enter choose · s skip · docs runhash.dev/docs",
	}
	if got := rows(New(pair(), "0.6.2")); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestWelcome_WithNoAgentSaysHowToSetOneUp(t *testing.T) {
	want := []string{
		"┌ welcome · hash 0.6.2",
		"│ Treat AI like a Unix pipe, not a magic wand.",
		"│ No agent found yet: hash works as a plain shell.",
		"│ Set one up to enable ??: runhash.dev/docs",
		"├ quick start",
		"│ ??        ask the AI    ctrl+r    search history",
		"│ cmd | ??  pipe to AI    ctrl+p    pick context",
		"│ copy cmd  copy command  copy out  copy last output",
		"└─ enter continue · docs runhash.dev/docs",
	}
	if got := rows(New(nil, "0.6.2")); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// The welcome draws inline, above the first prompt, and takes its rows
// with it once answered.
func TestWelcome_DrawsInlineAndLeavesNothingBehind(t *testing.T) {
	m := New(pair(), "0.6.2")
	if m.View().AltScreen {
		t.Error("the welcome takes over the screen")
	}
	m = press(m, "enter")
	if got := m.View().Content; got != "" {
		t.Errorf("an answered welcome still draws %q", got)
	}
}

func TestWelcome_ColorsComeFromThePalette(t *testing.T) {
	m := New(pair(), "0.6.2")
	m.Palette = prompt.Palette{Primary: "#010203", Dim: "8"}
	view := m.View().Content
	for name, want := range map[string]string{
		"accent title":  "38;2;1;2;3mwelcome",
		"accent cursor": "38;2;1;2;3m›",
		"accent keys":   "38;2;1;2;3m??",
		"gray chrome":   "\x1b[90m┌",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("%s: no %q in %q", name, want, view)
		}
	}
}

func TestWelcome_NoRowIsAsWideAsThePane(t *testing.T) {
	for _, width := range []int{30, 44, 80} {
		for _, m := range []*Model{New(pair(), "0.6.2"), New(nil, "0.6.2")} {
			m.width = width
			for _, row := range rows(m) {
				if n := runewidth.StringWidth(row); n >= width {
					t.Errorf("width %d: row %q is %d columns", width, row, n)
				}
			}
		}
	}
}
