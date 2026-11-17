package markdown

import (
	"strings"
	"testing"
)

const (
	tAccent  = "\x1b[38;5;201m"
	tSuccess = "\x1b[38;5;202m"
	tError   = "\x1b[38;5;203m"
	tWarning = "\x1b[38;5;204m"
)

func themed() *StreamingRenderer {
	r := NewStreamingRenderer()
	r.Theme = Theme{Accent: tAccent, Success: tSuccess, Error: tError, Warning: tWarning}
	return r
}

func renderAll(r *StreamingRenderer, text string) string {
	return r.Write(text) + r.Finish()
}

func TestTheme_AccentColorsBulletsCodeSpansAndHeaders(t *testing.T) {
	for in, want := range map[string]string{
		"- item\n":      tAccent + "•" + reset + " item\n",
		"1. item\n":     tAccent + "1." + reset + " item\n",
		"run `ls`\n":    "run " + tAccent + "ls" + reset + "\n",
		"# Title\n":     bold + tAccent + "Title" + reset + "\n",
		"## Title\n":    bold + tAccent + "Title" + reset + "\n",
		"### Title\n":   bold + tAccent + "Title" + reset + "\n",
		"#### Title\n":  bold + "Title" + reset + "\n",
		"**a `b` c**\n": bold + "a " + tAccent + "b" + reset + bold + " c" + reset + "\n",
	} {
		if got := renderAll(themed(), in); got != want {
			t.Errorf("%q rendered %q, want %q", in, got, want)
		}
	}
}

// A terminal without color gets an empty theme: nothing falls back to a
// fixed color.
func TestTheme_AnEmptyThemeDrawsNoAccent(t *testing.T) {
	r := NewStreamingRenderer()
	r.Theme = Theme{}
	if got, want := renderAll(r, "- run `ls`\n"), "•"+reset+" run ls"+reset+"\n"; got != want {
		t.Errorf("rendered %q, want %q", got, want)
	}
}

func TestTheme_SurvivesReset(t *testing.T) {
	r := themed()
	r.Reset()
	if r.Theme.Accent != tAccent {
		t.Error("Reset dropped the theme")
	}
}

func TestFence_ALanguageTitlesTheBlock(t *testing.T) {
	got := renderAll(themed(), "```bash\necho hi\n```\nafter\n")
	want := gray + "┌ bash" + reset + "\n" +
		CodeLinePrefix + "echo hi" + reset + "\n" +
		"after\n"
	if got != want {
		t.Errorf("rendered %q, want %q", got, want)
	}
}

func TestFence_TheRestOfTheInfoStringJoinsTheTitle(t *testing.T) {
	got := stripAnsi(renderAll(themed(), "```diff deploy.yml\n```\n"))
	if want := "┌ diff · deploy.yml\n"; got != want {
		t.Errorf("rendered %q, want %q", got, want)
	}
}

func TestFence_WithoutALanguageHasNoTitleRow(t *testing.T) {
	got := renderAll(themed(), "```\nx\n```\n")
	if want := CodeLinePrefix + "x" + reset + "\n"; got != want {
		t.Errorf("rendered %q, want %q", got, want)
	}
}

func TestFence_CodeSitsBehindABar(t *testing.T) {
	if got, want := stripAnsi(renderAll(themed(), "```\nx\n```\n")), "│ x\n"; got != want {
		t.Errorf("rendered %q, want %q", got, want)
	}
}

func TestFence_ADiffColorsAddedAndRemovedLines(t *testing.T) {
	got := renderAll(themed(), "```diff\n- old\n+ new\n same\n```\n")
	want := gray + "┌ diff" + reset + "\n" +
		CodeLinePrefix + tError + "- old" + reset + "\n" +
		CodeLinePrefix + tSuccess + "+ new" + reset + "\n" +
		CodeLinePrefix + " same" + reset + "\n"
	if got != want {
		t.Errorf("rendered %q, want %q", got, want)
	}
}

func TestFence_OnlyADiffColorsPlusAndMinus(t *testing.T) {
	got := renderAll(themed(), "```bash\n+ x\n- y\n```\n")
	if strings.Contains(got, tSuccess) || strings.Contains(got, tError) {
		t.Errorf("a bash block was colored as a diff: %q", got)
	}
}

func TestFence_ADiffLineSplitByAFlushIsColoredOnce(t *testing.T) {
	r := themed()
	var out strings.Builder
	out.WriteString(r.Write("```diff\n"))
	out.WriteString(r.Write("+"))
	out.WriteString(r.Flush())
	out.WriteString(r.Write(" new\n"))
	want := gray + "┌ diff" + reset + "\n" + CodeLinePrefix + tSuccess + "+ new" + reset + "\n"
	if got := out.String(); got != want {
		t.Errorf("rendered %q, want %q", got, want)
	}
}

func TestQuote_SitsBehindAGrayBar(t *testing.T) {
	got := renderAll(themed(), "> said **so**\nafter\n")
	want := gray + "│ " + reset + "said " + bold + "so" + reset + "\n" + "after\n"
	if got != want {
		t.Errorf("rendered %q, want %q", got, want)
	}
}

