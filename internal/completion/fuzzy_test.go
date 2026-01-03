package completion

import (
	"testing"
)

func TestFuzzyMatch_ExactPrefix(t *testing.T) {
	items := []Item{
		{Value: "foo", Display: "foo"},
		{Value: "bar", Display: "bar"},
		{Value: "foobar", Display: "foobar"},
	}

	result := FuzzyFilter(items, "foo")

	if len(result) != 2 {
		t.Errorf("Count = %d, want 2", len(result))
	}
	// "foo" should score higher than "foobar" (exact vs prefix)
	if result[0].Value != "foo" {
		t.Errorf("First = %q, want %q", result[0].Value, "foo")
	}
}

func TestFuzzyMatch_Subsequence(t *testing.T) {
	items := []Item{
		{Value: "kubectl", Display: "kubectl"},
		{Value: "kubeadm", Display: "kubeadm"},
		{Value: "kubelet", Display: "kubelet"},
	}

	result := FuzzyFilter(items, "kctl")

	if len(result) != 1 {
		t.Errorf("Count = %d, want 1", len(result))
	}
	if result[0].Value != "kubectl" {
		t.Errorf("Value = %q, want %q", result[0].Value, "kubectl")
	}
}

func TestFuzzyMatch_CaseInsensitive(t *testing.T) {
	items := []Item{
		{Value: "README.md", Display: "README.md"},
		{Value: "readme.txt", Display: "readme.txt"},
	}

	result := FuzzyFilter(items, "readme")

	if len(result) != 2 {
		t.Errorf("Count = %d, want 2", len(result))
	}
}

func TestFuzzyMatch_NoMatch(t *testing.T) {
	items := []Item{
		{Value: "foo", Display: "foo"},
		{Value: "bar", Display: "bar"},
	}

	result := FuzzyFilter(items, "xyz")

	if len(result) != 0 {
		t.Errorf("Count = %d, want 0", len(result))
	}
}

func TestFuzzyMatch_EmptyQuery(t *testing.T) {
	items := []Item{
		{Value: "foo", Display: "foo"},
		{Value: "bar", Display: "bar"},
	}

	result := FuzzyFilter(items, "")

	if len(result) != 2 {
		t.Errorf("Count = %d, want 2", len(result))
	}
}
