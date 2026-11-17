package prompt

import (
	"testing"
)

func TestParseANSIColors_Basic(t *testing.T) {
	// Green foreground: ESC[32m
	input := "\033[32m❯\033[0m"
	colors := parseANSIColors(input)

	if len(colors) == 0 {
		t.Fatal("Expected to find at least one color")
	}

	// Should find green (color 32 = ANSI green)
	found := false
	for _, c := range colors {
		if c == "32" || c == "2" { // 32 or basic green
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Expected to find green color, got: %v", colors)
	}
}

func TestParseANSIColors_256Color(t *testing.T) {
	// 256-color cyan: ESC[38;5;14m
	input := "\033[38;5;14mtext\033[0m"
	colors := parseANSIColors(input)

	found := false
	for _, c := range colors {
		if c == "14" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Expected to find color 14, got: %v", colors)
	}
}

func TestParseANSIColors_TrueColor(t *testing.T) {
	// True color RGB: ESC[38;2;6;182;212m (cyan-ish)
	input := "\033[38;2;6;182;212mtext\033[0m"
	colors := parseANSIColors(input)

	found := false
	for _, c := range colors {
		if c == "#06B6D4" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Expected to find #06B6D4, got: %v", colors)
	}
}

func TestExtractPalette_Fallback(t *testing.T) {
	// When starship isn't available, should return defaults
	palette := ExtractPalette("")

	// Should have valid lipgloss colors
	if palette.Primary == "" {
		t.Error("Primary color should not be empty")
	}
	if palette.Success == "" {
		t.Error("Success color should not be empty")
	}
	if palette.Error == "" {
		t.Error("Error color should not be empty")
	}
}

func TestPalette_Styles(t *testing.T) {
	palette := DefaultPalette()

	// Styles should be usable
	style := palette.PrimaryStyle()
	rendered := style.Render("test")
	if rendered == "" {
		t.Error("PrimaryStyle should render text")
	}
}

// A prompt shows no warning color, so the palette carries one of its own.
func TestDefaultPaletteHasAWarningColor(t *testing.T) {
	if DefaultPalette().Warning == "" {
		t.Error("no warning color")
	}
	if got := ExtractPaletteFromOutputs("", "").Warning; got != DefaultPalette().Warning {
		t.Errorf("an extracted palette's warning = %q, want the default", got)
	}
}

// Secondary text is the terminal's own gray, so it follows the terminal's
// theme as the rest of hash's gray does, instead of a fixed color that
// matches one theme.
func TestDefaultPaletteDimIsTheTerminalsGray(t *testing.T) {
	if got := DefaultPalette().Dim; got != "8" {
		t.Errorf("Dim = %q, want the terminal's bright black (8)", got)
	}
	if got := ExtractPaletteFromOutputs("", "").Dim; got != "8" {
		t.Errorf("an extracted palette's Dim = %q", got)
	}
}
