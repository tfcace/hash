package context

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// PickerUI provides an interactive context picker TUI.
type PickerUI struct {
	picker *Picker
	width  int
	height int
}

// NewPickerUI creates a new picker UI.
func NewPickerUI(collection *Collection) *PickerUI {
	return &PickerUI{
		picker: NewPicker(collection),
		width:  80,
		height: 20,
	}
}

// Run starts the interactive picker and returns the serialized context.
// Returns empty string if canceled.
func (ui *PickerUI) Run() (string, error) {
	p := tea.NewProgram(ui)
	model, err := p.Run()
	if err != nil {
		return "", err
	}

	finalUI := model.(*PickerUI)
	return finalUI.picker.Serialize(), nil
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
			// Cancel - deselect all and quit
			ui.picker.DeselectAll()
			return ui, tea.Quit

		case "enter":
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

	// Group items by category
	collection := ui.picker.Collection()
	categories := make(map[Category][]int) // category -> item indices
	for i, item := range collection.Items {
		categories[item.Category] = append(categories[item.Category], i)
	}

	// Render items grouped by category
	itemIndex := 0
	for _, cat := range []Category{CategoryAutoDetect, CategoryHistory, CategoryEnv, CategoryCustom} {
		indices := categories[cat]
		if len(indices) == 0 {
			continue
		}

		fmt.Fprintf(&b, "%s\n", categoryStyle.Render(cat.String()))

		for _, idx := range indices {
			item := collection.Items[idx]

			// Checkbox
			check := "[ ]"
			if item.Selected {
				check = checkStyle.Render("[x]")
			}

			// Item display
			display := item.Key
			if len(display) > ui.width-10 {
				display = display[:ui.width-13] + "..."
			}

			// Size indicator
			size := fmt.Sprintf("(%d B)", item.SizeBytes)

			line := fmt.Sprintf("%s %s %s", check, display, dimStyle.Render(size))

			if itemIndex == ui.picker.Cursor() {
				fmt.Fprintf(&b, "> %s\n", selectedStyle.Render(line))
			} else {
				fmt.Fprintf(&b, "  %s\n", normalStyle.Render(line))
			}

			itemIndex++
		}
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
	fmt.Fprintf(&b, "\n%s", dimStyle.Render("[Space: toggle] [a: all] [n: none] [Enter: confirm] [Esc: skip]"))

	v := tea.NewView(b.String())
	v.AltScreen = true
	return v
}
