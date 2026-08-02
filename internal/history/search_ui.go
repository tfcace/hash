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

	query            string
	results          []Command
	agentResults     []AgentInteraction
	agentResultsMode bool
	totalResults     int
	selected         int
	scrollOffset     int
	width            int
	height           int
	debounceID       int
	statusMessage    string
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
	return finalUI.selectedText(), nil
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
		if msg.agentResultsMode != ui.agentResultsMode {
			return ui, nil
		}
		ui.results = msg.results
		ui.agentResults = msg.agentResults
		ui.totalResults = msg.total
		if ui.resultCount() > 0 && ui.selected < 0 {
			ui.selected = 0
		}
		if ui.selected >= ui.resultCount() {
			ui.selected = ui.resultCount() - 1
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
		if ui.agentResultsMode && ui.selectedText() == "" {
			ui.statusMessage = "Only command results can be inserted"
			return ui, nil
		}
		return ui, tea.Quit

	case "up", "ctrl+p":
		if ui.selected > 0 {
			ui.selected--
			ui.adjustScroll()
		}

	case "down", "ctrl+n":
		if ui.selected < ui.resultCount()-1 {
			ui.selected++
			ui.adjustScroll()
		}

	case "tab":
		return ui, ui.selectAgentResults()

	case "shift+tab":
		return ui, ui.selectCommands()

	case "ctrl+y":
		cmd := ui.copyCommand()
		return ui, cmd

	case "ctrl+r":
		// Ctrl+R opens the picker. Once it is open, the visible tabs own
		// navigation so repeated Ctrl+R never changes the active result set.
		return ui, nil

	case "ctrl+o":
		cmd := ui.copyOutput()
		return ui, cmd

	default:
		cmd := ui.handleQueryEdit(msg)
		return ui, cmd
	}

	return ui, nil
}

func (ui *SearchUI) handleQueryEdit(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "backspace":
		if ui.query != "" {
			ui.query = ui.query[:len(ui.query)-1]
			return ui.resetAndSearch()
		}
	case "space":
		ui.query += " "
		return ui.resetAndSearch()
	default:
		if len(msg.String()) == 1 && msg.String()[0] >= 32 {
			ui.query += msg.String()
			return ui.resetAndSearch()
		}
	}
	return nil
}

func (ui *SearchUI) resetAndSearch() tea.Cmd {
	ui.selected = 0
	ui.scrollOffset = 0
	ui.debounceID++
	return ui.debounceSearch(ui.debounceID)
}

func (ui *SearchUI) selectAgentResults() tea.Cmd {
	if ui.agentResultsMode {
		return nil
	}
	ui.agentResultsMode = true
	return ui.resetAndSearch()
}

func (ui *SearchUI) selectCommands() tea.Cmd {
	if !ui.agentResultsMode {
		return nil
	}
	ui.agentResultsMode = false
	return ui.resetAndSearch()
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
	borderColor := lipgloss.Color(ui.palette.Primary)
	selectedStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ui.palette.Success)).
		Bold(true)
	normalStyle := lipgloss.NewStyle()
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(ui.palette.Dim))
	errorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(ui.palette.Error))

	headerBorder := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Padding(0, 1).
		Width(ui.width - 2)

	headerText := "Search Commands"
	if ui.agentResultsMode {
		headerText = "Search Agent results"
	}
	headerLabel := lipgloss.NewStyle().
		Foreground(borderColor).
		Bold(true).
		Render(headerText)

	queryDisplay := ui.query
	if queryDisplay == "" {
		queryDisplay = dimStyle.Render("type to search...")
	}

	header := fmt.Sprintf("%s %s", headerLabel, queryDisplay)
	b.WriteString(headerBorder.Render(header))
	b.WriteString("\n")

	activeTabStyle := lipgloss.NewStyle().
		Foreground(borderColor).
		Bold(true)
	tabStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(ui.palette.Dim))
	commandsTab := tabStyle.Render("[Commands]")
	agentResultsTab := tabStyle.Render("[Agent results]")
	if ui.agentResultsMode {
		agentResultsTab = activeTabStyle.Render("[Agent results]")
	} else {
		commandsTab = activeTabStyle.Render("[Commands]")
	}
	b.WriteString("  ")
	b.WriteString(commandsTab)
	b.WriteString(" ")
	b.WriteString(agentResultsTab)
	b.WriteString("\n")

	if ui.resultCount() == 0 {
		if ui.query != "" {
			b.WriteString(dimStyle.Render("  No matches"))
		} else if ui.agentResultsMode {
			b.WriteString(dimStyle.Render("  No saved agent results"))
		} else {
			b.WriteString(dimStyle.Render("  No history"))
		}
		b.WriteString("\n")
	} else {
		visibleEnd := ui.scrollOffset + maxVisibleResults
		if visibleEnd > ui.resultCount() {
			visibleEnd = ui.resultCount()
		}

		for i := ui.scrollOffset; i < visibleEnd; i++ {
			line := ""
			if ui.agentResultsMode {
				line = ui.formatAgentResultLine(ui.agentResults[i], i, selectedStyle, normalStyle, dimStyle)
			} else {
				line = ui.formatResultLine(ui.results[i], i, selectedStyle, normalStyle, dimStyle, errorStyle)
			}
			b.WriteString(line)
			b.WriteString("\n")
		}
	}

	if ui.selected >= 0 && ui.selected < ui.resultCount() {
		b.WriteString("\n")
		b.WriteString(dimStyle.Render("─── Preview ───"))
		b.WriteString("\n")
		if ui.agentResultsMode {
			result := ui.agentResults[ui.selected]
			b.WriteString(dimStyle.Render("Prompt: "))
			b.WriteString(normalStyle.Render(result.Prompt))
			b.WriteString("\n")
			b.WriteString(normalStyle.Render(result.Response))
			b.WriteString("\n")
			accepted := "not accepted"
			if result.Accepted {
				accepted = "accepted"
			}
			meta := fmt.Sprintf("%s │ %s │ %s", result.ResponseKind, accepted, result.Timestamp.Format("2006-01-02 15:04"))
			if result.Agent != "" {
				meta += fmt.Sprintf(" │ %s", result.Agent)
			}
			b.WriteString(dimStyle.Render(meta))
		} else {
			cmd := ui.results[ui.selected]
			b.WriteString(normalStyle.Render(cmd.Command))
			b.WriteString("\n")
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
		}
		b.WriteString("\n")
	}

	if ui.resultCount() > 0 {
		countStr := fmt.Sprintf("result %d of %d", ui.selected+1, ui.totalResults)
		padding := ui.width - len(countStr) - 2
		if padding > 0 {
			b.WriteString(strings.Repeat(" ", padding))
		}
		b.WriteString(dimStyle.Render(countStr))
		b.WriteString("\n")
	}

	if ui.statusMessage != "" {
		b.WriteString(selectedStyle.Render(ui.statusMessage))
		b.WriteString("\n")
	}

	help := "  tab agent results  shift+tab commands  ↑/↓ navigate  enter select  ctrl+y copy cmd  ctrl+o copy output  esc cancel"
	if ui.agentResultsMode {
		help = "  tab agent results  shift+tab commands  ↑/↓ navigate  enter insert command  ctrl+y copy response  esc cancel"
	}
	b.WriteString(dimStyle.Render(help))

	v := tea.NewView(b.String())
	// Disable bracketed paste to prevent DECRQM queries on shutdown.
	// Bubbletea queries modes 2026/2027 at exit; the terminal responses
	// arrive on stdin after bubbletea's input reader closes, leaking
	// characters like [?2027;1$y into the next editor session.
	v.DisableBracketedPasteMode = true
	return v
}

