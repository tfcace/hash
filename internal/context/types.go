package context

import (
	"fmt"
	"strings"
)

// Category defines context item types.
type Category int

const (
	CategoryHistory    Category = iota // Recent commands
	CategoryEnv                        // Environment variables
	CategoryAutoDetect                 // Auto-detected context (cwd, git, k8s)
	CategoryCustom                     // User-added context
)

func (c Category) String() string {
	switch c {
	case CategoryHistory:
		return "History"
	case CategoryEnv:
		return "Environment"
	case CategoryAutoDetect:
		return "Auto-detected"
	case CategoryCustom:
		return "Custom"
	default:
		return "Unknown"
	}
}

// Item represents a single context item.
type Item struct {
	Category  Category
	Key       string // Display key (e.g., "kubectl get pods" or "HOME")
	Value     string // Full value to include in context
	Selected  bool   // Whether to include in context
	SizeBytes int    // Size in bytes when serialized
}

// Collection holds all available context items.
type Collection struct {
	Items        []Item
	MaxSizeBytes int // Recommended max size (default 8KB)
}

// NewCollection creates a new context collection.
func NewCollection() *Collection {
	return &Collection{
		MaxSizeBytes: 8 * 1024, // 8KB default
	}
}

// Add adds an item to the collection.
func (c *Collection) Add(item Item) {
	item.SizeBytes = len(item.Value)
	c.Items = append(c.Items, item)
}

// SelectedSize returns the total size of selected items.
func (c *Collection) SelectedSize() int {
	total := 0
	for _, item := range c.Items {
		if item.Selected {
			total += item.SizeBytes
		}
	}
	return total
}

// SelectedItems returns only selected items.
func (c *Collection) SelectedItems() []Item {
	var selected []Item
	for _, item := range c.Items {
		if item.Selected {
			selected = append(selected, item)
		}
	}
	return selected
}

// Toggle toggles selection of an item by index.
func (c *Collection) Toggle(index int) {
	if index >= 0 && index < len(c.Items) {
		c.Items[index].Selected = !c.Items[index].Selected
	}
}

// Serialize returns the selected context as a string.
func (c *Collection) Serialize() string {
	var b strings.Builder

	// Group by category
	categories := make(map[Category][]Item)
	for _, item := range c.Items {
		if item.Selected {
			categories[item.Category] = append(categories[item.Category], item)
		}
	}

	// Output in order
	for _, cat := range []Category{CategoryAutoDetect, CategoryHistory, CategoryEnv, CategoryCustom} {
		items := categories[cat]
		if len(items) == 0 {
			continue
		}

		fmt.Fprintf(&b, "## %s\n", cat.String())
		for _, item := range items {
			if item.Key != "" {
				fmt.Fprintf(&b, "- %s: %s\n", item.Key, item.Value)
			} else {
				fmt.Fprintf(&b, "- %s\n", item.Value)
			}
		}
		fmt.Fprintln(&b)
	}

	return b.String()
}

// SizeStatus returns a status indicator for the current size.
// Returns "green", "yellow", or "red".
func (c *Collection) SizeStatus() string {
	size := c.SelectedSize()
	ratio := float64(size) / float64(c.MaxSizeBytes)

	if ratio < 0.5 {
		return "green"
	} else if ratio < 0.8 {
		return "yellow"
	}
	return "red"
}

// Clear removes all items from the collection.
func (c *Collection) Clear() {
	c.Items = nil
}

// SelectAll selects all items.
func (c *Collection) SelectAll() {
	for i := range c.Items {
		c.Items[i].Selected = true
	}
}

// DeselectAll deselects all items.
func (c *Collection) DeselectAll() {
	for i := range c.Items {
		c.Items[i].Selected = false
	}
}
