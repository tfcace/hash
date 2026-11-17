// Package modelpicker provides an interactive TUI for selecting the agent model.
package modelpicker

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/mattn/go-runewidth"

	"github.com/tfcace/hash/internal/agent"
	"github.com/tfcace/hash/internal/listui"
	"github.com/tfcace/hash/internal/prompt"
	"github.com/tfcace/hash/internal/termtext"
)

// PickerUI is a single-select TUI listing the agent's available models.
type PickerUI struct {
	// Palette colors the picker; the shell sets it to its own.
	Palette prompt.Palette

	models  []agent.ModelOption
	current string // value of the currently active model
	cursor  int
	done    bool // the picker ended, confirmed or not
	// confirmed is whether the picker ended on enter.
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
		Palette: prompt.DefaultPalette(),
		models:  models,
		current: current,
		cursor:  cursor,
		width:   80,
		height:  20,
	}
}

// Run starts the interactive picker. It returns the selected model value and
// ok=true when the user confirms, or ok=false when they cancel. It ends
// with ctx (the shell ending), with an error.
func (ui *PickerUI) Run(ctx context.Context) (value string, ok bool, err error) {
	p := tea.NewProgram(ui, tea.WithContext(ctx))
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
			ui.done, ui.confirmed = true, false
			return ui, tea.Quit
		case "enter":
			ui.done, ui.confirmed = true, true
			return ui, tea.Quit
		case "up", "k":
			ui.cursor--
		case "down", "j":
			ui.cursor++
		case "pgup":
			ui.cursor -= listui.Page(ui.height)
		case "pgdown":
			ui.cursor += listui.Page(ui.height)
		case "home":
			ui.cursor = 0
		case "end":
			ui.cursor = len(ui.models) - 1
		}
		ui.cursor = max(min(ui.cursor, len(ui.models)-1), 0)
	case tea.WindowSizeMsg:
		ui.width = msg.Width
		ui.height = msg.Height
	}
	return ui, nil
}

// pickerKeys are the keys the picker answers to: the foot shows the ones
// that fit.
var pickerKeys = []string{"↑/↓", "enter select", "esc cancel"}

// View implements tea.Model. The picker draws inline, on the frame every
// hash surface shares, and takes its rows with it when it closes.
func (ui *PickerUI) View() tea.View {
	if ui.done {
		return tea.NewView("")
	}
	return tea.NewView(strings.Join(ui.rows(), "\n"))
}

// rows is the open picker: the model in use on the title row, the models
// with the one in use marked, and the keys.
func (ui *PickerUI) rows() []string {
	accent := lipgloss.NewStyle().Foreground(lipgloss.Color(ui.Palette.Primary))
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color(ui.Palette.Dim))
	bar := dim.Render("│") + " "

	// Names and descriptions come from the agent: drawn inert, on one row.
	labels := make([]string, len(ui.models))
	labelWidth, inUse := 0, ""
	for i, m := range ui.models {
		labels[i] = termtext.OneLine(m.Name)
		if labels[i] == "" {
			labels[i] = termtext.OneLine(m.Value)
		}
		labelWidth = max(labelWidth, runewidth.StringWidth(labels[i]))
		if isCurrent(m, ui.current) {
			inUse = labels[i]
		}
	}
	// The bar, the cursor and the mark take six columns; the row stops a
	// column short of the pane.
	room := max(ui.width-7, 8)
	labelWidth = min(labelWidth, room)

	title := dim.Render("┌ ") + accent.Bold(true).Render("model")
	if inUse != "" {
		title += dim.Render(" · ") + termtext.Truncate(inUse, max(ui.width-len("┌ model · ")-1, 4), "...")
	}

	list := make([]string, len(ui.models))
	for i, m := range ui.models {
		label := termtext.Truncate(labels[i], labelWidth, "...")
		mark, circle := "  ", dim.Render("○")
		if isCurrent(m, ui.current) {
			circle = accent.Render("●")
		}
		desc := ""
		if left := room - labelWidth - 2; m.Description != "" && left > 3 {
			label += strings.Repeat(" ", labelWidth-runewidth.StringWidth(label))
			desc = "  " + dim.Render(termtext.Truncate(termtext.OneLine(m.Description), left, "..."))
		}
		if i == ui.cursor {
			mark, label = accent.Render("›")+" ", lipgloss.NewStyle().Bold(true).Render(label)
		}
		list[i] = bar + mark + circle + " " + label + desc
	}

	rows := []string{title}
	shown := len(list)
	if limit := listui.FrameRows(ui.height); limit > 0 {
		shown = limit - 2
	}
	rows = append(rows, listui.Window(list, ui.cursor, shown, func(hidden int, above bool) string {
		if above {
			return bar + dim.Render(fmt.Sprintf("↑ %d more", hidden))
		}
		return bar + dim.Render(fmt.Sprintf("↓ %d more", hidden))
	})...)
	return append(rows, dim.Render(ui.foot()))
}

// foot is the frame's last row: the keys, as many whole ones as fit.
func (ui *PickerUI) foot() string {
	for n := len(pickerKeys); ; n-- {
		row := "└─ " + strings.Join(pickerKeys[:n], " · ")
		if n == 1 || runewidth.StringWidth(row) < ui.width {
			return row
		}
	}
}
