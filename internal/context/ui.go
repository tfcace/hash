package context

import (
	"context"
	"errors"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/tfcace/hash/internal/termtext"
)

// ErrPickerCanceled is returned by Run when the user leaves with Esc or
// Ctrl+C. The collection's marks are put back as they were when it opened.
var ErrPickerCanceled = errors.New("context picker canceled")

// pickerOutcome is how the picker ended.
type pickerOutcome int

const (
	pickerOpen      pickerOutcome = iota // still running
	pickerConfirmed                      // Enter: use the selection
	pickerDefaults                       // d: back to the auto-detected defaults
	pickerCanceled                       // Esc / Ctrl+C: keep what the caller had
)

// PickerUI provides an interactive context picker TUI.
type PickerUI struct {
	picker  *Picker
	opened  Selection // the marks the picker opened on, restored on cancel
	width   int
	height  int
	outcome pickerOutcome
}

// NewPickerUI creates a new picker UI. It opens on collection's marks: give
// it the current picks (Selection.Apply) so Enter keeps what was chosen
// before.
func NewPickerUI(collection *Collection) *PickerUI {
	return &PickerUI{
		picker: NewPicker(collection),
		opened: SelectionOf(collection),
		width:  80,
		height: 20,
	}
}

// Run starts the interactive picker and returns what was picked.
// Returns ErrPickerCanceled if the user canceled, and an error when ctx
// ends (the shell ending).
func (ui *PickerUI) Run(ctx context.Context) (Selection, error) {
	p := tea.NewProgram(ui, tea.WithContext(ctx))
	model, err := p.Run()
	if err != nil {
		return Selection{}, err
	}
	return model.(*PickerUI).result()
}

// result is the picker's answer once it has ended.
func (ui *PickerUI) result() (Selection, error) {
	switch ui.outcome {
	case pickerConfirmed:
		return SelectionOf(ui.picker.Collection()), nil
	case pickerDefaults:
		return Selection{}, nil
	}
	return Selection{}, ErrPickerCanceled
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
		case "ctrl+c", "esc":
			ui.outcome = pickerCanceled
			ui.opened.Apply(ui.picker.Collection())
			return ui, tea.Quit

		case "enter":
			ui.outcome = pickerConfirmed
			return ui, tea.Quit

		case "d":
			// A confirmed choice is fixed: items detected later (a git
			// branch after cd) are never picked. The defaults pick them.
			ui.outcome = pickerDefaults
			return ui, tea.Quit

		case "up", "k":
			ui.picker.MoveUp()

		case "down", "j":
			ui.picker.MoveDown()

		case "space":
			ui.picker.ToggleCurrent()

		case "a":
			ui.picker.SelectAll()

		case "n":
			ui.picker.DeselectAll()
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

	// Styles
	headerStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("6"))

	selectedStyle := lipgloss.NewStyle().
		Background(lipgloss.Color("4")).
		Foreground(lipgloss.Color("15"))

	normalStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("7"))

	dimStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("8"))

	checkStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("2"))

	categoryStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("5"))

	// Header
	fmt.Fprintf(&b, "%s\n\n", headerStyle.Render("Select context to include with agent request:"))

	// Render items grouped by category, in the picker's display order
	collection := ui.picker.Collection()
	order := collection.displayOrder()
	for row, idx := range order {
		item := collection.Items[idx]
		if row == 0 || collection.Items[order[row-1]].Category != item.Category {
			if row > 0 {
				fmt.Fprintln(&b)
			}
			fmt.Fprintf(&b, "%s\n", categoryStyle.Render(item.Category.String()))
		}

		// Checkbox
		check := "[ ]"
		if item.Selected {
			check = checkStyle.Render("[x]")
		}

		// Size indicator
		size := fmt.Sprintf("(%d B)", item.SizeBytes)

		// Item display, truncated to fit the row in terminal columns
		display := termtext.OneLine(item.Key)
		if maxWidth := ui.width - 10 - len(size) - 1; maxWidth > 3 {
			display = termtext.Truncate(display, maxWidth, "...")
		}

		line := fmt.Sprintf("%s %s %s", check, display, dimStyle.Render(size))

		if row == ui.picker.Cursor() {
			fmt.Fprintf(&b, "> %s\n", selectedStyle.Render(line))
		} else {
			fmt.Fprintf(&b, "  %s\n", normalStyle.Render(line))
		}
	}
	if len(order) > 0 {
		fmt.Fprintln(&b)
	}

	// Size status
	selectedSize := collection.SelectedSize()
	maxSize := collection.MaxSizeBytes
	status := collection.SizeStatus()

	var statusStyle lipgloss.Style
	switch status {
	case "green":
		statusStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	case "yellow":
		statusStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	case "red":
		statusStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	}

	sizeInfo := fmt.Sprintf("Selected: %d / %d bytes", selectedSize, maxSize)
	fmt.Fprintf(&b, "%s\n", statusStyle.Render(sizeInfo))

	// Footer
	fmt.Fprintf(&b, "\n%s", dimStyle.Render("space to toggle · a all · n none · d defaults · enter to confirm · esc to skip"))

	v := tea.NewView(b.String())
	v.AltScreen = true
	return v
}
