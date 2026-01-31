package prompt

import "testing"

// TestColor256ToHex_BasicColors tests the basic 16 ANSI colors (0-15).
func TestColor256ToHex_BasicColors(t *testing.T) {
	tests := []struct {
		input int
		want  string
	}{
		{0, "#000000"},  // black
		{1, "#800000"},  // red
		{2, "#008000"},  // green
		{3, "#808000"},  // yellow
		{4, "#000080"},  // blue
		{5, "#800080"},  // magenta
		{6, "#008080"},  // cyan
		{7, "#c0c0c0"},  // white
		{8, "#808080"},  // bright black (gray)
		{9, "#ff0000"},  // bright red
		{10, "#00ff00"}, // bright green
		{11, "#ffff00"}, // bright yellow
		{12, "#0000ff"}, // bright blue
		{13, "#ff00ff"}, // bright magenta
		{14, "#00ffff"}, // bright cyan
		{15, "#ffffff"}, // bright white
	}

	for _, tt := range tests {
		got := color256ToHex(tt.input)
		if got != tt.want {
			t.Errorf("color256ToHex(%d) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// TestColor256ToHex_ColorCube tests the 216-color cube (16-231).
// Formula: r = (n/36)*51, g = ((n%36)/6)*51, b = (n%6)*51
func TestColor256ToHex_ColorCube(t *testing.T) {
	tests := []struct {
		input int
		want  string
	}{
		{16, "#000000"},  // start of cube (0,0,0)
		{17, "#000033"},  // (0,0,1) = 0,0,51
		{21, "#0000FF"},  // (0,0,5) - max blue
		{196, "#FF0000"}, // (5,0,0) - max red
		{46, "#00FF00"},  // (0,5,0) - max green
		{231, "#FFFFFF"}, // (5,5,5) - max all
		{124, "#990000"}, // (3,0,0) = 153,0,0
		{82, "#33FF00"},  // (1,5,0) = 51,255,0
	}

	for _, tt := range tests {
		got := color256ToHex(tt.input)
		if got != tt.want {
			t.Errorf("color256ToHex(%d) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// TestColor256ToHex_Grayscale tests grayscale colors (232-255).
func TestColor256ToHex_Grayscale(t *testing.T) {
	tests := []struct {
		input int
		want  string
	}{
		{232, "#080808"}, // darkest gray
		{233, "#121212"}, // second darkest
		{243, "#767676"}, // mid gray
		{255, "#EEEEEE"}, // lightest gray
	}

	for _, tt := range tests {
		got := color256ToHex(tt.input)
		if got != tt.want {
			t.Errorf("color256ToHex(%d) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// TestIsVisibleColor tests the visibility check function.
func TestIsVisibleColor(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"black is not visible", "#000000", false},
		{"very dark is not visible", "#101010", false},
		{"threshold edge low", "#323200", false}, // sum = 100 < 150
		{"threshold edge high", "#323232", true}, // sum = 150 = 150
		{"bright red is visible", "#FF0000", true},
		{"bright green is visible", "#00FF00", true},
		{"white is visible", "#FFFFFF", true},
		{"cyan is visible", "#06B6D4", true},
		{"invalid format short", "#FFF", false},
		{"invalid format no hash", "FFFFFF", false},
		{"invalid format empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isVisibleColor(tt.input)
			if got != tt.want {
				t.Errorf("isVisibleColor(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// TestIsGoodInputBg tests the input background suitability check.
func TestIsGoodInputBg(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"too dark", "#000000", false},        // sum = 0
		{"too dark edge", "#212121", false},   // sum = 99 < 100
		{"good dark edge", "#222222", true},   // sum = 102 >= 100
		{"good mid", "#505050", true},         // sum = 240
		{"good light edge", "#858585", true},  // sum = 399 <= 400
		{"too bright edge", "#868686", false}, // sum = 402 > 400
		{"too bright", "#FFFFFF", false},      // sum = 765
		{"good subtle dark", "#1a1f2c", true}, // sum = 26+31+44 = 101
		{"invalid format", "#FFF", false},
		{"invalid no hash", "1a1f2c", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isGoodInputBg(tt.input)
			if got != tt.want {
				t.Errorf("isGoodInputBg(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// TestParseANSIBgColors_TrueColor tests background true color extraction.
func TestParseANSIBgColors_TrueColor(t *testing.T) {
	// True color background: ESC[48;2;R;G;Bm
	input := "\033[48;2;26;31;44mtext\033[0m"
	colors := parseANSIBgColors(input)

	if len(colors) == 0 {
		t.Fatal("Expected to find at least one background color")
	}

	found := false
	for _, c := range colors {
		if c == "#1A1F2C" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Expected to find #1A1F2C, got: %v", colors)
	}
}

// TestParseANSIBgColors_256Color tests background 256-color extraction.
func TestParseANSIBgColors_256Color(t *testing.T) {
	// 256-color background: ESC[48;5;232m (darkest gray)
	input := "\033[48;5;232mtext\033[0m"
	colors := parseANSIBgColors(input)

	if len(colors) == 0 {
		t.Fatal("Expected to find at least one background color")
	}

	// Color 232 should convert to #080808
	found := false
	for _, c := range colors {
		if c == "#080808" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Expected to find #080808 (grayscale 232), got: %v", colors)
	}
}

// TestParseANSIBgColors_SkipsForeground verifies foreground colors are ignored.
func TestParseANSIBgColors_SkipsForeground(t *testing.T) {
	// Foreground true color (should be skipped): ESC[38;2;R;G;Bm
	input := "\033[38;2;255;0;0mred text\033[0m"
	colors := parseANSIBgColors(input)

	if len(colors) != 0 {
		t.Errorf("Expected no background colors from foreground sequence, got: %v", colors)
	}
}

// TestParseANSIBgColors_Mixed tests mixed foreground and background.
func TestParseANSIBgColors_Mixed(t *testing.T) {
	// Combined sequence with both fg and bg
	input := "\033[48;2;26;31;44;38;2;255;255;255mtext\033[0m"
	colors := parseANSIBgColors(input)

	// Should only extract background
	if len(colors) != 1 {
		t.Errorf("Expected exactly 1 background color, got: %v", colors)
	}
	if len(colors) > 0 && colors[0] != "#1A1F2C" {
		t.Errorf("Expected #1A1F2C, got: %v", colors[0])
	}
}

// TestParseANSIBgColors_Deduplication tests that duplicate colors are removed.
func TestParseANSIBgColors_Deduplication(t *testing.T) {
	// Same background color repeated
	input := "\033[48;2;26;31;44mfirst\033[0m \033[48;2;26;31;44msecond\033[0m"
	colors := parseANSIBgColors(input)

	if len(colors) != 1 {
		t.Errorf("Expected 1 unique color after deduplication, got %d: %v", len(colors), colors)
	}
}

// TestStripClearSequences tests removal of clear escape sequences.
func TestStripClearSequences(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"no sequences", "hello world", "hello world"},
		{"clear screen", "\033[2Jhello", "hello"},
		{"clear to end of screen", "\033[Jhello", "hello"},
		{"clear to end of screen explicit", "\033[0Jhello", "hello"},
		{"clear to start of screen", "\033[1Jhello", "hello"},
		{"clear entire screen", "\033[2Jhello", "hello"},
		{"clear saved lines", "\033[3Jhello", "hello"},
		{"clear line", "\033[Khello", "hello"},
		{"clear to end of line", "\033[0Khello", "hello"},
		{"clear to start of line", "\033[1Khello", "hello"},
		{"clear entire line", "\033[2Khello", "hello"},
		{"multiple sequences", "\033[2J\033[Khello\033[2Kworld", "helloworld"},
		{"mixed with colors", "\033[32m\033[2Jhello\033[0m", "\033[32mhello\033[0m"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripClearSequences(tt.input)
			if got != tt.want {
				t.Errorf("stripClearSequences(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// TestDefaultPalette verifies default palette values.
func TestDefaultPalette(t *testing.T) {
	p := DefaultPalette()

	if p.Primary == "" {
		t.Error("Primary color should not be empty")
	}
	if p.Success == "" {
		t.Error("Success color should not be empty")
	}
	if p.Error == "" {
		t.Error("Error color should not be empty")
	}
	if p.Dim == "" {
		t.Error("Dim color should not be empty")
	}
	if p.InputBg == "" {
		t.Error("InputBg color should not be empty")
	}

	// Verify default values are valid hex
	if !isVisibleColor(p.Primary) {
		t.Errorf("Primary %q should be visible", p.Primary)
	}
	if !isVisibleColor(p.Success) {
		t.Errorf("Success %q should be visible", p.Success)
	}
	if !isVisibleColor(p.Error) {
		t.Errorf("Error %q should be visible", p.Error)
	}
}

// TestPalette_AllStyles verifies all style methods work.
func TestPalette_AllStyles(t *testing.T) {
	p := DefaultPalette()

	t.Run("PrimaryStyle", func(t *testing.T) {
		rendered := p.PrimaryStyle().Render("test")
		if rendered == "" {
			t.Error("PrimaryStyle should render text")
		}
	})
	t.Run("SuccessStyle", func(t *testing.T) {
		rendered := p.SuccessStyle().Render("test")
		if rendered == "" {
			t.Error("SuccessStyle should render text")
		}
	})
	t.Run("ErrorStyle", func(t *testing.T) {
		rendered := p.ErrorStyle().Render("test")
		if rendered == "" {
			t.Error("ErrorStyle should render text")
		}
	})
	t.Run("DimStyle", func(t *testing.T) {
		rendered := p.DimStyle().Render("test")
		if rendered == "" {
			t.Error("DimStyle should render text")
		}
	})
}
