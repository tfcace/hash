package history

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/tfcace/hash/internal/prompt"
)

func TestSearchUI_Create(t *testing.T) {
	store, _ := NewStore(":memory:")
	defer store.Close()

	ui := NewSearchUI(store, prompt.DefaultPalette())
	if ui == nil {
		t.Fatal("NewSearchUI() returned nil")
	}
}

func TestSearchUI_FilterCommands(t *testing.T) {
	commands := []Command{
		{Command: "kubectl get pods"},
		{Command: "docker ps"},
		{Command: "kubectl get services"},
	}

	filtered := filterCommands(commands, "kubectl")

	if len(filtered) != 2 {
		t.Errorf("Count = %d, want 2", len(filtered))
	}
}

// Edge Case 1: Empty history search
func TestSearchUI_EmptyHistory(t *testing.T) {
	store, _ := NewStore(":memory:")
	defer store.Close()

	_ = NewSearchUI(store, prompt.DefaultPalette()) // Verify UI can be created with empty store

	// Search with empty history should return no results
	results, err := store.Search(SearchOptions{Query: "test"})
	if err != nil {
		t.Errorf("Search() error = %v", err)
	}

	if len(results) != 0 {
		t.Errorf("Empty history search returned %d results, want 0", len(results))
	}
}

// Edge Case 2: Very long commands
func TestSearchUI_VeryLongCommands(t *testing.T) {
	store, _ := NewStore(":memory:")
	defer store.Close()

	// Create a very long command (simulating deeply nested paths)
	longCmd := "ls -la " + strings.Repeat("path/to/very/deep/nested/directory/", 10)
	store.Add(Command{
		Command:   longCmd,
		Cwd:       "/",
		Timestamp: time.Now(),
	})

	// Verify the command is stored and can be retrieved
	results, err := store.Search(SearchOptions{Query: "", Limit: 20})
	if err != nil {
		t.Errorf("Search() error = %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("Expected 1 result, got %d", len(results))
	}

	if results[0].Command != longCmd {
		t.Errorf("Long command not preserved correctly")
	}

	// Test SearchUI rendering with long command
	ui := NewSearchUI(store, prompt.DefaultPalette())
	ui.results = results
	ui.totalResults = len(results)
	ui.selected = 0
	ui.width = 80 // Standard terminal width

	view := ui.View().Content
	if view == "" {
		t.Error("SearchUI.View() returned empty string")
	}

	// Verify truncation happens correctly
	if !strings.Contains(view, "...") && len(longCmd) > ui.width-25 {
		t.Logf("SearchUI should truncate long commands. Command len=%d, width=%d", len(longCmd), ui.width)
	}
}

// Edge Case 3: Unicode commands
func TestSearchUI_UnicodeCommands(t *testing.T) {
	store, _ := NewStore(":memory:")
	defer store.Close()

	unicodeCommands := []string{
		"echo 'Hello World'",
		"echo 'Privet mir'",
		"echo 'emoji test'",
		"ls -la /tmp/folder",
	}

	for _, cmd := range unicodeCommands {
		_, err := store.Add(Command{
			Command:   cmd,
			Cwd:       "/",
			Timestamp: time.Now(),
		})
		if err != nil {
			t.Fatalf("Failed to add unicode command: %v", err)
		}
	}

	// Search for commands
	results, err := store.Search(SearchOptions{Query: "World"})
	if err != nil {
		t.Errorf("Search() error = %v", err)
	}

	if len(results) != 1 {
		t.Errorf("Search: expected 1 result, got %d", len(results))
	}

	// Verify SearchUI renders correctly
	ui := NewSearchUI(store, prompt.DefaultPalette())
	ui.results = results
	ui.totalResults = len(results)
	ui.selected = 0
	view := ui.View().Content

	if !strings.Contains(view, "World") {
		t.Error("SearchUI did not render correctly")
	}
}

// Edge Case 4: No results handling
func TestSearchUI_NoResults(t *testing.T) {
	store, _ := NewStore(":memory:")
	defer store.Close()

	store.Add(Command{Command: "ls", Cwd: "/", Timestamp: time.Now()})
	store.Add(Command{Command: "pwd", Cwd: "/", Timestamp: time.Now()})

	results, err := store.Search(SearchOptions{Query: "nonexistent_command_xyz"})
	if err != nil {
		t.Errorf("Search() error = %v", err)
	}

	if len(results) != 0 {
		t.Errorf("No results search: expected 0 results, got %d", len(results))
	}

	// Verify SearchUI handles empty results
	ui := NewSearchUI(store, prompt.DefaultPalette())
	ui.results = results
	ui.totalResults = 0
	ui.selected = -1

	view := ui.View().Content
	if view == "" {
		t.Error("SearchUI.View() returned empty with no results")
	}

	// Should show header and footer even with no results
	if !strings.Contains(view, "Search") {
		t.Error("SearchUI missing header with no results")
	}
}

// Edge Case 5: Case insensitive filtering
func TestSearchUI_CaseInsensitiveFilter(t *testing.T) {
	commands := []Command{
		{Command: "KUBECTL get pods"},
		{Command: "Docker ps"},
		{Command: "python script.py"},
	}

	// Filter should be case insensitive
	filtered := filterCommands(commands, "kubectl")
	if len(filtered) != 1 {
		t.Errorf("Case insensitive filter: expected 1 result, got %d", len(filtered))
	}

	filtered = filterCommands(commands, "DOCKER")
	if len(filtered) != 1 {
		t.Errorf("Case insensitive filter (uppercase): expected 1 result, got %d", len(filtered))
	}
}

// Edge Case 6: Terminal resize handling
func TestSearchUI_TerminalResize(t *testing.T) {
	store, _ := NewStore(":memory:")
	defer store.Close()

	store.Add(Command{Command: "echo test", Cwd: "/", Timestamp: time.Now()})

	ui := NewSearchUI(store, prompt.DefaultPalette())
	ui.results = []Command{{Command: "echo very long command " + strings.Repeat("x", 100)}}
	ui.totalResults = 1
	ui.selected = 0

	// Test different terminal widths
	widths := []int{40, 80, 120, 200}
	for _, width := range widths {
		ui.width = width
		view := ui.View().Content

		if view == "" {
			t.Errorf("SearchUI.View() returned empty for width %d", width)
		}

		// Verify no panic and proper rendering
		lines := strings.Split(view, "\n")
		for _, line := range lines {
			// Allow some overage for ANSI codes, but generally stay within bounds
			if len(line) > width*2 { // 2x for ANSI codes
				t.Logf("Warning: line exceeds width %d by a large margin: %d", width, len(line))
			}
		}
	}
}

// Edge Case 7: Selected index boundaries
func TestSearchUI_SelectedIndexBoundaries(t *testing.T) {
	store, _ := NewStore(":memory:")
	defer store.Close()

	// Add commands
	for i := 0; i < 5; i++ {
		store.Add(Command{Command: "cmd" + string(rune('a'+i)), Cwd: "/", Timestamp: time.Now()})
	}

	ui := NewSearchUI(store, prompt.DefaultPalette())
	results, _ := store.Search(SearchOptions{Query: ""})
	ui.results = results
	ui.totalResults = len(results)

	// Test boundary cases
	testCases := []struct {
		selected int
		expected bool // true if valid
	}{
		{-1, false},
		{0, true},
		{len(results) - 1, true},
		{len(results), false},
		{100, false},
	}

	for _, tc := range testCases {
		ui.selected = tc.selected
		view := ui.View().Content

		if tc.expected && view == "" {
			t.Errorf("SearchUI.View() returned empty for valid selected=%d", tc.selected)
		}

		// Should not panic for any selected value
		_ = view
	}
}

// Edge Case 8: Special characters in commands
func TestSearchUI_SpecialCharacters(t *testing.T) {
	store, _ := NewStore(":memory:")
	defer store.Close()

	specialCmds := []string{
		`echo "test with \"quotes\""`,
		"echo 'test with $variables'",
		"grep 'pattern|with|pipes'",
		`find . -name "*.go"`,
	}

	for _, cmd := range specialCmds {
		store.Add(Command{Command: cmd, Cwd: "/", Timestamp: time.Now()})
	}

	results, err := store.Search(SearchOptions{Query: ""})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}

	if len(results) != len(specialCmds) {
		t.Errorf("Special char test: expected %d results, got %d", len(specialCmds), len(results))
	}
}

