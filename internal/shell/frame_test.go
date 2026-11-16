package shell

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/colorprofile"

	"github.com/tfcace/hash/internal/agentupdate"
	"github.com/tfcace/hash/internal/learning"
	"github.com/tfcace/hash/internal/prompt"
)

const gray = frameGray

func TestFrameTopIsATitleInItsColorAndASummary(t *testing.T) {
	got := frameTop("permission", "Bash", "\x1b[35m")
	want := gray + "┌ " + ansiReset + ansiBold + "\x1b[35m" + "permission" + ansiReset + gray + " · " + ansiReset + "Bash"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got := stripANSI(frameTop("fix", "", "")); got != "┌ fix" {
		t.Errorf("without a summary: %q", got)
	}
}

func TestFrameRowAndFoot(t *testing.T) {
	withColumns(t, 80)
	if got, want := frameRow("body"), gray+"│"+ansiReset+" body"; got != want {
		t.Errorf("row %q, want %q", got, want)
	}
	if got, want := frameFoot("enter to run", "esc to dismiss"), gray+"└─ enter to run · esc to dismiss"+ansiReset; got != want {
		t.Errorf("foot %q, want %q", got, want)
	}
}

func TestFrameFootDropsHintsThatDoNotFit(t *testing.T) {
	withColumns(t, 24)
	if got := stripANSI(frameFoot("enter to run", "esc to dismiss")); got != "└─ enter to run" {
		t.Errorf("foot %q", got)
	}
}

// bannerPalette has a color per role that no other role uses.
func bannerPalette() prompt.Palette {
	return prompt.Palette{Primary: "#010101", Success: "#020202", Error: "#030303", Warning: "#040404"}
}

const (
	bannerAccent  = "\x1b[38;2;1;1;1m"
	bannerSuccess = "\x1b[38;2;2;2;2m"
	bannerError   = "\x1b[38;2;3;3;3m"
	bannerWarning = "\x1b[38;2;4;4;4m"
)

func bannerHandler(t *testing.T) (*ErrorHandler, *bytes.Buffer) {
	t.Helper()
	setColorProfile(t, colorprofile.TrueColor)
	withColumns(t, 100)
	var buf bytes.Buffer
	return &ErrorHandler{out: &buf, palette: bannerPalette}, &buf
}

func TestBanner_CommandNotFound(t *testing.T) {
	h, buf := bannerHandler(t)
	h.HandleCommandNotFound("jq", []string{"jp", "jj"}, "brew install jq")
	h.RefusalFooter(true)

	want := "\n" +
		"┌ ✗ jq · command not found\n" +
		"│ did you mean: jp, jj?\n" +
		"│ install: brew install jq\n" +
		"└─ → to accept at prompt · esc to dismiss · ?? to explain\n"
	if got := stripANSI(buf.String()); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	if !strings.Contains(buf.String(), ansiBold+bannerError+"✗ jq") {
		t.Errorf("the title is not in the error color: %q", buf.String())
	}
}

func TestBanner_CommandNotFoundWithNothingToOffer(t *testing.T) {
	h, buf := bannerHandler(t)
	h.HandleCommandNotFound("jq", nil, "")
	h.RefusalFooter(false)
	if got, want := stripANSI(buf.String()), "\n┌ ✗ jq · command not found\n└─ ?? to explain\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBanner_NotExecutable(t *testing.T) {
	h, buf := bannerHandler(t)
	h.HandleNotExecutable("./deploy.sh", "Permission denied")
	if got, want := stripANSI(buf.String()), "\n┌ ✗ ./deploy.sh · Permission denied\n└─ ?? to explain\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBanner_DidYouMean(t *testing.T) {
	h, buf := bannerHandler(t)
	h.showDidYouMean("git checkout master")
	want := "\n┌ did you mean\n│ → git checkout master\n└─ → to accept at prompt · esc to dismiss · ?? to explain\n"
	if got := stripANSI(buf.String()); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	for _, colored := range []string{ansiBold + bannerWarning + "did you mean", bannerSuccess + "→"} {
		if !strings.Contains(buf.String(), colored) {
			t.Errorf("missing %q in %q", colored, buf.String())
		}
	}
}

func TestBanner_LearnedFix(t *testing.T) {
	h, buf := bannerHandler(t)
	h.showLearnedFix(learning.Fix{Fix: "go mod tidy", SuccessCount: 4}, true)
	want := "\n┌ fix · worked 4×\n│ → go mod tidy\n└─ → to accept at prompt · esc to dismiss · ?? to explain\n"
	if got := stripANSI(buf.String()); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if !strings.Contains(buf.String(), ansiBold+bannerWarning+"fix") {
		t.Errorf("the title is not in the warning color: %q", buf.String())
	}
}

