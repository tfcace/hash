package context

import (
	"testing"
)

func TestPicker_Create(t *testing.T) {
	collection := NewCollection()
	collection.Add(Item{Category: CategoryHistory, Key: "ls", Value: "ls -la"})

	picker := NewPicker(collection)
	if picker == nil {
		t.Fatal("NewPicker() returned nil")
	}
}

func TestPicker_Navigation(t *testing.T) {
	collection := NewCollection()
	collection.Add(Item{Category: CategoryHistory, Key: "a", Value: "a"})
	collection.Add(Item{Category: CategoryHistory, Key: "b", Value: "b"})
	collection.Add(Item{Category: CategoryHistory, Key: "c", Value: "c"})

	picker := NewPicker(collection)

	if picker.Cursor() != 0 {
		t.Errorf("cursor = %d, want 0", picker.Cursor())
	}

	picker.MoveDown()
	if picker.Cursor() != 1 {
		t.Errorf("cursor = %d, want 1", picker.Cursor())
	}

	picker.MoveUp()
	if picker.Cursor() != 0 {
		t.Errorf("cursor = %d, want 0", picker.Cursor())
	}
}

func TestPicker_NavigationBounds(t *testing.T) {
	collection := NewCollection()
	collection.Add(Item{Category: CategoryHistory, Key: "a", Value: "a"})
	collection.Add(Item{Category: CategoryHistory, Key: "b", Value: "b"})

	picker := NewPicker(collection)

	// Try to move up at top - should stay at 0
	picker.MoveUp()
	if picker.Cursor() != 0 {
		t.Errorf("cursor = %d, want 0 (should not go negative)", picker.Cursor())
	}

	// Move to bottom
	picker.MoveDown()
	picker.MoveDown()
	// Should be clamped at last item (index 1)
	if picker.Cursor() != 1 {
		t.Errorf("cursor = %d, want 1 (should not exceed bounds)", picker.Cursor())
	}
}

func TestPicker_Toggle(t *testing.T) {
	collection := NewCollection()
	collection.Add(Item{Category: CategoryHistory, Key: "a", Value: "a", Selected: false})

	picker := NewPicker(collection)

	if picker.Collection().Items[0].Selected {
		t.Error("Item should start unselected")
	}

	picker.ToggleCurrent()

	if !picker.Collection().Items[0].Selected {
		t.Error("Item should be selected after toggle")
	}
}

func TestPicker_EmptyCollection(t *testing.T) {
	collection := NewCollection()
	picker := NewPicker(collection)

	// Should not panic on empty collection
	picker.MoveDown()
	picker.MoveUp()
	picker.ToggleCurrent()

	if picker.Cursor() != 0 {
		t.Errorf("cursor = %d, want 0", picker.Cursor())
	}
}
