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

func TestFuzzyMatch_WordBoundary(t *testing.T) {
	// This test ensures that "Drive/" does NOT match "Google Drive/"
	// because the match doesn't start at a word boundary.
	// This prevents the bug where completing "cd Google Drive/" and pressing
	// TAB again would keep prepending "Google" to the path.
	items := []Item{
		{Value: "Google Drive/", Display: "Google Drive"},
		{Value: "Other/", Display: "Other"},
	}

	result := FuzzyFilter(items, "Drive/")

	// "Drive/" should NOT match "Google Drive/" because it's mid-word
	if len(result) != 0 {
		t.Errorf("Count = %d, want 0 (Drive/ should not match Google Drive/)", len(result))
		for _, item := range result {
			t.Errorf("  Matched: %q", item.Value)
		}
	}
}

func TestFuzzyMatch_WordBoundaryAfterSpace(t *testing.T) {
	// Word boundary matches get higher scores than subsequence matches.
	// "bar" should match both "foo bar" and "foobar", but "foo bar"
	// scores higher because "bar" is at a word boundary.
	items := []Item{
		{Value: "foo bar", Display: "foo bar"},
		{Value: "foobar", Display: "foobar"},
	}

	result := FuzzyFilter(items, "bar")

	// Both should match (word boundary + subsequence)
	if len(result) != 2 {
		t.Errorf("Count = %d, want 2", len(result))
	}
	// But "foo bar" should be first (word boundary = higher score)
	if len(result) > 0 && result[0].Value != "foo bar" {
		t.Errorf("First = %q, want %q (word boundary should score higher)", result[0].Value, "foo bar")
	}
}

func TestFuzzyMatch_WordBoundaryAfterSlash(t *testing.T) {
	// Matches AFTER a slash ARE valid word boundaries (for paths).
	// Both should match, but word boundary gets higher score.
	items := []Item{
		{Value: "src/main.go", Display: "src/main.go"},
		{Value: "srcmain.go", Display: "srcmain.go"},
	}

	result := FuzzyFilter(items, "main")

	// Both should match
	if len(result) != 2 {
		t.Errorf("Count = %d, want 2", len(result))
	}
	// But "src/main.go" should be first (word boundary after / = higher score)
	if len(result) > 0 && result[0].Value != "src/main.go" {
		t.Errorf("First = %q, want %q (word boundary should score higher)", result[0].Value, "src/main.go")
	}
}

func TestFuzzyMatch_SingleCharQuery(t *testing.T) {
	items := []Item{
		{Value: "apple", Display: "apple"},
		{Value: "banana", Display: "banana"},
		{Value: "cherry", Display: "cherry"},
	}

	result := FuzzyFilter(items, "a")

	// Should match "apple" (prefix) and "banana" (contains 'a')
	if len(result) < 1 {
		t.Errorf("Single char query should match, got %d results", len(result))
	}
	// "apple" should be first (prefix match > subsequence)
	if result[0].Value != "apple" {
		t.Errorf("First = %q, want %q (prefix should win)", result[0].Value, "apple")
	}
}

func TestFuzzyMatch_QueryLongerThanCandidate(t *testing.T) {
	items := []Item{
		{Value: "ab", Display: "ab"},
		{Value: "abc", Display: "abc"},
	}

	result := FuzzyFilter(items, "abcdef")

	// Query longer than all candidates - no match possible
	if len(result) != 0 {
		t.Errorf("Query longer than candidates should not match, got %d results", len(result))
	}
}

func TestFuzzyMatch_SpecialCharacters(t *testing.T) {
	items := []Item{
		{Value: "file-name.txt", Display: "file-name.txt"},
		{Value: "file_name.go", Display: "file_name.go"},
		{Value: "file.name.md", Display: "file.name.md"},
	}

	// Hyphen in query
	result := FuzzyFilter(items, "file-")
	if len(result) < 1 || result[0].Value != "file-name.txt" {
		t.Errorf("Should match hyphenated filename, got %v", result)
	}

	// Underscore in query
	result2 := FuzzyFilter(items, "file_")
	if len(result2) < 1 || result2[0].Value != "file_name.go" {
		t.Errorf("Should match underscored filename, got %v", result2)
	}
}

func TestFuzzyMatch_NumericPatterns(t *testing.T) {
	items := []Item{
		{Value: "file1.txt", Display: "file1.txt"},
		{Value: "file2.txt", Display: "file2.txt"},
		{Value: "file10.txt", Display: "file10.txt"},
	}

	result := FuzzyFilter(items, "f1")

	// Should match file1 and file10 via subsequence
	if len(result) < 2 {
		t.Errorf("Expected at least 2 matches for 'f1', got %d", len(result))
	}
}

func TestFuzzyMatch_ScoreOrdering(t *testing.T) {
	// Test that exact > prefix > word-boundary > subsequence
	items := []Item{
		{Value: "test", Display: "test"},       // exact
		{Value: "testing", Display: "testing"}, // prefix
		{Value: "my test", Display: "my test"}, // word boundary
		{Value: "atestb", Display: "atestb"},   // contains (no word boundary)
		{Value: "t_e_s_t", Display: "t_e_s_t"}, // subsequence
	}

	result := FuzzyFilter(items, "test")

	if len(result) < 3 {
		t.Fatalf("Expected at least 3 matches, got %d", len(result))
	}

	// Exact match should be first
	if result[0].Value != "test" {
		t.Errorf("First should be exact match 'test', got %q", result[0].Value)
	}

	// Prefix should be second
	if result[1].Value != "testing" {
		t.Errorf("Second should be prefix match 'testing', got %q", result[1].Value)
	}

	// Word boundary should be third
	if result[2].Value != "my test" {
		t.Errorf("Third should be word-boundary match 'my test', got %q", result[2].Value)
	}
}

func TestFuzzyMatch_ConsecutiveBonus(t *testing.T) {
	// "abc" matching: "abc" in a row vs spread out
	items := []Item{
		{Value: "a_b_c", Display: "a_b_c"}, // spread out
		{Value: "xabcy", Display: "xabcy"}, // consecutive
	}

	result := FuzzyFilter(items, "abc")

	if len(result) != 2 {
		t.Fatalf("Expected 2 matches, got %d", len(result))
	}

	// Consecutive match should score higher
	if result[0].Value != "xabcy" {
		t.Errorf("Consecutive match should score higher, first was %q", result[0].Value)
	}
}

func TestFuzzyMatch_ShorterCandidatePreferred(t *testing.T) {
	// When scores are similar, shorter candidates win
	items := []Item{
		{Value: "config.yaml", Display: "config.yaml"},
		{Value: "configuration.yaml", Display: "configuration.yaml"},
	}

	result := FuzzyFilter(items, "config")

	if len(result) != 2 {
		t.Fatalf("Expected 2 matches, got %d", len(result))
	}

	// Shorter match should be first (same prefix, but shorter is better)
	if result[0].Value != "config.yaml" {
		t.Errorf("Shorter candidate should be preferred, first was %q", result[0].Value)
	}
}

func TestFuzzyMatch_PathWithExtension(t *testing.T) {
	// Common use case: filtering by extension pattern
	items := []Item{
		{Value: "main.go", Display: "main.go"},
		{Value: "main_test.go", Display: "main_test.go"},
		{Value: "main.py", Display: "main.py"},
	}

	result := FuzzyFilter(items, "mgo")

	// Should match Go files via subsequence (m-a-i-n-.-g-o)
	found := false
	for _, item := range result {
		if item.Value == "main.go" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("'mgo' should match 'main.go', got %v", result)
	}
}