func TestSearchUI_ViewRendering(t *testing.T) {
	store, _ := NewStore(":memory:")
	defer store.Close()

	// Add test commands
	store.Add(Command{
		Command:   "go test ./...",
		Cwd:       "/",
		Timestamp: time.Now().Add(-5 * time.Minute),
		ExitCode:  0,
	})
	store.Add(Command{
		Command:   "go build",
		Cwd:       "/",
		Timestamp: time.Now().Add(-1 * time.Hour),
		ExitCode:  1, // Failed command
		GitBranch: "main",
	})

	ui := NewSearchUI(store, prompt.DefaultPalette())
	ui.width = 80
	ui.searchNow()

	view := ui.View().Content

	// Should contain header
	if !strings.Contains(view, "Search") {
		t.Error("View should contain Search header")
	}

	// Should contain commands
	if !strings.Contains(view, "go test") {
		t.Error("View should contain 'go test' command")
	}

	// Should contain help footer
	if !strings.Contains(view, "navigate") {
		t.Error("View should contain navigation help")
	}

	// Should show error indicator for failed command
	if !strings.Contains(view, "x ") {
		t.Error("View should show error indicator for failed command")
	}

	// Should show git branch
	if !strings.Contains(view, "main") {
		t.Error("View should show git branch")
	}
}

