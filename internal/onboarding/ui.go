package onboarding

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

const docsURL = "runhash.dev/docs"

// Model is the first-run welcome panel: an agent picker fused with the
// quick-start orientation. It mirrors internal/modelpicker so the TUIs match.
type Model struct {
	agents  []Agent // adapters detected on PATH (may be empty)
	version string
	cursor  int
	done    bool
	chosen  *Agent // non-nil only when the user picked an agent
	width   int
	height  int
}

// New builds the welcome model for the detected agents (nil/empty is the
// no-adapter empty state).
func New(agents []Agent, version string) *Model {
	return &Model{agents: agents, version: version, width: 80, height: 24}
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
			m.chosen = nil
			m.done = true
			return m, tea.Quit
		case "enter":
			if m.cursor < len(m.agents) {
				a := m.agents[m.cursor]
				m.chosen = &a
			} else {
				m.chosen = nil // Skip row
			}
			m.done = true
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

// View implements tea.Model.
func (m *Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	return v
}

// render produces the panel text. Split from View so it can be unit-tested
// without depending on tea.View internals.
func (m *Model) render() string {
	header := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	selected := lipgloss.NewStyle().Background(lipgloss.Color("4")).Foreground(lipgloss.Color("15"))
	normal := lipgloss.NewStyle().Foreground(lipgloss.Color("7"))
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	accent := lipgloss.NewStyle().Foreground(lipgloss.Color("6"))

	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", header.Render("Welcome to Hash "+m.version))
	fmt.Fprintf(&b, "%s\n\n", dim.Render("Treat AI like a Unix pipe, not a magic wand."))

	if len(m.agents) == 0 {
		fmt.Fprintf(&b, "%s\n", normal.Render("No agent found yet — Hash works as a plain shell."))
		fmt.Fprintf(&b, "%s\n\n", normal.Render("Set one up to enable "+accent.Render("??")+": "+accent.Render(docsURL)))
	} else {
		fmt.Fprintf(&b, "%s\n", normal.Render("Enable "+accent.Render("??")+" — pick an agent:"))
		for i, a := range m.agents {
			m.writeRow(&b, i, a.Name, a.Desc+" · found", selected, normal, dim)
		}
		m.writeRow(&b, m.skipRow(), "Skip", "use Hash as a plain shell", selected, normal, dim)
		b.WriteString("\n")
	}

	// Quick-start orientation: preserved from the original welcome, folded in.
	fmt.Fprintf(&b, "%s\n", header.Render("Quick start:"))
	tips := [][2]string{
		{"??", "ask the AI"}, {"Ctrl+R", "search history"},
		{"cmd | ??", "pipe to AI"}, {"Ctrl+P", "pick context"},
		{"Ctrl+Y", "copy command"}, {"Ctrl+O", "copy last output"},
	}
	for i := 0; i < len(tips); i += 2 {
		left := fmt.Sprintf("  %-10s %-16s", tips[i][0], tips[i][1])
		right := ""
		if i+1 < len(tips) {
			right = fmt.Sprintf("%-8s %s", tips[i+1][0], tips[i+1][1])
		}
		fmt.Fprintf(&b, "%s%s\n", accent.Render(left), normal.Render(right))
	}

	nav := "[↑/↓ select · ⏎ choose · s skip]"
	if len(m.agents) == 0 {
		nav = "[⏎ continue]"
	}
	fmt.Fprintf(&b, "\n%s   %s\n", dim.Render(nav), dim.Render("docs: "+docsURL))

	return b.String()
}

func (m *Model) writeRow(b *strings.Builder, i int, label, desc string, sel, normal, dim lipgloss.Style) {
	line := fmt.Sprintf("%s %s", label, dim.Render(desc))
	if i == m.cursor {
		fmt.Fprintf(b, "> %s\n", sel.Render(label+" "+desc))
		return
	}
	fmt.Fprintf(b, "  %s\n", normal.Render(line))
}
