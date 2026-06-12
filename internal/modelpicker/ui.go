// Package modelpicker provides an interactive TUI for selecting the agent model.
package modelpicker

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/tfcace/hash/internal/agent"
)

// PickerUI is a single-select TUI listing the agent's available models.
type PickerUI struct {
	models    []agent.ModelOption
	current   string // value of the currently active model
	cursor    int
	confirmed bool
	width     int
	height    int
}

// NewPickerUI creates a model picker. current identifies the active model by
// its value or display name; the cursor starts on it when present.
func NewPickerUI(models []agent.ModelOption, current string) *PickerUI {
	cursor := 0
	for i, m := range models {
		if isCurrent(m, current) {
			cursor = i
			break
		}
	}
	return &PickerUI{
		models:  models,
		current: current,
		cursor:  cursor,
		width:   80,
		height:  20,
	}
}

// Run starts the interactive picker. It returns the selected model value and
// ok=true when the user confirms, or ok=false when they cancel.
func (ui *PickerUI) Run() (value string, ok bool, err error) {
	p := tea.NewProgram(ui)
	final, err := p.Run()
	if err != nil {
		return "", false, err
	}
	fin, okCast := final.(*PickerUI)
	if !okCast || !fin.confirmed || len(fin.models) == 0 {
		return "", false, nil
	}
	return fin.models[fin.cursor].Value, true, nil
}

// isCurrent reports whether m is the active model, matched by value or name.
func isCurrent(m agent.ModelOption, current string) bool {
	return current != "" && (m.Value == current || m.Name == current)
}

// Init implements tea.Model.
func (ui *PickerUI) Init() tea.Cmd {
	return nil
}

// Update implements tea.Model.
func (ui *PickerUI) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c", "esc", "q":
			ui.confirmed = false
			return ui, tea.Quit
		case "enter":
			ui.confirmed = true
			return ui, tea.Quit
		case "up", "k":
			if ui.cursor > 0 {
				ui.cursor--
			}
		case "down", "j":
			if ui.cursor < len(ui.models)-1 {
				ui.cursor++
			}
		}
	case tea.WindowSizeMsg:
		ui.width = msg.Width
		ui.height = msg.Height
	}
	return ui, nil
}

// View implements tea.Model.
func (ui *PickerUI) View() tea.View {
	var b strings.Builder

	// Palette and row structure mirror the context picker (internal/context/ui.go)
	// so the two TUIs look identical.
	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	selectedStyle := lipgloss.NewStyle().Background(lipgloss.Color("4")).Foreground(lipgloss.Color("15"))
	normalStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("7"))
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	checkStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("2"))

	fmt.Fprintf(&b, "%s\n\n", headerStyle.Render("Select agent model:"))

	for i, m := range ui.models {
		// Mark the active model with a checkbox, like the context picker.
		check := "[ ]"
		if isCurrent(m, ui.current) {
			check = checkStyle.Render("[x]")
		}

		label := m.Name
		if label == "" {
			label = m.Value
		}

		line := fmt.Sprintf("%s %s", check, label)
		if m.Description != "" {
			line = fmt.Sprintf("%s %s %s", check, label, dimStyle.Render(m.Description))
		}

		if i == ui.cursor {
			fmt.Fprintf(&b, "> %s\n", selectedStyle.Render(line))
		} else {
			fmt.Fprintf(&b, "  %s\n", normalStyle.Render(line))
		}
	}

	fmt.Fprintf(&b, "\n%s", dimStyle.Render("[↑/↓: move] [Enter: select] [Esc: cancel]"))

	v := tea.NewView(b.String())
	v.AltScreen = true
	return v
}
