package shell

import (
	"bytes"
	"math/rand"
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
	"github.com/mattn/go-runewidth"
)

func TestResponseUI_ShowAgentHintIncludesACPInstallCommand(t *testing.T) {
	var buf bytes.Buffer
	ui := NewResponseUI(&buf)

	ui.ShowAgentHint("stdio", "claude-agent-acp", "")
	output := buf.String()

	if !strings.Contains(output, "npm install -g @agentclientprotocol/claude-agent-acp") {
		t.Fatalf("expected claude-agent-acp install hint, got:\n%s", output)
	}
	if !strings.Contains(output, "which claude-agent-acp") {
		t.Fatalf("expected PATH hint for claude-agent-acp, got:\n%s", output)
	}
}

func TestAgentStateStringUsesScopedConversationStyle(t *testing.T) {
	got := AgentStateThinking.String()
	if !bytes.Contains([]byte(got), []byte("agent ·")) {
		t.Fatalf("thinking state should include scoped agent label, got %q", got)
	}
	if !bytes.Contains([]byte(got), []byte("thinking")) {
		t.Fatalf("thinking state should use lower-case status copy, got %q", got)
	}
}

func spinnerGlyphs(cells []spinnerCell) string {
	var b strings.Builder
	for _, c := range cells {
		b.WriteString(c.glyph)
	}
	return b.String()
}

// settledMask shows which cells have resolved: "ha.." is two letters in.
func settledMask(cells []spinnerCell) string {
	var b strings.Builder
	for _, c := range cells {
		if c.settled {
			b.WriteString(c.glyph)
		} else {
			b.WriteString(".")
		}
	}
	return b.String()
}

func TestSpinnerResolvesTheNameLeftToRight(t *testing.T) {
	for frame, want := range map[int]string{
		0: "....", 5: "....", 6: "h...", 11: "ha..", 16: "has.", 21: "hash", 37: "hash",
	} {
		if got := settledMask(spinnerCells(frame, nil)); got != want {
			t.Errorf("frame %d: settled %q, want %q", frame, got, want)
		}
	}
}

func TestSpinnerDissolvesAndStartsOver(t *testing.T) {
	for frame, want := range map[int]string{38: "▓▓▓▓", 40: "▒▒▒▒", 42: "░░░░", 44: "    ", 45: "    "} {
		cells := spinnerCells(frame, nil)
		if got := spinnerGlyphs(cells); got != want {
			t.Errorf("frame %d: glyphs %q, want %q", frame, got, want)
		}
		if got := settledMask(cells); got != "...." {
			t.Errorf("frame %d: a dissolving cell is drawn as settled: %q", frame, got)
		}
	}
	if got := settledMask(spinnerCells(spinnerCycle, nil)); got != "...." {
		t.Errorf("the cycle does not start over with noise: %q", got)
	}
	if got := settledMask(spinnerCells(spinnerCycle+6, nil)); got != "h..." {
		t.Errorf("the second cycle does not resolve again: %q", got)
	}
}

func TestSpinnerNoiseComesFromTheGlyphPoolAndNeverSpellsALetter(t *testing.T) {
	rng := rand.New(rand.NewSource(7)) //nolint:gosec // test animation
	for frame := 0; frame < 6; frame++ {
		for _, c := range spinnerCells(frame, rng) {
			if !containsString(spinnerNoise, c.glyph) {
				t.Errorf("frame %d: noise glyph %q is not in the pool", frame, c.glyph)
			}
		}
	}
	for _, g := range spinnerNoise {
		if strings.Contains(spinnerName, g) {
			t.Errorf("noise glyph %q would read as a letter of the name", g)
		}
		if runewidth.StringWidth(g) != 1 {
			t.Errorf("noise glyph %q is not one column wide", g)
		}
	}
	for _, want := range []string{"░", "▒", "▓", "█", "-", "\\", "|", "/", "+", "*", "$", "#"} {
		if !containsString(spinnerNoise, want) {
			t.Errorf("the noise pool lost %q", want)
		}
	}
}

func TestSpinnerNoiseChangesFromFrameToFrameWithoutARandomSource(t *testing.T) {
	if spinnerGlyphs(spinnerCells(0, nil)) == spinnerGlyphs(spinnerCells(1, nil)) {
		t.Error("two noise frames in a row are identical")
	}
}

// The label must not shift as the spinner animates.
func TestSpinnerAlwaysTakesFourColumns(t *testing.T) {
	rng := rand.New(rand.NewSource(7)) //nolint:gosec // test animation
	for frame := 0; frame < 2*spinnerCycle; frame++ {
		if got := runewidth.StringWidth(spinnerGlyphs(spinnerCells(frame, rng))); got != 4 {
			t.Fatalf("frame %d is %d columns wide, want 4", frame, got)
		}
	}
}

func TestSpinnerLettersAreBoldAccentAndNoiseFaintAccent(t *testing.T) {
	setColorProfile(t, colorprofile.TrueColor)
	line := formatSpinner("agent · thinking", spinnerCells(6, nil), "#06B6D4")

	const accent = "\x1b[38;2;6;182;212m"
	if want := ansiBold + accent + "h" + ansiReset; !strings.Contains(line, want) {
		t.Errorf("the settled letter is not bold accent: %q", line)
	}
	if want := ansiFaint + accent; strings.Count(line, want) != 3 {
		t.Errorf("the three noise cells are not faint accent: %q", line)
	}
	if want := "\033[90magent · thinking\033[0m"; !strings.HasSuffix(line, want) {
		t.Errorf("the label is not dim: %q", line)
	}
	if !strings.HasPrefix(line, " ") {
		t.Errorf("the row is not indented by one space: %q", line)
	}
}

func TestSpinnerWithoutAReadableAccentIsCyan(t *testing.T) {
	setColorProfile(t, colorprofile.TrueColor)
	line := formatSpinner("agent · thinking", spinnerCells(21, nil), "")
	if !strings.Contains(line, ansiBold+"\x1b[36m"+"h") {
		t.Errorf("no cyan fallback: %q", line)
	}
}

func TestSpinnerKeepsItsShapeOnATerminalWithoutColor(t *testing.T) {
	setColorProfile(t, colorprofile.ASCII)
	line := formatSpinner("agent · thinking", spinnerCells(21, nil), "#06B6D4")
	if strings.Contains(line, "38;") || strings.Contains(line, "[36m") {
		t.Errorf("accent color on a terminal without color: %q", line)
	}
	if got := stripANSI(line); got != " hash agent · thinking" {
		t.Errorf("row reads %q", got)
	}
}

func containsString(values []string, want string) bool {
	return indexString(values, want) >= 0
}

func indexString(values []string, want string) int {
	for i, value := range values {
		if value == want {
			return i
		}
	}
	return -1
}

// An agent's error message is drawn inert: an escape sequence in it shows
// as text instead of driving the terminal (clearing the screen, setting
// the title). Its lines stay lines.
func TestResponseUI_ShowErrorDrawsTheMessageInert(t *testing.T) {
	var buf bytes.Buffer
	NewResponseUI(&buf).ShowError("rpc failed\x1b[2J\x1b]0;pwned\x07\nsecond line")
	got := buf.String()
	if want := "\033[31m✗ rpc failed^[[2J^[]0;pwned^G\nsecond line\033[0m\n"; got != want {
		t.Errorf("ShowError wrote %q, want %q", got, want)
	}
}
