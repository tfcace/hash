package trace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInit_Disabled(t *testing.T) {
	os.Unsetenv("HASH_TRACE")

	err := Init()
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	if global != nil {
		t.Error("expected global tracer to be nil when HASH_TRACE not set")
	}
}

func TestInit_Enabled(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test-trace.jsonl")

	os.Setenv("HASH_TRACE", "editor")
	os.Setenv("HASH_TRACE_PATH", path)
	defer os.Unsetenv("HASH_TRACE")
	defer os.Unsetenv("HASH_TRACE_PATH")

	err := Init()
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	defer Close()

	if global == nil {
		t.Fatal("expected global tracer to be initialized")
	}

	if !Enabled("editor") {
		t.Error("expected editor subsystem to be enabled")
	}
	if Enabled("agent") {
		t.Error("expected agent subsystem to be disabled")
	}
}

func TestEmit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "emit-test.jsonl")

	os.Setenv("HASH_TRACE", "editor")
	os.Setenv("HASH_TRACE_PATH", path)
	defer os.Unsetenv("HASH_TRACE")
	defer os.Unsetenv("HASH_TRACE_PATH")

	err := Init()
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	Editor("key_read", map[string]any{
		"raw":    []byte{0x63},
		"parsed": "c",
	})

	Close()

	// Read file and verify
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile error = %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "trace_start") {
		t.Error("missing trace_start entry")
	}
	if !strings.Contains(content, "key_read") {
		t.Error("missing key_read entry")
	}
	if !strings.Contains(content, "trace_end") {
		t.Error("missing trace_end entry")
	}
}
