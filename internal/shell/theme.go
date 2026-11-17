package shell

import (
	"os"
	"sync"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"github.com/tfcace/hash/internal/markdown"
	"github.com/tfcace/hash/internal/prompt"
)

// markdownTheme is the palette as the colors a reply is drawn with, fitted
// to the terminal.
func markdownTheme(p prompt.Palette) markdown.Theme {
	return markdown.Theme{
		Accent:  fgCode(p.Primary),
		Success: fgCode(p.Success),
		Error:   fgCode(p.Error),
		Warning: fgCode(p.Warning),
	}
}

// colorProfile is how much color the terminal on stdout can show, detected
// once from the environment (NO_COLOR, COLORTERM, TERM, tmux). A variable so
// tests can set one.
var colorProfile = sync.OnceValue(func() colorprofile.Profile {
	return colorprofile.Detect(os.Stdout, os.Environ())
})

// fgCode is the escape sequence that sets the foreground to a palette color
// ("#RRGGBB" or an ANSI index), as close as the terminal can show it: 24-bit,
// 256 colors, 16 colors, or "" when it shows none or the color is unreadable.
func fgCode(paletteColor string) string {
	c := lipgloss.Color(paletteColor)
	if _, unreadable := c.(lipgloss.NoColor); unreadable {
		return ""
	}
	fitted := colorProfile().Convert(c)
	if fitted == nil {
		return ""
	}
	return ansi.NewStyle().ForegroundColor(fitted).String()
}
