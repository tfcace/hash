package context

import (
	"context"
	"errors"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/mattn/go-runewidth"
	"github.com/tfcace/hash/internal/listui"
	"github.com/tfcace/hash/internal/prompt"
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
	// Palette is the interface's colors. NewPickerUI sets the default one.
	Palette prompt.Palette

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
		Palette: prompt.DefaultPalette(),
		picker:  NewPicker(collection),
		opened:  SelectionOf(collection),
		width:   80,
		height:  20,
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

		case "pgup":
			ui.picker.MoveTo(ui.picker.Cursor() - listui.Page(ui.height))

		case "pgdown":
			ui.picker.MoveTo(ui.picker.Cursor() + listui.Page(ui.height))

		case "home":
			ui.picker.MoveTo(0)

		case "end":
			ui.picker.MoveTo(len(ui.picker.Collection().Items) - 1)

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

// pickerKeys are the keys the picker answers to: the foot shows the ones
// that fit.
var pickerKeys = []string{"space toggle", "a all", "n none", "d defaults", "enter confirm", "esc skip"}

const (
	meterCells   = 24 // the budget meter on the title row
	previewLines = 4  // how much of the current item's value is shown
	minListRows  = 8  // a longer list keeps at least this many rows before the preview gets any
	sizeColumn   = 7  // "1023 B", "9.9 kB", "512 kB", "12.3 MB"
)

// View implements tea.Model. The picker draws inline, on the frame every
// hash surface shares, and takes its rows with it when it closes.
func (ui *PickerUI) View() tea.View {
	if ui.outcome != pickerOpen {
		return tea.NewView("")
	}
	return tea.NewView(strings.Join(ui.rows(), "\n"))
}

// rows is the open picker: what is picked against the budget on the title
// row, a section per category, the budget, and the keys.
func (ui *PickerUI) rows() []string {
	accent := lipgloss.NewStyle().Foreground(lipgloss.Color(ui.Palette.Primary))
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color(ui.Palette.Dim))
	over := lipgloss.NewStyle().Foreground(lipgloss.Color(ui.Palette.Error))
	bar := dim.Render("│") + " "

	collection := ui.picker.Collection()
	order := collection.displayOrder()
	used, budget := collection.SelectedSize(), max(collection.MaxSizeBytes, 1)

	under := Item{} // the item under the cursor
	if len(order) > 0 {
		under = collection.Items[order[min(ui.picker.Cursor(), len(order)-1)]]
	}
	rows := []string{dim.Render("┌ ") + accent.Bold(true).Render("context") + dim.Render(" · ") + ui.summary(under, used, budget, accent, dim, over)}
	if len(order) == 0 {
		return append(rows, bar+dim.Render("nothing to pick"), dim.Render(ui.foot()))
	}

	// The key column is as wide as the longest key, as far as the pane
	// allows; the share bar takes what is left.
	keyWidth := 0
	for _, idx := range order {
		keyWidth = max(keyWidth, runewidth.StringWidth(termtext.OneLine(collection.Items[idx].Key)))
	}
	keyWidth = max(min(keyWidth, ui.width-9-sizeColumn), 4)

	var list []string // the sections and their items
	cursorRow := 0
	for row, idx := range order {
		item := collection.Items[idx]
		if row == 0 || collection.Items[order[row-1]].Category != item.Category {
			list = append(list, dim.Render("├ "+strings.ToLower(item.Category.String())))
		}
		current := row == ui.picker.Cursor()
		if current {
			cursorRow = len(list)
		}

		key := termtext.Truncate(termtext.OneLine(item.Key), keyWidth, "...")
		key += strings.Repeat(" ", max(keyWidth-runewidth.StringWidth(key), 0))

		mark, circle := "  ", dim.Render("○")
		switch {
		case item.Selected:
			circle = accent.Render("●")
		case !current:
			key = dim.Render(key)
		}
		if current {
			mark, key = accent.Render("›")+" ", lipgloss.NewStyle().Bold(true).Render(key)
		}
		list = append(list, bar+mark+circle+" "+key+dim.Render(fmt.Sprintf("%*s", sizeColumn, formatSize(item.SizeBytes))))
	}

	// The list comes first: the preview is shown when the frame has room
	// for it beside a list worth reading.
	limit := listui.FrameRows(ui.height)
	preview := ui.preview(under, bar, dim)
	if limit > 0 && limit-2-len(preview) < min(len(list), minListRows) {
		preview = nil
	}
	room := len(list)
	if limit > 0 {
		room = limit - 2 - len(preview)
	}
	rows = append(rows, listui.Window(list, cursorRow, room, func(hidden int, above bool) string {
		if above {
			return bar + dim.Render(fmt.Sprintf("↑ %d more", hidden))
		}
		return bar + dim.Render(fmt.Sprintf("↓ %d more", hidden))
	})...)
	return append(append(rows, preview...), dim.Render(ui.foot()))
}

// preview is what the current item would send: its first few lines, and
// how many more there are.
func (ui *PickerUI) preview(current Item, bar string, dim lipgloss.Style) []string {
	lines := strings.Split(strings.TrimRight(current.Value, "\n"), "\n")
	if hidden := len(lines) - previewLines; hidden > 0 {
		lines = append(lines[:previewLines-1:previewLines-1], fmt.Sprintf("… %d more lines", hidden+1))
	}
	rows := []string{dim.Render("├ preview · " + termtext.Truncate(termtext.OneLine(current.Key), max(ui.width-len("├ preview · ")-1, 4), "..."))}
	for _, line := range lines {
		rows = append(rows, bar+dim.Render(termtext.Truncate(termtext.Inert(line), max(ui.width-3, 1), "…")))
	}
	return rows
}

// summary is the title row's summary: what is picked against the budget,
// and a meter of it when that fits. Picks over the budget say by how much.
func (ui *PickerUI) summary(current Item, used, budget int, accent, dim, over lipgloss.Style) string {
	short := formatSize(used) + " of " + formatSize(budget)
	if used > budget {
		return over.Render(short + " · over by " + formatSize(used-budget))
	}
	if len("┌ context · ")+len(short)+2+meterCells >= ui.width {
		return short
	}
	return short + "  " + ui.meter(current, used, budget, accent, dim, over)
}

// meter draws the budget: what is picked, what the current row would add,
// and what is left.
func (ui *PickerUI) meter(current Item, used, budget int, accent, dim, over lipgloss.Style) string {
	cells := func(bytes int) int { return min(bytes*meterCells/budget, meterCells) }
	picked := cells(used)
	if used > 0 {
		picked = max(picked, 1)
	}
	adds := 0
	if !current.Selected {
		adds = min(max(cells(used+current.SizeBytes), picked+1), meterCells) - picked
	}
	added := accent
	if used+current.SizeBytes > budget {
		added = over
	}
	return accent.Render(strings.Repeat("█", picked)) + added.Render(strings.Repeat("▒", adds)) +
		dim.Render(strings.Repeat("░", meterCells-picked-adds))
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

// formatSize is a byte count as a person reads it, in at most sizeColumn
// columns. Units are powers of 1024, as the budget is set.
func formatSize(bytes int) string {
	switch kB := float64(bytes) / 1024; {
	case bytes < 1024:
		return fmt.Sprintf("%d B", bytes)
	case kB < 9.95:
		return fmt.Sprintf("%.1f kB", kB)
	case kB < 999.5:
		return fmt.Sprintf("%.0f kB", kB)
	default:
		return fmt.Sprintf("%.1f MB", kB/1024)
	}
}
