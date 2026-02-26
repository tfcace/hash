package history

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/tfcace/hash/internal/clipboard"
	"github.com/tfcace/hash/internal/prompt"
	sysClipboard "golang.design/x/clipboard"
)

const (
	maxVisibleResults = 10
	debounceDelay     = 50 * time.Millisecond
	statusDuration    = 1500 * time.Millisecond
)

// SearchUI provides Ctrl+R style interactive search.
type SearchUI struct {
	store        *Store
	clipboardBuf *clipboard.Buffer
	palette      prompt.Palette

	query         string
	results       []Command
	totalResults  int
	selected      int
	scrollOffset  int
	width         int
	height        int
	debounceID    int
	statusMessage string
}

// NewSearchUI creates a new search UI with the given color palette.
func NewSearchUI(store *Store, palette prompt.Palette) *SearchUI {
	return &SearchUI{
		store:   store,
		palette: palette,
		width:   80,
		height:  20,
	}
}

// SetClipboard sets the clipboard buffer for output cross-referencing.
func (ui *SearchUI) SetClipboard(buf *clipboard.Buffer) {
	ui.clipboardBuf = buf
}

// Run starts the interactive search and returns the selected command.
func (ui *SearchUI) Run() (string, error) {
	// No alt-screen: inline rendering
	p := tea.NewProgram(ui)
	model, err := p.Run()
	if err != nil {
		return "", err
	}

	finalUI := model.(*SearchUI)
	if finalUI.selected >= 0 && finalUI.selected < len(finalUI.results) {
		return finalUI.results[finalUI.selected].Command, nil
	}

	return "", nil
}

// Init implements tea.Model.
func (ui *SearchUI) Init() tea.Cmd {
	ui.searchNow()
	return nil
}

// Update implements tea.Model.
func (ui *SearchUI) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		return ui.handleKey(msg)

	case tea.WindowSizeMsg:
		ui.width = msg.Width
		ui.height = msg.Height

	case searchResultMsg:
		ui.results = msg.results
		ui.totalResults = msg.total
		if len(ui.results) > 0 && ui.selected < 0 {
			ui.selected = 0
		}
		if ui.selected >= len(ui.results) {
			ui.selected = len(ui.results) - 1
		}

	case debounceMsg:
		if msg.id == ui.debounceID {
			cmd := ui.search()
			return ui, cmd
		}

	case clearStatusMsg:
		ui.statusMessage = ""
	}

	return ui, nil
}

func (ui *SearchUI) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// Clear status message on any keypress
	ui.statusMessage = ""

	switch msg.String() {
	case "ctrl+c", "esc":
		ui.selected = -1
		return ui, tea.Quit

	case "enter":
		return ui, tea.Quit

	case "up", "ctrl+p":
		if ui.selected > 0 {
			ui.selected--
			ui.adjustScroll()
		}

	case "down", "ctrl+n", "tab":
		if ui.selected < len(ui.results)-1 {
			ui.selected++
			ui.adjustScroll()
		}

	case "shift+tab":
		if ui.selected > 0 {
			ui.selected--
			ui.adjustScroll()
		}

	case "backspace":
		if ui.query != "" {
			ui.query = ui.query[:len(ui.query)-1]
			ui.selected = 0
			ui.scrollOffset = 0
			ui.debounceID++
			cmd := ui.debounceSearch(ui.debounceID)
			return ui, cmd
		}

	case "ctrl+y":
		cmd := ui.copyCommand()
		return ui, cmd

	case "ctrl+o":
		cmd := ui.copyOutput()
		return ui, cmd

	default:
		if len(msg.String()) == 1 && msg.String()[0] >= 32 {
			ui.query += msg.String()
			ui.selected = 0
			ui.scrollOffset = 0
			ui.debounceID++
			cmd := ui.debounceSearch(ui.debounceID)
			return ui, cmd
		}
	}

	return ui, nil
}

func (ui *SearchUI) adjustScroll() {
	// Keep selected item visible
	if ui.selected < ui.scrollOffset {
		ui.scrollOffset = ui.selected
	}
	if ui.selected >= ui.scrollOffset+maxVisibleResults {
		ui.scrollOffset = ui.selected - maxVisibleResults + 1
	}
}