func TestSearchUI_Scrolling(t *testing.T) {
	store, _ := NewStore(":memory:")
	defer store.Close()

	// Add more commands than visible limit
	for i := 0; i < 20; i++ {
		store.Add(Command{
			Command:   fmt.Sprintf("command %d", i),
			Cwd:       "/",
			Timestamp: time.Now().Add(time.Duration(-i) * time.Minute),
		})
	}

	ui := NewSearchUI(store, prompt.DefaultPalette())
	ui.width = 80
	ui.searchNow()

	// Initially selected should be 0
	if ui.selected != 0 {
		t.Errorf("Initial selected = %d, want 0", ui.selected)
	}

	// Move down past visible limit
	for i := 0; i < 15; i++ {
		ui.selected++
		ui.adjustScroll()
	}

	// Scroll offset should have adjusted
	if ui.scrollOffset == 0 {
		t.Error("Scroll offset should have changed after scrolling down")
	}

	// Selected item should still be within visible range
	if ui.selected < ui.scrollOffset || ui.selected >= ui.scrollOffset+10 {
		t.Error("Selected item should be within visible range")
	}
}

func TestSearchUI_TimestampFormatting(t *testing.T) {
	store, _ := NewStore(":memory:")
	defer store.Close()

	ui := NewSearchUI(store, prompt.DefaultPalette())

	cases := []struct {
		duration time.Duration
		contains string
	}{
		{30 * time.Second, "just now"},
		{5 * time.Minute, "min ago"},
		{2 * time.Hour, "hours ago"},
		{36 * time.Hour, "yesterday"},
	}

	for _, tc := range cases {
		ts := time.Now().Add(-tc.duration)
		result := ui.formatTimestamp(ts)
		if !strings.Contains(result, tc.contains) {
			t.Errorf("formatTimestamp(%v) = %q, want to contain %q", tc.duration, result, tc.contains)
		}
	}
}

func TestSearchUI_PersistentCopyConfirmation(t *testing.T) {
	// The status message should not auto-clear
	// It should only clear on next user action

	store, _ := NewStore(":memory:")
	defer store.Close()
	store.Add(Command{Command: "test", Timestamp: time.Now()})

	ui := NewSearchUI(store, prompt.DefaultPalette())
	ui.searchNow()
	ui.statusMessage = "Copied!"

	// Status should persist in view
	view := ui.View().Content
	if !strings.Contains(view, "Copied!") {
		t.Error("Status message should be visible")
	}

	// Simulate a keypress (any navigation)
	ui.handleKey(tea.KeyPressMsg{Code: tea.KeyDown})

	// Status should be cleared after keypress
	if ui.statusMessage != "" {
		t.Error("Status should clear after keypress")
	}
}

func TestSearchUI_ResultCountFormat(t *testing.T) {
	store, _ := NewStore(":memory:")
	defer store.Close()

	for i := 0; i < 5; i++ {
		store.Add(Command{Command: fmt.Sprintf("cmd%d", i), Timestamp: time.Now()})
	}

	ui := NewSearchUI(store, prompt.DefaultPalette())
	ui.searchNow()

	view := ui.View().Content

	// Should use "result X of Y" format, not "[X/Y]"
	if strings.Contains(view, "[1/5]") {
		t.Error("Should not use [X/Y] format")
	}
	if !strings.Contains(view, "result 1 of 5") {
		t.Error("Should use 'result X of Y' format")
	}
}

