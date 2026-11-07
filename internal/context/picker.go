package context

// Picker provides an interactive context picker. The cursor is a row in
// display order (grouped by category), not an index into Items.
type Picker struct {
	collection *Collection
	cursor     int
}

// NewPicker creates a new context picker.
func NewPicker(collection *Collection) *Picker {
	return &Picker{
		collection: collection,
		cursor:     0,
	}
}

// Cursor returns the current cursor row.
func (p *Picker) Cursor() int {
	return p.cursor
}

// Collection returns the underlying collection.
func (p *Picker) Collection() *Collection {
	return p.collection
}

// MoveUp moves the cursor up.
func (p *Picker) MoveUp() {
	if p.cursor > 0 {
		p.cursor--
	}
}

// MoveDown moves the cursor down.
func (p *Picker) MoveDown() {
	if len(p.collection.Items) > 0 && p.cursor < len(p.collection.Items)-1 {
		p.cursor++
	}
}

// current returns the Items index under the cursor, or -1 if empty.
func (p *Picker) current() int {
	order := p.collection.displayOrder()
	if p.cursor >= len(order) {
		return -1
	}
	return order[p.cursor]
}

// ToggleCurrent toggles selection of the current item.
func (p *Picker) ToggleCurrent() {
	if i := p.current(); i >= 0 {
		p.collection.Toggle(i)
	}
}

// CurrentItem returns the currently selected item, or nil if empty.
func (p *Picker) CurrentItem() *Item {
	i := p.current()
	if i < 0 {
		return nil
	}
	return &p.collection.Items[i]
}

// ItemCount returns the number of items in the collection.
func (p *Picker) ItemCount() int {
	return len(p.collection.Items)
}

// Reset resets the cursor to the beginning.
func (p *Picker) Reset() {
	p.cursor = 0
}

// SelectAll selects all items.
func (p *Picker) SelectAll() {
	p.collection.SelectAll()
}

// DeselectAll deselects all items.
func (p *Picker) DeselectAll() {
	p.collection.DeselectAll()
}

// SelectedSize returns the size of selected items.
func (p *Picker) SelectedSize() int {
	return p.collection.SelectedSize()
}

// SizeStatus returns the size status indicator.
func (p *Picker) SizeStatus() string {
	return p.collection.SizeStatus()
}
