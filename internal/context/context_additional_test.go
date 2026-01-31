package context

import (
	"strings"
	"testing"
)

// TestTruncateString tests the truncateString helper function.
func TestTruncateString(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		maxLen int
		want   string
	}{
		{"empty string", "", 10, ""},
		{"under limit", "hello", 10, "hello"},
		{"exactly at limit", "hello", 5, "hello"},
		{"over limit with ellipsis", "hello world", 8, "hello..."},
		{"maxLen 3 no ellipsis", "hello", 3, "hel"},
		{"maxLen 2 no ellipsis", "hello", 2, "he"},
		{"maxLen 1 no ellipsis", "hello", 1, "h"},
		{"maxLen 0", "hello", 0, ""},
		{"unicode under limit", "hello world", 50, "hello world"},
		{"long string", strings.Repeat("a", 100), 10, "aaaaaaa..."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateString(tt.input, tt.maxLen)
			if got != tt.want {
				t.Errorf("truncateString(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
			}
		})
	}
}

// TestBuilder_WithLastOutput tests the WithLastOutput method.
func TestBuilder_WithLastOutput(t *testing.T) {
	t.Run("empty output ignored", func(t *testing.T) {
		collection := NewBuilder().WithLastOutput("").Build()
		for _, item := range collection.Items {
			if item.Key == "last output" {
				t.Error("Empty output should not be added")
			}
		}
	})

	t.Run("normal output added", func(t *testing.T) {
		collection := NewBuilder().WithLastOutput("some output").Build()
		found := false
		for _, item := range collection.Items {
			if item.Key == "last output" {
				found = true
				if item.Value != "some output" {
					t.Errorf("Value = %q, want %q", item.Value, "some output")
				}
				if item.Selected {
					t.Error("last output should not be selected by default")
				}
			}
		}
		if !found {
			t.Error("WithLastOutput should add 'last output' item")
		}
	})

	t.Run("long output truncated", func(t *testing.T) {
		longOutput := strings.Repeat("x", 2000)
		collection := NewBuilder().WithLastOutput(longOutput).Build()
		for _, item := range collection.Items {
			if item.Key == "last output" {
				if len(item.Value) > 1000 {
					t.Errorf("Output should be truncated to 1000, got %d", len(item.Value))
				}
				if !strings.HasSuffix(item.Value, "...") {
					t.Error("Truncated output should end with ...")
				}
			}
		}
	})
}

// TestBuilder_WithLastError tests the WithLastError method.
func TestBuilder_WithLastError(t *testing.T) {
	t.Run("empty error ignored", func(t *testing.T) {
		collection := NewBuilder().WithLastError("").Build()
		for _, item := range collection.Items {
			if item.Key == "last error" {
				t.Error("Empty error should not be added")
			}
		}
	})

	t.Run("error is selected by default", func(t *testing.T) {
		collection := NewBuilder().WithLastError("some error").Build()
		for _, item := range collection.Items {
			if item.Key == "last error" {
				if !item.Selected {
					t.Error("last error should be selected by default")
				}
			}
		}
	})

	t.Run("long error truncated", func(t *testing.T) {
		longError := strings.Repeat("e", 1000)
		collection := NewBuilder().WithLastError(longError).Build()
		for _, item := range collection.Items {
			if item.Key == "last error" {
				if len(item.Value) > 500 {
					t.Errorf("Error should be truncated to 500, got %d", len(item.Value))
				}
			}
		}
	})
}

// TestBuilder_WithHistoryLimit_EdgeCases tests edge cases for history limit.
func TestBuilder_WithHistoryLimit_EdgeCases(t *testing.T) {
	t.Run("empty history", func(t *testing.T) {
		collection := NewBuilder().WithHistoryLimit(nil, 10).Build()
		count := 0
		for _, item := range collection.Items {
			if item.Category == CategoryHistory {
				count++
			}
		}
		if count != 0 {
			t.Errorf("Empty history should produce 0 items, got %d", count)
		}
	})

	t.Run("history smaller than limit", func(t *testing.T) {
		collection := NewBuilder().WithHistoryLimit([]string{"a", "b"}, 10).Build()
		count := 0
		for _, item := range collection.Items {
			if item.Category == CategoryHistory {
				count++
			}
		}
		if count != 2 {
			t.Errorf("Should have 2 items, got %d", count)
		}
	})

	t.Run("history exactly at limit", func(t *testing.T) {
		collection := NewBuilder().WithHistoryLimit([]string{"a", "b", "c"}, 3).Build()
		count := 0
		for _, item := range collection.Items {
			if item.Category == CategoryHistory {
				count++
			}
		}
		if count != 3 {
			t.Errorf("Should have 3 items, got %d", count)
		}
	})

	t.Run("limit 0", func(t *testing.T) {
		collection := NewBuilder().WithHistoryLimit([]string{"a", "b", "c"}, 0).Build()
		count := 0
		for _, item := range collection.Items {
			if item.Category == CategoryHistory {
				count++
			}
		}
		if count != 0 {
			t.Errorf("Limit 0 should produce 0 items, got %d", count)
		}
	})
}

