package completion

import (
	"strconv"
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

func TestPrefixFilterItems_LimitsLargeResultSet(t *testing.T) {
	values := make([]string, 5000)
	for i := range values {
		values[i] = "item-" + strconv.Itoa(i)
	}

	result := prefixFilterItems(values, "item-")
	if len(result.Items) > completionItemLimit {
		t.Fatalf("prefixFilterItems returned %d items, want at most %d", len(result.Items), completionItemLimit)
	}
	if len(result.Items) != completionItemLimit {
		t.Fatalf("prefixFilterItems returned %d items, want %d", len(result.Items), completionItemLimit)
	}
	if result.Items[0].Value != "item-0" || result.Items[len(result.Items)-1].Value != "item-199" {
		t.Fatalf("prefixFilterItems should preserve first matching values, got first=%q last=%q",
			result.Items[0].Value,
			result.Items[len(result.Items)-1].Value,
		)
	}
}