func (ui *SearchUI) resultCount() int {
	if ui.agentResultsMode {
		return len(ui.agentResults)
	}
	return len(ui.results)
}

// selectedText returns only content safe to insert into the command editor.
// Explanations and legacy unknown results remain preview/copy-only.
func (ui *SearchUI) selectedText() string {
	if ui.selected < 0 || ui.selected >= ui.resultCount() {
		return ""
	}
	if ui.agentResultsMode {
		result := ui.agentResults[ui.selected]
		if result.ResponseKind != AgentResponseKindCommand {
			return ""
		}
		return result.Response
	}
	return ui.results[ui.selected].Command
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

func (ui *SearchUI) formatAgentResultLine(result AgentInteraction, idx int, selected, normal, dim lipgloss.Style) string {
	indicator := "  "
	if idx == ui.selected {
		indicator = selected.Render("> ")
	}
	text := result.Prompt
	if text == "" {
		text = result.Response
	}
	agentName := result.Agent
	if agentName == "" {
		agentName = "unknown agent"
	}
	acceptance := "not accepted"
	if result.Accepted {
		acceptance = "accepted"
	}
	meta := fmt.Sprintf("%s · %s · %s · %s", result.ResponseKind, agentName, acceptance, ui.formatTimestamp(result.Timestamp))

	// Reserve room for the complete metadata so no field silently drops off
	// narrow terminals. The prompt gives way first and keeps the row readable.
	maxWidth := ui.width - len(meta) - 6
	if maxWidth < 3 {
		maxWidth = 3
	}
	if len(text) > maxWidth && maxWidth > 3 {
		text = text[:maxWidth-3] + "..."
	}
	style := normal
	if idx == ui.selected {
		style = selected
	}
	padding := ui.width - 2 - len(text) - len(meta) - 2
	if padding < 1 {
		padding = 1
	}
	return indicator + style.Render(text) + strings.Repeat(" ", padding) + dim.Render(meta)
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
	results          []Command
	agentResults     []AgentInteraction
	agentResultsMode bool
	total            int
}

type debounceMsg struct {
	id int
}

type clearStatusMsg struct{}

func (ui *SearchUI) search() tea.Cmd {
	return func() tea.Msg {
		if ui.agentResultsMode {
			results, _ := ui.store.GetAgentInteractions(ui.query, 100)
			return searchResultMsg{agentResults: results, agentResultsMode: true, total: len(results)}
		}
		results, _ := ui.store.Search(SearchOptions{
			Query: ui.query,
			Limit: 100, // Get more for scrolling
		})
		return searchResultMsg{results: results, agentResultsMode: false, total: len(results)}
	}
}

func (ui *SearchUI) searchNow() {
	if ui.agentResultsMode {
		results, _ := ui.store.GetAgentInteractions(ui.query, 100)
		ui.agentResults = results
		ui.totalResults = len(results)
		if len(results) > 0 {
			ui.selected = 0
		} else {
			ui.selected = -1
		}
		return
	}
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
	if ui.selected < 0 || ui.selected >= ui.resultCount() {
		ui.statusMessage = "No selection"
		return nil
	}

	cmd := ui.selectedText()
	if ui.agentResultsMode && cmd == "" {
		cmd = ui.agentResults[ui.selected].Response
	}
	if err := ui.copyToClipboard(cmd); err != nil {
		ui.statusMessage = "Clipboard error"
	} else {
		ui.statusMessage = "Copied!"
	}
	return nil
}

func (ui *SearchUI) copyOutput() tea.Cmd {
	if ui.agentResultsMode {
		return ui.copyCommand()
	}
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
