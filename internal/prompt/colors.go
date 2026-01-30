package prompt

import (
	"bytes"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Palette holds colors extracted from the prompt for consistent UI theming.
type Palette struct {
	Primary   string // Main accent color (from directory chip background, typically colorful)
	Secondary string // Secondary foreground color
	Success   string // Success indicator (prompt char on exit 0, typically green)
	Error     string // Error indicator (prompt char on exit 1, typically red)
	Dim       string // Dimmed/secondary text (gray)
	InputBg   string // Background color for command input highlight
}

// DefaultPalette returns fallback colors matching common starship themes.
func DefaultPalette() Palette {
	return Palette{
		Primary: "#06B6D4", // Cyan
		Success: "#22C55E", // Green
		Error:   "#EF4444", // Red
		Dim:     "#6B7280", // Gray
		InputBg: "#1a1f2c", // Subtle dark background for input highlight
	}
}

// PrimaryStyle returns a lipgloss style with the primary color.
func (p Palette) PrimaryStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(p.Primary))
}

// SuccessStyle returns a lipgloss style with the success color.
func (p Palette) SuccessStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(p.Success))
}

// ErrorStyle returns a lipgloss style with the error color.
func (p Palette) ErrorStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(p.Error))
}

// DimStyle returns a lipgloss style with the dim color.
func (p Palette) DimStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(p.Dim))
}

// ExtractPalette extracts a color palette from starship prompt output.
// If starshipPath is empty or extraction fails, returns DefaultPalette.
func ExtractPalette(starshipPath string) Palette {
	if starshipPath == "" {
		return DefaultPalette()
	}

	palette := DefaultPalette()

	// Get prompt with exit code 0 (success)
	successPrompt := runStarship(starshipPath, 0)
	if successPrompt != "" {
		colors := parseANSIColors(successPrompt)
		if len(colors) > 0 {
			palette.Secondary = colors[0]
		}
		// Last color before reset is usually prompt char
		if len(colors) > 1 {
			palette.Success = colors[len(colors)-1]
		}

		// Extract background colors
		bgColors := parseANSIBgColors(successPrompt)

		// Find first colorful background as Primary (the chip accent color)
		for _, bg := range bgColors {
			if isVisibleColor(bg) {
				palette.Primary = bg
				break
			}
		}

		// Find a darker background for input highlight
		for _, bg := range bgColors {
			if isGoodInputBg(bg) {
				palette.InputBg = bg
				break
			}
		}
	}

	// Get prompt with exit code 1 (error)
	errorPrompt := runStarship(starshipPath, 1)
	if errorPrompt != "" {
		colors := parseANSIColors(errorPrompt)
		// Last color is usually the error prompt char
		if len(colors) > 0 {
			palette.Error = colors[len(colors)-1]
		}
	}

	return palette
}

// runStarship executes starship prompt with the given exit code.
func runStarship(path string, exitCode int) string {
	cmd := exec.Command(path, "prompt", "--status", strconv.Itoa(exitCode))
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return ""
	}
	return out.String()
}

// ANSI escape code patterns
var (
	// Basic colors: ESC[31m (foreground), ESC[41m (background)
	basicColorRe = regexp.MustCompile(`\033\[([34][0-7])m`)
	// 256 colors: ESC[38;5;Nm or ESC[48;5;Nm
	color256Re = regexp.MustCompile(`\033\[([34])8;5;(\d+)m`)
	// True color: ESC[38;2;R;G;B or ESC[48;2;R;G;B (may be followed by more params or m)
	// Use non-greedy match to handle combined sequences like ESC[48;2;R;G;B;38;2;R;G;Bm
	trueColorRe = regexp.MustCompile(`([34])8;2;(\d+);(\d+);(\d+)`)
)

