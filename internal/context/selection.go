package context

// itemID names a context item by what it is, not its value: a request
// includes the item's value as it is then (the cwd and branch now, the
// latest output).
type itemID struct {
	category Category
	key      string
}

func idOf(item Item) itemID {
	return itemID{category: item.Category, key: item.Key}
}

// Selection is what the user picked in the context picker. The zero value
// is the defaults: nothing picked yet, so a request gets the auto-detected
// context. A confirmed picker makes it the items chosen, which may be none
// at all ("n for none"): that is a choice too, not the defaults. A choice
// is fixed: an item that appears later (the git branch after cd into a
// repo) is not in it. The picker's "d for defaults" returns to the zero
// value, where such items are picked again.
type Selection struct {
	picked bool
	items  map[itemID]bool
}

// Defaults reports whether nothing has been picked, so the auto-detected
// context applies.
func (s Selection) Defaults() bool {
	return !s.picked
}

// SelectionOf is what is marked selected in c, as a choice (what
// confirming the picker on c gives).
func SelectionOf(c *Collection) Selection {
	s := Selection{picked: true, items: map[itemID]bool{}}
	for _, item := range c.Items {
		if item.Selected {
			s.items[idOf(item)] = true
		}
	}
	return s
}

// Apply marks c's items as picked by s. The defaults leave c's own default
// marks. A picked item no longer on offer (a command gone from recent
// history) drops out.
func (s Selection) Apply(c *Collection) {
	if !s.picked {
		return
	}
	for i := range c.Items {
		c.Items[i].Selected = s.items[idOf(c.Items[i])]
	}
}
