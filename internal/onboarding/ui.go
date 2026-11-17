package onboarding

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/mattn/go-runewidth"

	"github.com/tfcace/hash/internal/prompt"
	"github.com/tfcace/hash/internal/termtext"
)

const docsURL = "runhash.dev/docs"

// Model is the first-run welcome panel: an agent picker fused with the
// quick-start orientation, on the frame every hash surface shares.
type Model struct {
	// Palette colors the panel; the shell sets it to its own.
	Palette prompt.Palette

	agents  []Agent // adapters detected on PATH (may be empty)
	done    bool    // the panel was answered
	version string
	cursor  int
	chosen  *Agent // non-nil only when the user picked an agent
	width   int
	height  int
}

// New builds the welcome model for the detected agents (nil/empty is the
// no-adapter empty state).
func New(agents []Agent, version string) *Model {
	return &Model{Palette: prompt.DefaultPalette(), agents: agents, version: version, width: 80, height: 24}
}

// skipRow is the index of the trailing "Skip" choice.
func (m *Model) skipRow() int { return len(m.agents) }

// Result returns the chosen agent, or ok=false if the user skipped / the
// empty state dropped through to a plain shell.
func (m *Model) Result() (Agent, bool) {
	if m.chosen == nil {
		return Agent{}, false
	}
	return *m.chosen, true
}

// Run starts the interactive panel and returns the user's choice.
func (m *Model) Run() (Agent, bool, error) {
	final, err := tea.NewProgram(m).Run()
	if err != nil {
		return Agent{}, false, err
	}
	fin, ok := final.(*Model)
	if !ok {
		return Agent{}, false, nil
	}
	a, chosen := fin.Result()
	return a, chosen, nil
}

// Init implements tea.Model.
func (m *Model) Init() tea.Cmd { return nil }

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c", "esc", "q", "s", "S":
			m.chosen, m.done = nil, true
			return m, tea.Quit
		case "enter":
			m.done = true
			if m.cursor < len(m.agents) {
				a := m.agents[m.cursor]
				m.chosen = &a
			} else {
				m.chosen = nil // Skip row
			}
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < m.skipRow() {
				m.cursor++
			}
		}
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	}
	return m, nil
}

// View implements tea.Model. The panel draws inline, above the first
// prompt, and takes its rows with it once answered.
func (m *Model) View() tea.View {
	return tea.NewView(m.render())
}

// tips is the quick start: what to type, and what it does.
var tips = [][2]string{
	{"??", "ask the AI"}, {"ctrl+r", "search history"},
	{"cmd | ??", "pipe to AI"}, {"ctrl+p", "pick context"},
	{"copy cmd", "copy command"}, {"copy out", "copy last output"},
}

// render produces the panel text. Split from View so it can be unit-tested
// without depending on tea.View internals.
func (m *Model) render() string {
	if m.done {
		return ""
	}
	accent := lipgloss.NewStyle().Foreground(lipgloss.Color(m.Palette.Primary))
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color(m.Palette.Dim))
	bar := dim.Render("│") + " "
	room := max(m.width-3, 8) // behind the bar, a column short of the pane
	fit := func(text string, width int) string { return termtext.Truncate(text, max(width, 1), "…") }

	rows := []string{
		dim.Render("┌ ") + accent.Bold(true).Render("welcome") + dim.Render(fit(" · hash "+m.version, m.width-len("┌ welcome")-1)),
		bar + fit("Treat AI like a Unix pipe, not a magic wand.", room),
	}

	if len(m.agents) == 0 {
		rows = append(rows,
			bar+fit("No agent found yet: hash works as a plain shell.", room),
			bar+fit("Set one up to enable ??: "+docsURL, room))
	} else {
		rows = append(rows, dim.Render(fit("├ pick an agent to enable ??", m.width-1)))
		choices := make([][2]string, 0, len(m.agents)+1)
		nameWidth := len("Skip")
		for _, a := range m.agents {
			choices = append(choices, [2]string{a.Name, a.Desc + " · found"})
			nameWidth = max(nameWidth, runewidth.StringWidth(a.Name))
		}
		choices = append(choices, [2]string{"Skip", "use hash as a plain shell"})
		nameWidth = min(nameWidth, room-2)
		for i, c := range choices {
			name := fit(c[0], nameWidth)
			name += strings.Repeat(" ", nameWidth-runewidth.StringWidth(name))
			desc := ""
			if left := room - 2 - nameWidth - 2; left > 3 {
				desc = "  " + dim.Render(fit(c[1], left))
			}
			mark := "  "
			if i == m.cursor {
				mark, name = accent.Render("›")+" ", lipgloss.NewStyle().Bold(true).Render(name)
			}
			rows = append(rows, bar+mark+name+desc)
		}
	}

	rows = append(rows, dim.Render("├ quick start"))
	// Two tips a row where the pane has room for them, one where it has not.
	columns := 2
	widths := tipWidths(columns)
	if widths[0]+2+widths[1]+2+widths[2]+2+widths[3] > room {
		columns, widths = 1, tipWidths(1)
	}
	pad := func(text string, width int) string {
		return text + strings.Repeat(" ", max(width-runewidth.StringWidth(text), 0))
	}
	for i := 0; i < len(tips); i += columns {
		row := accent.Render(pad(tips[i][0], widths[0])) + "  "
		if columns == 1 {
			rows = append(rows, bar+row+fit(tips[i][1], room-widths[0]-2))
			continue
		}
		row += pad(tips[i][1], widths[1]) + "  " + accent.Render(pad(tips[i+1][0], widths[2])) + "  " + tips[i+1][1]
		rows = append(rows, bar+row)
	}

	keys := []string{"↑/↓", "enter choose", "s skip", "docs " + docsURL}
	if len(m.agents) == 0 {
		keys = []string{"enter continue", "docs " + docsURL}
	}
	for n := len(keys); ; n-- {
		foot := "└─ " + strings.Join(keys[:n], " · ")
		if n == 1 || runewidth.StringWidth(foot) < m.width {
			return strings.Join(append(rows, dim.Render(foot)), "\n")
		}
	}
}

// tipWidths is the width of each column of the quick start laid out in
// columns pairs a row: the keys and the texts of each pair.
func tipWidths(columns int) [4]int {
	var widths [4]int
	for i, tip := range tips {
		col := (i % columns) * 2
		widths[col] = max(widths[col], runewidth.StringWidth(tip[0]))
		widths[col+1] = max(widths[col+1], runewidth.StringWidth(tip[1]))
	}
	return widths
}