// parseANSIColors extracts color values from ANSI escape sequences.
// Returns colors as strings suitable for lipgloss.Color().
func parseANSIColors(s string) []string {
	var colors []string
	seen := make(map[string]bool)

	// Extract true colors first (most specific)
	for _, match := range trueColorRe.FindAllStringSubmatch(s, -1) {
		if match[1] != "3" { // skip non-foreground
			continue
		}
		r, _ := strconv.Atoi(match[2])
		g, _ := strconv.Atoi(match[3])
		b, _ := strconv.Atoi(match[4])
		hex := fmt.Sprintf("#%02X%02X%02X", r, g, b)
		if !seen[hex] {
			colors = append(colors, hex)
			seen[hex] = true
		}
	}

	// Extract 256 colors
	for _, match := range color256Re.FindAllStringSubmatch(s, -1) {
		if match[1] == "3" { // foreground only
			color := match[2]
			if !seen[color] {
				colors = append(colors, color)
				seen[color] = true
			}
		}
	}

	// Extract basic colors
	for _, match := range basicColorRe.FindAllStringSubmatch(s, -1) {
		code := match[1]
		if strings.HasPrefix(code, "3") { // foreground only
			color := code[1:] // just the color number
			if !seen[color] {
				colors = append(colors, color)
				seen[color] = true
			}
		}
	}

	return colors
}

// parseANSIBgColors extracts background color values from ANSI escape sequences.
func parseANSIBgColors(s string) []string {
	var colors []string
	seen := make(map[string]bool)

	// Extract true color backgrounds (most specific)
	for _, match := range trueColorRe.FindAllStringSubmatch(s, -1) {
		if match[1] != "4" { // skip non-background
			continue
		}
		r, _ := strconv.Atoi(match[2])
		g, _ := strconv.Atoi(match[3])
		b, _ := strconv.Atoi(match[4])
		hex := fmt.Sprintf("#%02X%02X%02X", r, g, b)
		if !seen[hex] {
			colors = append(colors, hex)
			seen[hex] = true
		}
	}

	// Extract 256 color backgrounds
	for _, match := range color256Re.FindAllStringSubmatch(s, -1) {
		if match[1] == "4" { // background
			// Convert 256 color to approximate hex
			colorNum, _ := strconv.Atoi(match[2])
			hex := color256ToHex(colorNum)
			if !seen[hex] {
				colors = append(colors, hex)
				seen[hex] = true
			}
		}
	}

	return colors
}

// isGoodInputBg checks if a color is suitable for input background.
// Rejects colors that are too dark (invisible) or too bright (distracting).
func isGoodInputBg(hex string) bool {
	if len(hex) != 7 || hex[0] != '#' {
		return false
	}
	var r, g, b int
	fmt.Sscanf(hex[1:], "%02x%02x%02x", &r, &g, &b) //nolint:errcheck // hex format already validated
	sum := r + g + b
	// Skip very dark colors (sum < 100) and very bright (sum > 400)
	return sum >= 100 && sum <= 400
}

// isVisibleColor checks if a color is visible (not too dark or too light).
func isVisibleColor(hex string) bool {
	if len(hex) != 7 || hex[0] != '#' {
		return false
	}
	var r, g, b int
	fmt.Sscanf(hex[1:], "%02x%02x%02x", &r, &g, &b) //nolint:errcheck // hex format already validated
	sum := r + g + b
	// Skip very dark colors (sum < 150) - need to be visible on dark terminals
	return sum >= 150
}

// color256ToHex converts a 256-color code to approximate hex.
func color256ToHex(n int) string {
	if n < 16 {
		// Basic colors - return reasonable defaults
		basics := []string{
			"#000000", "#800000", "#008000", "#808000",
			"#000080", "#800080", "#008080", "#c0c0c0",
			"#808080", "#ff0000", "#00ff00", "#ffff00",
			"#0000ff", "#ff00ff", "#00ffff", "#ffffff",
		}
		return basics[n]
	}
	if n < 232 {
		// 216 color cube
		n -= 16
		r := (n / 36) * 51
		g := ((n % 36) / 6) * 51
		b := (n % 6) * 51
		return fmt.Sprintf("#%02X%02X%02X", r, g, b)
	}
	// Grayscale
	gray := (n-232)*10 + 8
	return fmt.Sprintf("#%02X%02X%02X", gray, gray, gray)
}