// View implements tea.Model.
func (ui *SearchUI) View() tea.View {
	var b strings.Builder

	// Styles
	borderColor := lipgloss.Color(ui.palette.Primary)
	selectedStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ui.palette.Success)).
		Bold(true)
	normalStyle := lipgloss.NewStyle()
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(ui.palette.Dim))
	errorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(ui.palette.Error))

	// Header box
	headerBorder := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Padding(0, 1).
		Width(ui.width - 2)

	headerLabel := lipgloss.NewStyle().
		Foreground(borderColor).
		Bold(true).
		Render("Search")

	queryDisplay := ui.query
	if queryDisplay == "" {
		queryDisplay = dimStyle.Render("type to search...")
	}

	header := fmt.Sprintf("%s %s", headerLabel, queryDisplay)
	b.WriteString(headerBorder.Render(header))
	b.WriteString("\n")

	// Results
	if len(ui.results) == 0 {
		if ui.query != "" {
			b.WriteString(dimStyle.Render("  No matches"))
		} else {
			b.WriteString(dimStyle.Render("  No history"))
		}
		b.WriteString("\n")
	} else {
		visibleEnd := ui.scrollOffset + maxVisibleResults
		if visibleEnd > len(ui.results) {
			visibleEnd = len(ui.results)
		}

		for i := ui.scrollOffset; i < visibleEnd; i++ {
			cmd := ui.results[i]
			line := ui.formatResultLine(cmd, i, selectedStyle, normalStyle, dimStyle, errorStyle)
			b.WriteString(line)
			b.WriteString("\n")
		}
	}

	// Preview pane for selected command
	if ui.selected >= 0 && ui.selected < len(ui.results) {
		cmd := ui.results[ui.selected]

		b.WriteString("\n")
		b.WriteString(dimStyle.Render("─── Preview ───"))
		b.WriteString("\n")

		// Full command (not truncated)
		b.WriteString(normalStyle.Render(cmd.Command))
		b.WriteString("\n")

		// Metadata line
		meta := fmt.Sprintf("%s │ %s", cmd.Timestamp.Format("2006-01-02 15:04"), cmd.Cwd)
		if cmd.GitBranch != "" {
			meta += fmt.Sprintf(" │ %s", cmd.GitBranch)
		}
		if cmd.ExitCode != 0 {
			meta += fmt.Sprintf(" │ ✗%d", cmd.ExitCode)
		} else {
			meta += " │ ✓"
		}
		b.WriteString(dimStyle.Render(meta))
		b.WriteString("\n")
	}

	// Result count (bottom right)
	if len(ui.results) > 0 {
		countStr := fmt.Sprintf("result %d of %d", ui.selected+1, ui.totalResults)
		padding := ui.width - len(countStr) - 2
		if padding > 0 {
			b.WriteString(strings.Repeat(" ", padding))
		}
		b.WriteString(dimStyle.Render(countStr))
		b.WriteString("\n")
	}

	// Status message (if any)
	if ui.statusMessage != "" {
		b.WriteString(selectedStyle.Render(ui.statusMessage))
		b.WriteString("\n")
	}

	// Help footer
	help := "  ↑/↓ navigate  enter select  ctrl+y copy cmd  ctrl+o copy output  esc cancel"
	b.WriteString(dimStyle.Render(help))

	return tea.NewView(b.String())
}

func (ui *SearchUI) formatResultLine(cmd Command, idx int, selected, normal, dim, errStyle lipgloss.Style) string {
	var b strings.Builder

	// Selection indicator
	if idx == ui.selected {
		b.WriteString(selected.Render("> "))
	} else {
		b.WriteString("  ")
	}

	// Command text (truncate if needed)
	maxCmdWidth := ui.width - 25 // Leave room for metadata
	cmdText := cmd.Command
	if len(cmdText) > maxCmdWidth && maxCmdWidth > 3 {
		cmdText = cmdText[:maxCmdWidth-3] + "..."
	}

	if idx == ui.selected {
		b.WriteString(selected.Render(cmdText))
	} else {
		b.WriteString(normal.Render(cmdText))
	}

	// Right-align metadata with indicator before it
	meta := ui.formatTimestamp(cmd.Timestamp)
	if cmd.GitBranch != "" {
		meta += " " + cmd.GitBranch
	}

	// Calculate padding (indicator goes between padding and metadata)
	currentLen := 2 + len(cmdText)
	padding := ui.width - currentLen - len(meta) - 4 // -2 for indicator, -2 for margins
	if padding > 0 {
		b.WriteString(strings.Repeat(" ", padding))
	}

	// Status indicator before metadata - ensures right edge alignment
	if cmd.ExitCode != 0 {
		b.WriteString(errStyle.Render("x "))
	} else {
		b.WriteString("  ")
	}

	b.WriteString(dim.Render(meta))

	return b.String()
}

