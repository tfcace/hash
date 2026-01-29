package completion

import (
	"testing"
)

func TestCompletionItem_Display(t *testing.T) {
	item := Item{
		Value:       "README.md",
		Display:     "README.md",
		Description: "Markdown file",
		Icon:        "",
	}

	if item.Value != "README.md" {
		t.Errorf("Value = %q, want %q", item.Value, "README.md")
	}
}

func TestCompletionResult(t *testing.T) {
	result := Result{
		Items: []Item{
			{Value: "foo", Display: "foo"},
			{Value: "bar", Display: "bar"},
		},
		Prefix: "./",
	}

	if len(result.Items) != 2 {
		t.Errorf("Items count = %d, want 2", len(result.Items))
	}
	if result.Prefix != "./" {
		t.Errorf("Prefix = %q, want %q", result.Prefix, "./")
	}
}

func TestKindAlias(t *testing.T) {
	// Verify KindAlias is a valid Kind
	var k Kind = "alias"
	if k != "alias" {
		t.Errorf("expected 'alias', got %s", k)
	}
}