// A fix that is not pre-typed at the prompt has nothing to accept.
func TestBanner_PossibleFix(t *testing.T) {
	h, buf := bannerHandler(t)
	h.showLearnedFix(learning.Fix{Fix: "go mod tidy", SuccessCount: 1, FailureCount: 2}, false)
	want := "\n┌ possible fix · tried 3×, worked 1×\n│ ? go mod tidy\n└─ ?? to explain\n"
	if got := stripANSI(buf.String()); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBanner_UpdateAvailable(t *testing.T) {
	h, buf := bannerHandler(t)
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	h.showUpdateAvailable(agentupdate.Notice{Installed: "0.42.0", Latest: "0.88.0", InstalledAt: now.AddDate(0, -4, -2)}, now)
	want := "\n┌ update · " + agentupdate.DisplayName + " 0.42.0 → 0.88.0 · installed 4 months ago\n" +
		"│ → " + updateCommand + "\n" +
		"└─ → to accept at prompt · esc to dismiss\n"
	if got := stripANSI(buf.String()); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if !strings.Contains(buf.String(), ansiBold+bannerAccent+"update") {
		t.Errorf("the title is not in the accent color: %q", buf.String())
	}
}

// A handler that was given no palette (a fallback one) still draws.
func TestBanner_WithoutAPaletteUsesTheDefaultOne(t *testing.T) {
	setColorProfile(t, colorprofile.TrueColor)
	var buf bytes.Buffer
	(&ErrorHandler{out: &buf}).HandleNotExecutable("./x", "Permission denied")
	if !strings.Contains(buf.String(), fgCode(prompt.DefaultPalette().Error)) {
		t.Errorf("no default error color in %q", buf.String())
	}
}

func TestPermissionPrompt_IsAFrameOfFourRows(t *testing.T) {
	setColorProfile(t, colorprofile.TrueColor)
	withColumns(t, 100)
	var buf bytes.Buffer
	aoc := NewAgentOutputCoordinator(&buf)
	aoc.RenderPermissionPrompt("kubectl get pods", "Bash", "#010101")

	want := "\r\n" + ansiClearLine +
		"\r\n" + ansiClearLine + "┌ permission · Bash" +
		"\r\n" + ansiClearLine + "│ kubectl get pods" +
		"\r\n" + ansiClearLine + "└─ y to allow · n or esc to deny · a to always allow · ctrl+c to cancel"
	if got := stripANSIKeepingClears(buf.String()); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	for _, styled := range []string{ansiBold + bannerAccent + "permission", ansiBold + "kubectl get pods"} {
		if !strings.Contains(buf.String(), styled) {
			t.Errorf("missing %q in %q", styled, buf.String())
		}
	}

	buf.Reset()
	aoc.ClearPermissionPrompt()
	if got, want := buf.String(), "\r"+ansiClearLine+strings.Repeat(ansiCursorUp+"\r"+ansiClearLine, 3); got != want {
		t.Errorf("clear wrote %q, want four rows erased %q", got, want)
	}
}

func TestPermissionPrompt_WithoutAToolNameHasNoSummary(t *testing.T) {
	var buf bytes.Buffer
	aoc := NewAgentOutputCoordinator(&buf)
	aoc.RenderPermissionPrompt("ls", "", "")
	if got := stripANSI(buf.String()); !strings.Contains(got, "┌ permission\r\n") {
		t.Errorf("got %q", got)
	}
}

func TestPermissionPrompt_TheTooQuickNoteRedrawsTheFoot(t *testing.T) {
	withColumns(t, 120)
	var buf bytes.Buffer
	aoc := NewAgentOutputCoordinator(&buf)
	aoc.RenderPermissionPrompt("ls", "", "")
	buf.Reset()
	aoc.NoteTooQuick()
	if got := stripANSIKeepingClears(buf.String()); !strings.HasPrefix(got, "\r"+ansiClearLine+"└─ "+tooQuickNote+" · y to allow") {
		t.Errorf("got %q", got)
	}
}

// stripANSIKeepingClears strips colors but keeps the line clears a prompt's
// rows start with.
func stripANSIKeepingClears(s string) string {
	return sgrSeqRe.ReplaceAllString(s, "")
}

// permissionTitle marks a permission prompt in raw output: its title as the
// frame closes it.
const permissionTitle = "permission" + ansiReset