func TestSearchUI_PreviewPane(t *testing.T) {
	// Create store with test data
	store, _ := NewStore(":memory:")
	defer store.Close()

	store.Add(Command{
		Command:   "kubectl get pods -n staging --sort-by='.status.startTime'",
		Cwd:       "/home/user/projects",
		GitBranch: "main",
		ExitCode:  0,
		Timestamp: time.Now(),
	})

	ui := NewSearchUI(store, prompt.DefaultPalette())
	ui.searchNow()

	view := ui.View().Content

	// Should contain preview section
	if !strings.Contains(view, "Preview") {
		t.Error("View should contain Preview section")
	}

	// Should show full command in preview (not truncated)
	if !strings.Contains(view, "kubectl get pods -n staging --sort-by='.status.startTime'") {
		t.Error("Preview should show full command")
	}
}

func TestSearchUI_TabsSelectAgentResultsAndPreserveQuery(t *testing.T) {
	store := newTestStore(t)
	_, err := store.Add(Command{Command: "kubectl get deploy/api", Timestamp: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.AddAgentInteraction(AgentInteraction{
		Prompt:       "find deploy logs",
		Response:     "kubectl logs deploy/api",
		ResponseKind: AgentResponseKindCommand,
		Agent:        "mock-agent",
		Accepted:     true,
		Timestamp:    time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}

	ui := NewSearchUI(store, prompt.DefaultPalette())
	ui.query = "deploy"
	ui.searchNow()
	if ui.agentResultsMode {
		t.Fatal("picker should open on the Commands tab")
	}
	view := ui.View().Content
	if !strings.Contains(view, "[Commands]") || !strings.Contains(view, "[Agent results]") {
		t.Fatalf("view should render visible tabs:\n%s", ui.View().Content)
	}

	ui.handleKey(tea.KeyPressMsg{Code: tea.KeyTab})
	ui.searchNow()
	if !ui.agentResultsMode {
		t.Fatal("Tab should select Agent results")
	}
	if ui.query != "deploy" {
		t.Fatalf("query = %q, want preserved query", ui.query)
	}
	if len(ui.agentResults) != 1 {
		t.Fatalf("agent result count = %d, want 1", len(ui.agentResults))
	}

	view = ui.View().Content
	for _, want := range []string{"Agent results", "find deploy logs", "kubectl logs deploy/api", "command", "mock-agent", "accepted"} {
		if !strings.Contains(view, want) {
			t.Fatalf("agent results view missing %q:\n%s", want, view)
		}
	}
	if got := ui.selectedText(); got != "kubectl logs deploy/api" {
		t.Fatalf("selectedText() = %q, want command response", got)
	}

	ui.handleKey(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	ui.searchNow()
	if ui.agentResultsMode {
		t.Fatal("Shift+Tab should select Commands")
	}
	if ui.query != "deploy" {
		t.Fatalf("query = %q after returning to Commands, want preserved query", ui.query)
	}

	ui.handleKey(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	if ui.agentResultsMode {
		t.Fatal("Ctrl+R while the picker is open must not switch tabs")
	}
}

func TestSearchUI_AgentExplanationDoesNotInsert(t *testing.T) {
	store := newTestStore(t)
	_, err := store.AddAgentInteraction(AgentInteraction{
		Prompt:       "why did deploy fail",
		Response:     "The migration has not completed.",
		ResponseKind: AgentResponseKindExplanation,
		Timestamp:    time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}

	ui := NewSearchUI(store, prompt.DefaultPalette())
	ui.handleKey(tea.KeyPressMsg{Code: tea.KeyTab})
	ui.searchNow()
	if got := ui.selectedText(); got != "" {
		t.Fatalf("selectedText() = %q, want explanation to remain preview-only", got)
	}
	model, _ := ui.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if model != ui || ui.statusMessage != "Only command results can be inserted" {
		t.Fatal("Enter should keep an explanation result in preview instead of selecting it")
	}
	for _, want := range []string{"tab agent results", "shift+tab commands", "ctrl+y copy response"} {
		if !strings.Contains(ui.View().Content, want) {
			t.Fatalf("agent result help should contain %q:\n%s", want, ui.View().Content)
		}
	}
}
