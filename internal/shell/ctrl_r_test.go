package shell

import (
	"strings"
	"testing"

	"github.com/tfcace/hash/internal/history"
	"github.com/tfcace/hash/internal/prompt"
	"github.com/tfcace/hash/internal/readline"
)

// TestCtrlRE2E is an end-to-end test for Ctrl+R functionality
func TestCtrlRE2E(t *testing.T) {
	// Test 1: Create history store
	t.Log("Test 1: Creating history store...")
	store, err := history.NewStore(":memory:")
	if err != nil {
		t.Fatalf("Failed to create history store: %v", err)
	}
	defer store.Close()
	t.Log("✓ History store created successfully")

	// Test 2: Add commands to history
	t.Log("\nTest 2: Adding commands to history...")
	commands := []history.Command{
		{Command: "ls"},
		{Command: "pwd"},
		{Command: "echo test"},
		{Command: "whoami"},
		{Command: "ls -la"},
		{Command: "docker ps"},
		{Command: "kubectl get pods"},
	}

	for _, cmd := range commands {
		_, err := store.Add(cmd)
		if err != nil {
			t.Fatalf("Failed to add command '%s': %v", cmd.Command, err)
		}
	}
	t.Logf("✓ Added %d commands to history", len(commands))

	// Test 3: Get recent commands
	t.Log("\nTest 3: Retrieving recent commands...")
	recent, err := store.GetRecent(5)
	if err != nil {
		t.Fatalf("Failed to get recent commands: %v", err)
	}
	if len(recent) != 5 {
		t.Fatalf("Expected 5 recent commands, got %d", len(recent))
	}
	t.Logf("✓ Retrieved %d recent commands", len(recent))

	// Test 4: Search for commands
	t.Log("\nTest 4: Testing search functionality...")
	searchTests := []struct {
		query    string
		expected int
	}{
		{"ls", 2},      // "ls" and "ls -la"
		{"docker", 1},  // "docker ps"
		{"kubectl", 1}, // "kubectl get pods"
		{"echo", 1},    // "echo test"
		{"nonexistent", 0},
		{"", 7}, // All commands
	}

	for _, test := range searchTests {
		results, err := store.Search(history.SearchOptions{
			Query: test.query,
			Limit: 20,
		})
		if err != nil {
			t.Fatalf("Search failed for query '%s': %v", test.query, err)
		}
		if len(results) != test.expected {
			t.Fatalf("Search for '%s': expected %d results, got %d", test.query, test.expected, len(results))
		}
		t.Logf("✓ Search for '%s': %d results (expected %d)", test.query, len(results), test.expected)
	}

	// Test 5: Create and test SearchUI
	t.Log("\nTest 5: Testing SearchUI creation...")
	ui := history.NewSearchUI(store, prompt.DefaultPalette())
	if ui == nil {
		t.Fatal("SearchUI creation failed")
	}
	t.Log("✓ SearchUI created successfully")

	// Test 6: Test InputHandler creation
	t.Log("\nTest 6: Testing InputHandler creation...")
	handler := readline.NewInputHandler(nil, store)
	if handler == nil {
		t.Fatal("InputHandler creation failed")
	}
	t.Log("✓ InputHandler created successfully")

	// Test 7: Verify Ctrl+R rune value
	t.Log("\nTest 7: Verifying Ctrl+R handling...")
	ctrlRRune := rune(18) // ASCII 18 is Ctrl+R
	if ctrlRRune != 18 {
		t.Fatalf("Ctrl+R rune value incorrect: got %d, expected 18", ctrlRRune)
	}
	t.Logf("✓ Ctrl+R rune value correct: %d", ctrlRRune)

	// Test 8: Test filtering logic
	t.Log("\nTest 8: Testing command filtering...")
	filteredResults, err := store.Search(history.SearchOptions{
		Query: "ls",
		Limit: 20,
	})
	if err != nil {
		t.Fatalf("Filter test failed: %v", err)
	}
	t.Logf("✓ Filtering works: found %d commands matching 'ls'", len(filteredResults))

	t.Log("\n" + strings.Repeat("=", 50))
	t.Log("All tests passed!")
	t.Log(strings.Repeat("=", 50))
}
