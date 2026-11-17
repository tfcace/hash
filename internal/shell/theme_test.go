package shell

import (
	"bytes"
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"

	"github.com/tfcace/hash/internal/markdown"
	"github.com/tfcace/hash/internal/prompt"
)

func setColorProfile(t *testing.T, p colorprofile.Profile) {
	t.Helper()
	prev := colorProfile
	colorProfile = func() colorprofile.Profile { return p }
	t.Cleanup(func() { colorProfile = prev })
}

func TestFgCodeFitsTheColorToTheTerminal(t *testing.T) {
	for _, tc := range []struct {
		profile colorprofile.Profile
		want    *regexp.Regexp
	}{
		{colorprofile.TrueColor, regexp.MustCompile(`^\x1b\[38;2;6;182;212m$`)},
		{colorprofile.ANSI256, regexp.MustCompile(`^\x1b\[38;5;\d+m$`)},
		{colorprofile.ANSI, regexp.MustCompile(`^\x1b\[(3|9)[0-7]m$`)},
		{colorprofile.ASCII, regexp.MustCompile(`^$`)},
		{colorprofile.NoTTY, regexp.MustCompile(`^$`)},
	} {
		setColorProfile(t, tc.profile)
		if got := fgCode("#06B6D4"); !tc.want.MatchString(got) {
			t.Errorf("%v: fgCode = %q, want a match for %s", tc.profile, got, tc.want)
		}
	}
}

func TestFgCodeReadsTheANSIIndexesAPromptCanYield(t *testing.T) {
	setColorProfile(t, colorprofile.TrueColor)
	if got := fgCode("2"); got != "\x1b[32m" {
		t.Errorf(`fgCode("2") = %q, want the basic green code`, got)
	}
}

func TestFgCodeIsEmptyForAColorItCannotRead(t *testing.T) {
	setColorProfile(t, colorprofile.TrueColor)
	for _, c := range []string{"", "teal", "#12", "#GGGGGG"} {
		if got := fgCode(c); got != "" {
			t.Errorf("fgCode(%q) = %q, want empty", c, got)
		}
	}
}

func TestPermissionPromptAccentFitsTheTerminal(t *testing.T) {
	render := func(p colorprofile.Profile) string {
		setColorProfile(t, p)
		var out bytes.Buffer
		NewAgentOutputCoordinator(&out).RenderPermissionPrompt("rm -rf build", "Bash", "#06B6D4")
		return out.String()
	}
	if got := render(colorprofile.TrueColor); !strings.Contains(got, "\x1b[38;2;6;182;212m") {
		t.Errorf("truecolor terminal: no 24-bit accent in %q", got)
	}
	if got := render(colorprofile.ANSI256); strings.Contains(got, "38;2;") || !strings.Contains(got, "\x1b[38;5;") {
		t.Errorf("256-color terminal: accent not downsampled in %q", got)
	}
	if got := render(colorprofile.ASCII); strings.Contains(got, "\x1b[3") {
		t.Errorf("no-color terminal: accent still colored in %q", got)
	}
}

func TestMarkdownThemeDrawsTheReplyInThePalette(t *testing.T) {
	setColorProfile(t, colorprofile.TrueColor)
	p := prompt.Palette{Primary: "#010203", Success: "#040506", Error: "#070809", Warning: "#0A0B0C"}
	got := markdownTheme(p)
	want := markdown.Theme{
		Accent:  "\x1b[38;2;1;2;3m",
		Success: "\x1b[38;2;4;5;6m",
		Error:   "\x1b[38;2;7;8;9m",
		Warning: "\x1b[38;2;10;11;12m",
	}
	if got != want {
		t.Errorf("got %+q, want %+q", got, want)
	}
}

func TestMarkdownThemeIsEmptyOnATerminalWithoutColor(t *testing.T) {
	setColorProfile(t, colorprofile.ASCII)
	if got := markdownTheme(prompt.DefaultPalette()); got != (markdown.Theme{}) {
		t.Errorf("got %+q, want no colors", got)
	}
}