func (ui *SearchUI) formatTimestamp(t time.Time) string {
	now := time.Now()
	diff := now.Sub(t)

	switch {
	case diff < time.Minute:
		return "just now"
	case diff < time.Hour:
		mins := int(diff.Minutes())
		if mins == 1 {
			return "1 min ago"
		}
		return fmt.Sprintf("%d min ago", mins)
	case diff < 24*time.Hour:
		hours := int(diff.Hours())
		if hours == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", hours)
	case diff < 48*time.Hour:
		return "yesterday"
	default:
		return t.Format("Jan 2")
	}
}

// Messages
type searchResultMsg struct {
	results []Command
	total   int
}

type debounceMsg struct {
	id int
}

type clearStatusMsg struct{}

func (ui *SearchUI) search() tea.Cmd {
	return func() tea.Msg {
		results, _ := ui.store.Search(SearchOptions{
			Query: ui.query,
			Limit: 100, // Get more for scrolling
		})
		return searchResultMsg{results: results, total: len(results)}
	}
}

func (ui *SearchUI) searchNow() {
	results, _ := ui.store.Search(SearchOptions{
		Query: ui.query,
		Limit: 100,
	})
	ui.results = results
	ui.totalResults = len(results)
	if len(ui.results) > 0 {
		ui.selected = 0
	} else {
		ui.selected = -1
	}
}

func (ui *SearchUI) debounceSearch(id int) tea.Cmd {
	return tea.Tick(debounceDelay, func(t time.Time) tea.Msg {
		return debounceMsg{id: id}
	})
}

func (ui *SearchUI) copyToClipboard(text string) error {
	if err := sysClipboard.Init(); err != nil {
		return err
	}
	sysClipboard.Write(sysClipboard.FmtText, []byte(text))
	return nil
}

func (ui *SearchUI) copyCommand() tea.Cmd {
	if ui.selected < 0 || ui.selected >= len(ui.results) {
		ui.statusMessage = "No selection"
		return nil
	}

	cmd := ui.results[ui.selected].Command
	if err := ui.copyToClipboard(cmd); err != nil {
		ui.statusMessage = "Clipboard error"
	} else {
		ui.statusMessage = "Copied!"
	}
	return nil
}

func (ui *SearchUI) copyOutput() tea.Cmd {
	if ui.selected < 0 || ui.selected >= len(ui.results) {
		ui.statusMessage = "No selection"
		return nil
	}

	cmd := ui.results[ui.selected].Command
	output := ui.findOutputForCommand(cmd)
	if output == "" {
		ui.statusMessage = "No output available"
	} else if err := ui.copyToClipboard(output); err != nil {
		ui.statusMessage = "Clipboard error"
	} else {
		ui.statusMessage = "Copied output!"
	}
	return nil
}

func (ui *SearchUI) findOutputForCommand(cmd string) string {
	if ui.clipboardBuf == nil {
		return ""
	}

	for i := 0; i < ui.clipboardBuf.Len(); i++ {
		if ui.clipboardBuf.GetCommand(i) == cmd {
			return ui.clipboardBuf.GetOutput(i)
		}
	}
	return ""
}

// filterCommands filters commands by query (for testing).
func filterCommands(commands []Command, query string) []Command {
	if query == "" {
		return commands
	}

	query = strings.ToLower(query)
	var filtered []Command
	for i := range commands {
		if strings.Contains(strings.ToLower(commands[i].Command), query) {
			filtered = append(filtered, commands[i])
		}
	}
	return filtered
}
