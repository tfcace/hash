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
	if result.Hidden() != (Hidden{Uncounted: true}) {
		t.Fatalf("prefixFilterItems stopped at its limit without saying more were not shown")
	}
	if result.Items[0].Value != "item-0" || result.Items[len(result.Items)-1].Value != "item-199" {
		t.Fatalf("prefixFilterItems should preserve first matching values, got first=%q last=%q",
			result.Items[0].Value,
			result.Items[len(result.Items)-1].Value,
		)
	}
}

func TestResult_Outcome(t *testing.T) {
	items := []Item{{Value: "a"}}
	tests := []struct {
		name   string
		result Result
		want   Outcome
	}{
		{"zero value is not mine", Result{}, OutcomeNotMine},
		{"items are an answer", Result{Items: items}, OutcomeAnswer},
		{"pending", pendingResult(), OutcomePending},
		{"owned empty is no matches", ownedResult(nil), OutcomeNoMatches},
		{"owned with items is an answer", ownedResult(items), OutcomeAnswer},
	}
	for _, tt := range tests {
		if got := tt.result.Outcome(); got != tt.want {
			t.Errorf("%s: Outcome() = %v, want %v", tt.name, got, tt.want)
		}
	}
}
