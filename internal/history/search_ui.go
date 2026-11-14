package history

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/mattn/go-runewidth"
	"github.com/tfcace/hash/internal/clipboard"
	"github.com/tfcace/hash/internal/prompt"
	"github.com/tfcace/hash/internal/termtext"
	sysClipboard "golang.design/x/clipboard"
)

const (
	maxVisibleResults = 10
	debounceDelay     = 50 * time.Millisecond
)

// searchOutcome is how the search ended, apart from what is selected.
type searchOutcome int

const (
	searchOpen     searchOutcome = iota // still running
	searchAccepted                      // Enter: run the selected command
	searchCanceled                      // Esc / Ctrl+C: choose nothing
)

// SearchUI provides Ctrl+R style interactive search.
//
// Every query edit bumps debounceID; results record the debounceID of the
// query that produced them (resultsID), so results for an older query are
// recognizable as stale. Stale rows stay on screen, dimmed, until the newer
// results land (no flicker while typing), but they are never highlighted,
// previewed or chosen: a key that acts on the selection (Enter, Up/Down,
// the copy keys) first brings the results up to the query (fresh).
type SearchUI struct {
	store        *Store
	clipboardBuf *clipboard.Buffer
	palette      prompt.Palette

	query         string
	results       []Command
	resultsID     int // debounceID of the query results came from
	totalResults  int
	selected      int // index into results; meaningful only when results is non-empty
	scrollOffset  int
	width         int
	height        int
	debounceID    int
	outcome       searchOutcome
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

// Run starts the interactive search and returns the selected command. It
// ends with ctx (the shell ending), with an error.
func (ui *SearchUI) Run(ctx context.Context) (string, error) {
	// No alt-screen: inline rendering
	p := tea.NewProgram(ui, tea.WithContext(ctx))
	model, err := p.Run()
	if err != nil {
		return "", err
	}

	return model.(*SearchUI).chosen(), nil
}

// chosen is the command the search ended on: the selection if accepted,
// nothing otherwise.
func (ui *SearchUI) chosen() string {
	if ui.outcome != searchAccepted || ui.selected < 0 || ui.selected >= len(ui.results) {
		return ""
	}
	return ui.results[ui.selected].Command
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
		// Only results for the typed query, and only if no key already
		// searched for it (and maybe moved the highlight since).
		if msg.id == ui.debounceID && ui.stale() {
			ui.setResults(msg.id, msg.results)
		}

	case debounceMsg:
		if msg.id == ui.debounceID {
			cmd := ui.search()
			return ui, cmd
		}
	}

	return ui, nil
}

func (ui *SearchUI) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// Clear status message on any keypress
	ui.statusMessage = ""

	switch msg.String() {
	case "ctrl+c", "esc":
		ui.outcome = searchCanceled
		return ui, tea.Quit

	case "enter":
		ui.fresh()
		ui.outcome = searchAccepted
		return ui, tea.Quit

	case "up", "ctrl+p", "shift+tab":
		ui.fresh()
		if ui.selected > 0 {
			ui.selected--
			ui.adjustScroll()
		}

	case "down", "ctrl+n", "tab":
		ui.fresh()
		if ui.selected < len(ui.results)-1 {
			ui.selected++
			ui.adjustScroll()
		}

	case "ctrl+y":
		ui.fresh()
		cmd := ui.copyCommand()
		return ui, cmd

	case "ctrl+o":
		ui.fresh()
		cmd := ui.copyOutput()
		return ui, cmd

	default:
		cmd := ui.handleQueryEdit(msg)
		return ui, cmd
	}

	return ui, nil
}

func (ui *SearchUI) handleQueryEdit(msg tea.KeyPressMsg) tea.Cmd {
	switch {
	case msg.String() == "backspace":
		if ui.query != "" {
			_, size := utf8.DecodeLastRuneInString(ui.query)
			ui.query = ui.query[:len(ui.query)-size]
			return ui.resetAndSearch()
		}
	case msg.Text != "" && msg.Mod&(tea.ModCtrl|tea.ModAlt) == 0:
		// Printable input, space and multibyte characters included
		ui.query += msg.Text
		return ui.resetAndSearch()
	}
	return nil
}

