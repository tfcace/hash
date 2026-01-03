package context

import (
	"strings"
	"testing"
)

func TestContextItem_Fields(t *testing.T) {
	item := Item{
		Category:  CategoryHistory,
		Key:       "kubectl get pods",
		Value:     "kubectl get pods -n staging",
		Selected:  true,
		SizeBytes: 45,
	}

	if item.Category != CategoryHistory {
		t.Errorf("Category = %v, want %v", item.Category, CategoryHistory)
	}
	if !item.Selected {
		t.Error("Selected should be true")
	}
}

func TestContextCollection_TotalSize(t *testing.T) {
	collection := &Collection{
		Items: []Item{
			{SizeBytes: 100, Selected: true},
			{SizeBytes: 200, Selected: true},
			{SizeBytes: 300, Selected: false},
		},
	}

	size := collection.SelectedSize()
	if size != 300 {
		t.Errorf("SelectedSize = %d, want 300", size)
	}
}

func TestContextCollection_Serialize(t *testing.T) {
	collection := &Collection{
		Items: []Item{
			{Category: CategoryHistory, Key: "ls", Value: "ls -la", Selected: true},
			{Category: CategoryEnv, Key: "HOME", Value: "/home/user", Selected: true},
		},
	}

	output := collection.Serialize()
	if output == "" {
		t.Error("Serialize should not return empty")
	}
	if !strings.Contains(output, "ls -la") {
		t.Error("Serialize should contain history item")
	}
	if !strings.Contains(output, "/home/user") {
		t.Error("Serialize should contain env item")
	}
}

func TestContextCollection_Toggle(t *testing.T) {
	collection := NewCollection()
	collection.Add(Item{Category: CategoryHistory, Key: "ls", Value: "ls", Selected: false})

	if collection.Items[0].Selected {
		t.Error("Item should start unselected")
	}

	collection.Toggle(0)
	if !collection.Items[0].Selected {
		t.Error("Item should be selected after toggle")
	}

	collection.Toggle(0)
	if collection.Items[0].Selected {
		t.Error("Item should be unselected after second toggle")
	}
}

func TestContextCollection_SelectedItems(t *testing.T) {
	collection := &Collection{
		Items: []Item{
			{Value: "a", Selected: true},
			{Value: "b", Selected: false},
			{Value: "c", Selected: true},
		},
	}

	selected := collection.SelectedItems()
	if len(selected) != 2 {
		t.Errorf("SelectedItems count = %d, want 2", len(selected))
	}
}

func TestContextCollection_SizeStatus(t *testing.T) {
	tests := []struct {
		selected int
		max      int
		want     string
	}{
		{1000, 8192, "green"},  // < 50%
		{5000, 8192, "yellow"}, // 50-80%
		{7000, 8192, "red"},    // > 80%
	}

	for _, tt := range tests {
		collection := &Collection{
			Items:        []Item{{SizeBytes: tt.selected, Selected: true}},
			MaxSizeBytes: tt.max,
		}
		got := collection.SizeStatus()
		if got != tt.want {
			t.Errorf("SizeStatus() with %d/%d = %q, want %q", tt.selected, tt.max, got, tt.want)
		}
	}
}

func TestCategory_String(t *testing.T) {
	tests := []struct {
		cat  Category
		want string
	}{
		{CategoryHistory, "History"},
		{CategoryEnv, "Environment"},
		{CategoryAutoDetect, "Auto-detected"},
		{CategoryCustom, "Custom"},
	}

	for _, tt := range tests {
		got := tt.cat.String()
		if got != tt.want {
			t.Errorf("Category(%d).String() = %q, want %q", tt.cat, got, tt.want)
		}
	}
}
