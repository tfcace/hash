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
	"github.com/tfcace/hash/internal/listui"
	"github.com/tfcace/hash/internal/prompt"
	"github.com/tfcace/hash/internal/termtext"
	"github.com/tfcace/hash/internal/tilde"
	sysClipboard "golang.design/x/clipboard"
)

const (
	// defaultVisibleResults is how many results are listed before the
	// pane's height is known.
	defaultVisibleResults = 10
	// minVisibleResults are listed however short the pane is.
	minVisibleResults = 3
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
		ui.adjustScroll()

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

	case "pgup", "pgdown", "home", "end":
		ui.fresh()
		switch page := ui.visibleResults(); msg.String() {
		case "pgup":
			ui.selected -= page
		case "pgdown":
			ui.selected += page
		case "home":
			ui.selected = 0
		case "end":
			ui.selected = len(ui.results) - 1
		}
		ui.selected = max(min(ui.selected, len(ui.results)-1), 0)
		ui.adjustScroll()

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
	if visible := ui.visibleResults(); ui.selected >= ui.scrollOffset+visible {
		ui.scrollOffset = ui.selected - visible + 1
	}
}

// visibleResults is how many results the frame lists: what its share of
// the pane leaves beside its other rows.
func (ui *SearchUI) visibleResults() int {
	limit := listui.FrameRows(ui.height)
	if limit == 0 {
		return defaultVisibleResults
	}
	other := 2 // the title and the foot
	if len(ui.results) > 0 {
		other++ // the selected result's row, or "searching..."
	}
	if ui.selected >= 0 && ui.selected < len(ui.results) && !ui.stale() {
		other += len(ui.whole(ui.results[ui.selected], lipgloss.NewStyle()))
	}
	if ui.statusMessage != "" {
		other++
	}
	return max(limit-other, minVisibleResults)
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

// searchKeys are the keys the search answers to, most needed first: the
// foot shows the ones that fit.
var searchKeys = []string{"↑/↓", "pgup/pgdn", "enter select", searchWayOut, "ctrl+y copy cmd", "ctrl+o copy output"}

// searchWayOut is the one key that always applies.
const searchWayOut = "esc cancel"

// frame draws the open search on the frame every hash surface shares: a
// title row with the query, the results and the selected one's preview
// behind a bar, and the keys on the last row.
func (ui *SearchUI) frame() string {
	accent := lipgloss.NewStyle().Foreground(lipgloss.Color(ui.palette.Primary))
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color(ui.palette.Dim))
	bar := dim.Render("│") + " "

	query := termtext.OneLine(ui.query)
	if query == "" {
		query = dim.Render("type to search...")
	} else if room := ui.width - len("┌ history · ") - 1; room > 3 {
		query = termtext.Truncate(query, room, "...")
	}
	rows := []string{dim.Render("┌ ") + accent.Bold(true).Render("history") + dim.Render(" · ") + query}

	switch {
	case len(ui.results) > 0:
		last := min(ui.scrollOffset+ui.visibleResults(), len(ui.results))
		for i := ui.scrollOffset; i < last; i++ {
			if ui.stale() {
				// Waiting for the typed query: the old rows hold the space, dimmed
				rows = append(rows, bar+"  "+dim.Render(ui.fitCommand(ui.results[i].Command, ui.width-7)))
				continue
			}
			rows = append(rows, bar+ui.resultRow(ui.results[i], i == ui.selected, accent, dim))
		}
	case ui.stale():
		// The typed query has not been searched: nothing is known yet
		rows = append(rows, bar+dim.Render("searching..."))
	case ui.query != "":
		rows = append(rows, bar+dim.Render("No matches"))
	default:
		rows = append(rows, bar+dim.Render("No history"))
	}

	switch {
	case len(ui.results) == 0:
	case ui.stale():
		rows = append(rows, dim.Render("├ searching..."))
	case ui.selected >= 0 && ui.selected < len(ui.results):
		rows = append(rows, dim.Render(ui.about(ui.results[ui.selected])))
		for _, line := range ui.whole(ui.results[ui.selected], dim) {
			rows = append(rows, bar+line)
		}
	}

	if ui.statusMessage != "" {
		ok := lipgloss.NewStyle().Foreground(lipgloss.Color(ui.palette.Success))
		rows = append(rows, bar+ok.Render(ui.statusMessage))
	}
	return strings.Join(append(rows, dim.Render(ui.foot())), "\n")
}