func (ui *SearchUI) resetAndSearch() tea.Cmd {
	ui.debounceID++
	return ui.debounceSearch(ui.debounceID)
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

// View implements tea.Model. Once the search ended its frame is empty:
// bubbletea leaves an inline program's last frame on the screen, and the
// prompt that comes next draws where the search was.
func (ui *SearchUI) View() tea.View {
	if ui.outcome != searchOpen {
		return ui.view("")
	}
	return ui.view(ui.frame())
}

// frame draws the open search: the query box, the results, the preview and
// the footer.
func (ui *SearchUI) frame() string {
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

	queryDisplay := termtext.OneLine(ui.query)
	if queryDisplay == "" {
		queryDisplay = dimStyle.Render("type to search...")
	}

	header := fmt.Sprintf("%s %s", headerLabel, queryDisplay)
	b.WriteString(headerBorder.Render(header))
	b.WriteString("\n")

	// Results
	if len(ui.results) == 0 {
		switch {
		case ui.stale():
			// The typed query has not been searched: nothing is known yet
			b.WriteString(dimStyle.Render("  searching..."))
		case ui.query != "":
			b.WriteString(dimStyle.Render("  No matches"))
		default:
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
			var line string
			if ui.stale() {
				// Waiting for the typed query: the old rows hold the space, dimmed
				line = "  " + dimStyle.Render(ui.truncateCommand(cmd.Command))
			} else {
				line = ui.formatResultLine(cmd, i, selectedStyle, normalStyle, dimStyle, errorStyle)
			}
			b.WriteString(line)
			b.WriteString("\n")
		}
	}

	// Preview pane for selected command
	if !ui.stale() && ui.selected >= 0 && ui.selected < len(ui.results) {
		ui.writePreview(&b, ui.results[ui.selected], normalStyle, dimStyle)
	}

	// Result count (bottom right)
	if len(ui.results) > 0 {
		countStr := fmt.Sprintf("result %d of %d", ui.selected+1, ui.totalResults)
		if ui.stale() {
			countStr = "searching..."
		}
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
	return b.String()
}

// view is the frame that draws content.
func (ui *SearchUI) view(content string) tea.View {
	v := tea.NewView(content)
	// Disable bracketed paste to prevent DECRQM queries on shutdown.
	// Bubbletea queries modes 2026/2027 at exit; the terminal responses
	// arrive on stdin after bubbletea's input reader closes, leaking
	// characters like [?2027;1$y into the next editor session.
	v.DisableBracketedPasteMode = true
	return v
}

// writePreview shows the selected command in full with its metadata.
func (ui *SearchUI) writePreview(b *strings.Builder, cmd Command, normalStyle, dimStyle lipgloss.Style) {
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("─── Preview ───"))
	b.WriteString("\n")

	// Full command (not truncated), each of its lines drawn inert
	lines := strings.Split(cmd.Command, "\n")
	for i, line := range lines {
		lines[i] = termtext.Inert(line)
	}
	b.WriteString(normalStyle.Render(strings.Join(lines, "\n")))
	b.WriteString("\n")

	// The question behind a command the agent produced
	if ui.store != nil {
		if question, ok := ui.store.AgentPromptForCommand(cmd.ID); ok {
			b.WriteString(dimStyle.Render("?? " + termtext.OneLine(question)))
			b.WriteString("\n")
		}
	}

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
	b.WriteString(dimStyle.Render(termtext.OneLine(meta)))
	b.WriteString("\n")
}

func (ui *SearchUI) formatResultLine(cmd Command, idx int, selected, normal, dim, errStyle lipgloss.Style) string {
	var b strings.Builder

	// Selection indicator
	if idx == ui.selected {
		b.WriteString(selected.Render("> "))
	} else {
		b.WriteString("  ")
	}

	cmdText := ui.truncateCommand(cmd.Command)

	if idx == ui.selected {
		b.WriteString(selected.Render(cmdText))
	} else {
		b.WriteString(normal.Render(cmdText))
	}

	// Right-align metadata with indicator before it
	meta := ui.formatTimestamp(cmd.Timestamp)
	if cmd.GitBranch != "" {
		meta += " " + termtext.OneLine(cmd.GitBranch)
	}

	// Calculate padding (indicator goes between padding and metadata)
	currentLen := 2 + runewidth.StringWidth(cmdText)
	padding := ui.width - currentLen - runewidth.StringWidth(meta) - 4 // -2 for indicator, -2 for margins
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

// truncateCommand fits a command into its column, measured in terminal
// columns, leaving room for the metadata. It is drawn inert on one row.
func (ui *SearchUI) truncateCommand(command string) string {
	command = termtext.OneLine(command)
	if maxCmdWidth := ui.width - 25; maxCmdWidth > 3 {
		return termtext.Truncate(command, maxCmdWidth, "...")
	}
	return command
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
	id      int // debounceID of the query searched
	results []Command
}

type debounceMsg struct {
	id int
}

// searchLimit fetches more than are visible, for scrolling.
const searchLimit = 100

// search returns a command that searches for the current query off the
// update loop. It captures the query and its id: the model itself belongs to
// Update and is never read from the command's goroutine.
func (ui *SearchUI) search() tea.Cmd {
	store, query, id := ui.store, ui.query, ui.debounceID
	return func() tea.Msg {
		results, _ := store.Search(SearchOptions{Query: query, Limit: searchLimit, Unique: true})
		return searchResultMsg{id: id, results: results}
	}
}

// stale reports whether the results are for an older query than the one
// typed (its debounced search has not landed yet).
func (ui *SearchUI) stale() bool {
	return ui.resultsID != ui.debounceID
}

// fresh brings stale results up to the typed query, so a key acting on the
// selection acts on a row of this query's results.
func (ui *SearchUI) fresh() {
	if ui.stale() {
		ui.searchNow()
	}
}

// searchNow searches for the current query synchronously.
func (ui *SearchUI) searchNow() {
	results, _ := ui.store.Search(SearchOptions{Query: ui.query, Limit: searchLimit, Unique: true})
	ui.setResults(ui.debounceID, results)
}

// setResults shows the results for the query with the given id and selects
// the first.
func (ui *SearchUI) setResults(id int, results []Command) {
	ui.results = results
	ui.resultsID = id
	ui.totalResults = len(results)
	ui.selected = 0
	ui.scrollOffset = 0
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
	_, err := sysClipboard.Write(context.Background(), sysClipboard.FmtText, []byte(text))
	return err
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
	output, state := ui.findOutputForCommand(cmd)
	if state == clipboard.OutputUncaptured {
		ui.statusMessage = "It wrote straight to the terminal: no output kept"
	} else if output == "" {
		ui.statusMessage = "No output available"
	} else if err := ui.copyToClipboard(output); err != nil {
		ui.statusMessage = "Clipboard error"
	} else {
		ui.statusMessage = "Copied output!"
	}
	return nil
}

// findOutputForCommand is the output the clipboard buffer kept for the
// last run of cmd, and what became of it.
func (ui *SearchUI) findOutputForCommand(cmd string) (string, clipboard.OutputState) {
	if ui.clipboardBuf == nil {
		return "", clipboard.OutputNone
	}

	for i := 0; i < ui.clipboardBuf.Len(); i++ {
		if ui.clipboardBuf.GetCommand(i) == cmd {
			return ui.clipboardBuf.GetOutput(i), ui.clipboardBuf.OutputState(i)
		}
	}
	return "", clipboard.OutputNone
}