func TestCallout_IsTitledAndBarredInItsKindsColor(t *testing.T) {
	for kind, color := range map[string]string{
		"NOTE": tAccent, "TIP": tAccent, "IMPORTANT": tAccent, "WARNING": tWarning, "CAUTION": tError, "warning": tWarning,
	} {
		got := renderAll(themed(), "> [!"+kind+"]\n> pods are down\n>\n> twice\n\nafter\n")
		bar := color + "│" + reset
		want := color + "┌ " + strings.ToLower(kind) + reset + "\n" +
			bar + " pods are down\n" +
			bar + "\n" +
			bar + " twice\n" +
			"\n" + "after\n"
		if got != want {
			t.Errorf("%s rendered %q, want %q", kind, got, want)
		}
	}
}

func TestCallout_EndsWithItsQuote(t *testing.T) {
	got := renderAll(themed(), "> [!WARNING]\n> a\n\n> b\n")
	if want := gray + "│ " + reset + "b\n"; !strings.HasSuffix(got, want) {
		t.Errorf("the quote after a callout kept its color: %q", got)
	}
}

func TestCallout_AnUnknownKindIsAnOrdinaryQuote(t *testing.T) {
	got := stripAnsi(renderAll(themed(), "> [!SHRUG]\n"))
	if want := "│ [!SHRUG]\n"; got != want {
		t.Errorf("rendered %q, want %q", got, want)
	}
}

// The first fragment decides how a line opens, so a quote that may still
// turn out to be a callout waits.
func TestCallout_AMarkerStillArrivingIsHeld(t *testing.T) {
	for _, fragment := range []string{">", "> ", "> [", "> [!", "> [!WARN", "> [!WARNING]"} {
		r := themed()
		if got := r.Write(fragment) + r.Flush(); got != "" {
			t.Errorf("%q drew %q before its line was decided", fragment, got)
		}
	}
	r := themed()
	if got := stripAnsi(r.Write("> so") + r.Flush()); got != "│ so" {
		t.Errorf("an ordinary quote drew %q", got)
	}
	if got := r.Write(" it goes\n"); got != " it goes\n" {
		t.Errorf("its continuation drew %q", got)
	}
}

func TestChecklist_DoneIsAFilledCircleAndDimTodoAnOpenOne(t *testing.T) {
	for in, want := range map[string]string{
		"- [x] freeze\n":       tAccent + "●" + reset + " " + dim + "freeze" + reset + "\n",
		"- [X] freeze\n":       tAccent + "●" + reset + " " + dim + "freeze" + reset + "\n",
		"* [x] freeze\n":       tAccent + "●" + reset + " " + dim + "freeze" + reset + "\n",
		"- [ ] write\n":        "○ write\n",
		"  - [ ] nested\n":     "  ○ nested\n",
		"- [x] run `ls` now\n": tAccent + "●" + reset + " " + dim + "run " + tAccent + "ls" + reset + dim + " now" + reset + "\n",
		"- [y] not a box\n":    tAccent + "•" + reset + " [y] not a box\n",
	} {
		if got := renderAll(themed(), in); got != want {
			t.Errorf("%q rendered %q, want %q", in, got, want)
		}
	}
}

func TestChecklist_ABoxStillArrivingIsHeld(t *testing.T) {
	for _, fragment := range []string{"- ", "- [", "- [x", "- [x]", "- [ ]", "  * ["} {
		r := themed()
		if got := r.Write(fragment) + r.Flush(); got != "" {
			t.Errorf("%q drew %q before its line was decided", fragment, got)
		}
	}
	r := themed()
	if got := stripAnsi(r.Write("- [x] fr") + r.Flush() + r.Write("eeze\n")); got != "● freeze\n" {
		t.Errorf("a split item drew %q", got)
	}
}

func TestWrapper_ABarContinuesOnWrappedRows(t *testing.T) {
	callout := tWarning + "│" + reset + " aaa bbb ccc ddd\n"
	want := tWarning + "│" + reset + " aaa bbb\n" + tWarning + "│" + reset + " ccc ddd\n"
	if got := wrapAll(12, callout); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	quote := gray + "│ " + reset + "aaa bbb ccc ddd\n"
	if got, want := stripAnsi(wrapAll(12, quote)), "│ aaa bbb\n│ ccc ddd\n"; got != want {
		t.Errorf("a quote wrapped as %q, want %q", got, want)
	}
}

func TestWrapper_ABarDoesNotCarryToTheNextLine(t *testing.T) {
	got := stripAnsi(wrapAll(12, tWarning+"│"+reset+" aaa\n"+"bbb ccc ddd eee\n"))
	if want := "│ aaa\nbbb ccc ddd\neee\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestWrapper_AChecklistItemContinuesUnderItsText(t *testing.T) {
	got := stripAnsi(wrapAll(12, "○ aaa bbb ccc ddd\n● eee fff ggg hhh\n"))
	if want := "○ aaa bbb\n  ccc ddd\n● eee fff\n  ggg hhh\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// A word too long for any row stays on the bar's row: breaking first would
// leave a row holding only the bar.
func TestWrapper_ABarStaysWithAWordTooLongForARow(t *testing.T) {
	if got, want := wrapAll(8, "│ 00000000 11\n"), "│ 00000000\n│ 11\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCallout_EndsAtACodeBlockOrATable(t *testing.T) {
	for _, between := range []string{"```\nx\n```\n", "| a | b |\n|---|---|\n| 1 | 2 |\n"} {
		got := renderAll(themed(), "> [!WARNING]\n> a\n"+between+"> b\n")
		if want := gray + "│ " + reset + "b\n"; !strings.HasSuffix(got, want) {
			t.Errorf("after %q the quote kept the callout's color: %q", between, got)
		}
	}
}