// foot is the frame's last row: the keys, as many whole ones as fit.
func (ui *SearchUI) foot() string {
	keys := searchKeys
	if len(ui.results) == 0 {
		// Nothing to move through, select or copy: only the way out.
		keys = []string{searchWayOut}
	}
	for n := len(keys); ; n-- {
		row := "└─ " + strings.Join(keys[:n], " · ")
		if n == 1 || runewidth.StringWidth(row) < ui.width {
			return row
		}
	}
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

// about is the row under the list for the selected result: which one it
// is, and when and where it ran.
func (ui *SearchUI) about(cmd Command) string {
	room := ui.width - 1 // a column short of the pane

	status := "✓"
	if cmd.ExitCode != 0 {
		status = fmt.Sprintf("✗%d", cmd.ExitCode)
	}
	head := []string{fmt.Sprintf("├ %d of %d", ui.selected+1, ui.totalResults), cmd.Timestamp.Format("2006-01-02 15:04")}
	tail := []string{status}
	if cmd.GitBranch != "" {
		tail = []string{fit(termtext.OneLine(cmd.GitBranch), maxBranchColumns), status}
	}
	// The folder takes what the rest leaves, keeping its end: the part
	// that tells one folder from another.
	cwd := termtext.OneLine(tilde.Abbrev(cmd.Cwd))
	if room > 0 {
		rest := runewidth.StringWidth(strings.Join(append(head, tail...), " · ")) + len(" · ")
		cwd = fitEnd(cwd, room-rest)
	}
	if cwd != "" {
		head = append(head, cwd)
	}
	return fit(strings.Join(append(head, tail...), " · "), room)
}

// maxWholeRows is how many rows the selected command may take under the
// list: the results above it are what is being searched.
const maxWholeRows = 3

// whole is the rows under the list for what the selected result's own row
// could not show: the command, when its row cut it or it has more lines
// than one, and the question behind a command the agent produced.
func (ui *SearchUI) whole(cmd Command, dim lipgloss.Style) []string {
	room := ui.width - 3 // behind the bar, a column short of the pane

	var rows []string
	_, commandRoom := ui.rowLayout(cmd)
	if strings.Contains(cmd.Command, "\n") || (ui.width > 0 && runewidth.StringWidth(termtext.OneLine(cmd.Command)) > commandRoom) {
		for _, line := range strings.Split(cmd.Command, "\n") {
			rows = append(rows, breakColumns(termtext.Inert(line), room)...)
		}
		if len(rows) > maxWholeRows {
			rows = append(rows[:maxWholeRows-1], dim.Render(fmt.Sprintf("… %d more lines", len(rows)-maxWholeRows+1)))
		}
	}
	if ui.store != nil {
		if question, ok := ui.store.AgentPromptForCommand(cmd.ID); ok {
			rows = append(rows, dim.Render(fit("?? "+termtext.OneLine(question), room)))
		}
	}
	return rows
}

// maxBranchColumns is how much of a branch name a row shows.
const maxBranchColumns = 24

// fit is s in at most width columns, cut with an ellipsis. A width below 1
// is no limit.
func fit(s string, width int) string {
	if width < 1 {
		return s
	}
	return termtext.Truncate(s, width, "…")
}

// fitEnd is the end of s in at most width columns, behind an ellipsis, or
// nothing when there is no room for any of it.
func fitEnd(s string, width int) string {
	if runewidth.StringWidth(s) <= width {
		return s
	}
	if width < 4 {
		return ""
	}
	runes := []rune(s)
	start := len(runes)
	for used := 1; start > 0; start-- {
		used += runewidth.RuneWidth(runes[start-1])
		if used > width {
			break
		}
	}
	return "…" + string(runes[start:])
}

// breakColumns breaks s into rows of at most width columns, between words
// where it can and inside a word longer than a row. A width below 1 is no
// limit.
func breakColumns(s string, width int) []string {
	if width < 1 || runewidth.StringWidth(s) <= width {
		return []string{s}
	}
	var rows []string
	row, used := "", 0
	for _, word := range strings.Split(s, " ") {
		w := runewidth.StringWidth(word)
		if used > 0 && used+1+w <= width {
			row, used = row+" "+word, used+1+w
			continue
		}
		if used > 0 {
			rows = append(rows, row)
		}
		// A word longer than a row fills rows until its rest fits one.
		for w > width {
			head := runewidth.Truncate(word, width, "")
			rows = append(rows, head)
			word = word[len(head):]
			w = runewidth.StringWidth(word)
		}
		row, used = word, w
	}
	return append(rows, row)
}

// resultRow is one result: a cursor on the current one, the command, and
// when it ran, right-aligned behind a mark if it failed.
func (ui *SearchUI) resultRow(cmd Command, current bool, accent, dim lipgloss.Style) string {
	meta, commandRoom := ui.rowLayout(cmd)
	room := ui.width - 7
	cmdText := ui.fitCommand(cmd.Command, commandRoom)
	row := "  " + cmdText
	if current {
		row = accent.Render("›") + " " + lipgloss.NewStyle().Bold(true).Render(cmdText)
	}
	if padding := room - runewidth.StringWidth(cmdText) - runewidth.StringWidth(meta); padding > 0 {
		row += strings.Repeat(" ", padding)
	}
	mark := "  "
	if cmd.ExitCode != 0 {
		mark = lipgloss.NewStyle().Foreground(lipgloss.Color(ui.palette.Error)).Render("✗") + " "
	}
	return row + mark + dim.Render(meta)
}

// fitCommand is a command on one inert row of at most room columns. Before
// the pane's width is known it is left whole.
func (ui *SearchUI) fitCommand(command string, room int) string {
	command = termtext.OneLine(command)
	if ui.width <= 0 {
		return command
	}
	return termtext.Truncate(command, max(room, 1), "...")
}

// rowLayout is what sits beside a result's command on its row, and the
// columns that leaves the command. The command comes first: the branch,
// then when it ran, give way to it.
func (ui *SearchUI) rowLayout(cmd Command) (meta string, commandRoom int) {
	// The bar, the cursor and the mark take two columns each; the row stops
	// a column short of the pane.
	room := ui.width - 7

	when := ui.formatTimestamp(cmd.Timestamp)
	meta = when
	if cmd.GitBranch != "" {
		meta += " · " + fit(termtext.OneLine(cmd.GitBranch), maxBranchColumns)
	}
	if ui.width > 0 {
		for _, shorter := range []string{when, ""} {
			if room-runewidth.StringWidth(meta) >= minCommandColumns {
				break
			}
			meta = shorter
		}
	}
	return meta, room - runewidth.StringWidth(meta) - 1
}

// minCommandColumns is the least room a result's command is left with
// before what sits beside it is dropped.
const minCommandColumns = 16

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
