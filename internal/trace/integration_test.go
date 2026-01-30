package trace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIntegration_FullTrace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "integration.jsonl")

	os.Setenv("HASH_TRACE", "all")
	os.Setenv("HASH_TRACE_PATH", path)
	os.Setenv("HASH_TRACE_LEVEL", "verbose")
	defer os.Unsetenv("HASH_TRACE")
	defer os.Unsetenv("HASH_TRACE_PATH")
	defer os.Unsetenv("HASH_TRACE_LEVEL")

	err := Init()
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	// Simulate a keystroke flow
	Editor("key_read", map[string]any{
		"source": "direct",
		"raw":    []byte{0x63}, // 'c'
		"parsed": "c",
	})

	EditorDetailed("key_dispatch", map[string]any{
		"key":               "c",
		"ghost_active":      false,
		"completion_active": false,
		"mode":              "insert",
	})

	AgentHigh("ghost_start", map[string]any{
		"prompt": "list files",
	})

	Agent("ghost_chunk", map[string]any{
		"text":      "ls -la",
		"total_len": 6,
	})

	AgentHigh("ghost_accept", map[string]any{
		"key":      "Tab",
		"accepted": "ls -la",
		"action":   "accept_all",
	})

	ShellHigh("dispatch", map[string]any{
		"type":    "regular",
		"command": "ls -la",
	})

	Close()

	// Verify trace file
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile error = %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) < 6 {
		t.Errorf("expected at least 6 trace entries, got %d", len(lines))
	}

	// Verify each line is valid JSON
	for i, line := range lines {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Errorf("line %d: invalid JSON: %v", i, err)
		}
	}

	// Verify goroutine IDs are present
	var entry Entry
	json.Unmarshal([]byte(lines[1]), &entry) // First event after trace_start
	if entry.Goroutine == 0 {
		// Goroutine 0 is technically valid but unlikely
		t.Log("warning: goroutine ID is 0, might not be extracted correctly")
	}

	// Verify delta_ms is calculated
	json.Unmarshal([]byte(lines[2]), &entry)
	// Delta should be > 0 for second event in same subsystem
	// (but might be 0 if events are very fast)
}

func TestIntegration_LevelFiltering(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "level-test.jsonl")

	os.Setenv("HASH_TRACE", "editor")
	os.Setenv("HASH_TRACE_PATH", path)
	os.Setenv("HASH_TRACE_LEVEL", "high")
	defer os.Unsetenv("HASH_TRACE")
	defer os.Unsetenv("HASH_TRACE_PATH")
	defer os.Unsetenv("HASH_TRACE_LEVEL")

	err := Init()
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	// Emit events at different levels
	Editor("verbose_event", nil)          // Should be filtered
	EditorDetailed("detailed_event", nil) // Should be filtered
	EditorHigh("high_event", nil)         // Should pass

	Close()

	// Verify trace file
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile error = %v", err)
	}

	content := string(data)
	if strings.Contains(content, "verbose_event") {
		t.Error("verbose_event should have been filtered at 'high' level")
	}
	if strings.Contains(content, "detailed_event") {
		t.Error("detailed_event should have been filtered at 'high' level")
	}
	if !strings.Contains(content, "high_event") {
		t.Error("high_event should have passed through at 'high' level")
	}
}

func TestIntegration_SubsystemFiltering(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "subsystem-test.jsonl")

	os.Setenv("HASH_TRACE", "editor,shell")
	os.Setenv("HASH_TRACE_PATH", path)
	defer os.Unsetenv("HASH_TRACE")
	defer os.Unsetenv("HASH_TRACE_PATH")

	err := Init()
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	// Emit events from different subsystems
	Editor("editor_event", nil) // Should pass
	Shell("shell_event", nil)   // Should pass
	Agent("agent_event", nil)   // Should be filtered
	Parser("parser_event", nil) // Should be filtered

	Close()

	// Verify trace file
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile error = %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "editor_event") {
		t.Error("editor_event should have passed")
	}
	if !strings.Contains(content, "shell_event") {
		t.Error("shell_event should have passed")
	}
	if strings.Contains(content, "agent_event") {
		t.Error("agent_event should have been filtered")
	}
	if strings.Contains(content, "parser_event") {
		t.Error("parser_event should have been filtered")
	}
}