// TestCollection_SizeStatus_Boundaries tests size status thresholds.
func TestCollection_SizeStatus_Boundaries(t *testing.T) {
	tests := []struct {
		name       string
		maxSize    int
		itemSize   int
		wantStatus string
	}{
		{"0% is green", 100, 0, "green"},
		{"49% is green", 100, 49, "green"},
		{"50% is yellow", 100, 50, "yellow"},
		{"79% is yellow", 100, 79, "yellow"},
		{"80% is red", 100, 80, "red"},
		{"100% is red", 100, 100, "red"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewCollection()
			c.MaxSizeBytes = tt.maxSize
			if tt.itemSize > 0 {
				// Add an item with the specified size
				c.Add(Item{
					Category: CategoryCustom,
					Key:      "test",
					Value:    strings.Repeat("x", tt.itemSize),
					Selected: true,
				})
			}

			got := c.SizeStatus()
			if got != tt.wantStatus {
				t.Errorf("SizeStatus() = %q, want %q (size=%d, max=%d)",
					got, tt.wantStatus, c.SelectedSize(), c.MaxSizeBytes)
			}
		})
	}
}

// TestCollection_Toggle_Bounds tests toggle with out of bounds index.
func TestCollection_Toggle_Bounds(t *testing.T) {
	c := NewCollection()
	c.Add(Item{Category: CategoryCustom, Key: "test", Value: "value", Selected: false})

	// Should not panic with out of bounds
	c.Toggle(-1)
	c.Toggle(100)

	// Original item should be unchanged
	if c.Items[0].Selected {
		t.Error("Item should still be unselected after out-of-bounds toggle")
	}

	// Valid toggle should work
	c.Toggle(0)
	if !c.Items[0].Selected {
		t.Error("Item should be selected after valid toggle")
	}
}

// TestCollection_SelectDeselectAll tests bulk selection operations.
func TestCollection_SelectDeselectAll(t *testing.T) {
	c := NewCollection()
	c.Add(Item{Category: CategoryCustom, Key: "a", Value: "1", Selected: false})
	c.Add(Item{Category: CategoryCustom, Key: "b", Value: "2", Selected: true})
	c.Add(Item{Category: CategoryCustom, Key: "c", Value: "3", Selected: false})

	c.SelectAll()
	for i, item := range c.Items {
		if !item.Selected {
			t.Errorf("Item %d should be selected after SelectAll", i)
		}
	}

	c.DeselectAll()
	for i, item := range c.Items {
		if item.Selected {
			t.Errorf("Item %d should be deselected after DeselectAll", i)
		}
	}
}

// TestCollection_Clear tests clearing the collection.
func TestCollection_Clear(t *testing.T) {
	c := NewCollection()
	c.Add(Item{Category: CategoryCustom, Key: "test", Value: "value"})
	c.Add(Item{Category: CategoryCustom, Key: "test2", Value: "value2"})

	c.Clear()

	if len(c.Items) != 0 {
		t.Errorf("Clear should remove all items, got %d", len(c.Items))
	}
}

// TestCollection_SelectedItems tests filtering selected items.
func TestCollection_SelectedItems(t *testing.T) {
	c := NewCollection()
	c.Add(Item{Category: CategoryCustom, Key: "a", Value: "1", Selected: true})
	c.Add(Item{Category: CategoryCustom, Key: "b", Value: "2", Selected: false})
	c.Add(Item{Category: CategoryCustom, Key: "c", Value: "3", Selected: true})

	selected := c.SelectedItems()
	if len(selected) != 2 {
		t.Errorf("SelectedItems() returned %d items, want 2", len(selected))
	}

	for _, item := range selected {
		if !item.Selected {
			t.Error("SelectedItems returned unselected item")
		}
	}
}

// TestCollection_Serialize_CategoryOrder tests category ordering in serialization.
func TestCollection_Serialize_CategoryOrder(t *testing.T) {
	c := NewCollection()
	// Add items in reverse category order
	c.Add(Item{Category: CategoryCustom, Key: "custom", Value: "val", Selected: true})
	c.Add(Item{Category: CategoryEnv, Key: "env", Value: "val", Selected: true})
	c.Add(Item{Category: CategoryHistory, Key: "history", Value: "val", Selected: true})
	c.Add(Item{Category: CategoryAutoDetect, Key: "auto", Value: "val", Selected: true})

	serialized := c.Serialize()

	// AutoDetect should come before History
	autoIdx := strings.Index(serialized, "Auto-detected")
	histIdx := strings.Index(serialized, "History")
	envIdx := strings.Index(serialized, "Environment")
	customIdx := strings.Index(serialized, "Custom")

	if autoIdx == -1 || histIdx == -1 || envIdx == -1 || customIdx == -1 {
		t.Fatalf("Serialized output missing categories: %s", serialized)
	}

	if autoIdx > histIdx {
		t.Error("AutoDetect should come before History")
	}
	if histIdx > envIdx {
		t.Error("History should come before Environment")
	}
	if envIdx > customIdx {
		t.Error("Environment should come before Custom")
	}
}

// TestCollection_Add_SizeCalculation tests automatic size calculation.
func TestCollection_Add_SizeCalculation(t *testing.T) {
	c := NewCollection()
	c.Add(Item{Category: CategoryCustom, Key: "test", Value: "hello"})

	if c.Items[0].SizeBytes != 5 {
		t.Errorf("SizeBytes = %d, want 5", c.Items[0].SizeBytes)
	}

	c.Add(Item{Category: CategoryCustom, Key: "test2", Value: "world!"})
	if c.Items[1].SizeBytes != 6 {
		t.Errorf("SizeBytes = %d, want 6", c.Items[1].SizeBytes)
	}
}
