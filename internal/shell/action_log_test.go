package shell

import (
	"bytes"
	"strings"
	"testing"
)

func TestActionLog_AddAndSummary(t *testing.T) {
	var buf bytes.Buffer
	al := NewActionLog(&buf)

	al.Add("Read", "internal/auth/auth.go", true)
	al.Add("Bash", "go test ./internal/auth/...", true)
	al.Add("Write", "internal/auth/auth.go", true)
	al.Add("Bash", "rm -rf /tmp", false)

	summary := al.Summary()
	if len(summary) != 4 {
		t.Fatalf("Summary() returned %d items, want 4", len(summary))
	}
	if !summary[0].Allowed {
		t.Error("first action should be allowed")
	}
	if summary[3].Allowed {
		t.Error("fourth action should be denied")
	}
}

func TestActionLog_HasEdits(t *testing.T) {
	var buf bytes.Buffer
	al := NewActionLog(&buf)

	if al.HasEdits() {
		t.Error("empty log should have no edits")
	}

	al.Add("Read", "file.go", true)
	if al.HasEdits() {
		t.Error("read-only log should have no edits")
	}

	al.Add("Write", "file.go", true)
	if !al.HasEdits() {
		t.Error("log with write should have edits")
	}
}

func TestActionLog_EditedFiles(t *testing.T) {
	var buf bytes.Buffer
	al := NewActionLog(&buf)

	al.Add("Write", "a.go", true)
	al.Add("Write", "b.go", true)
	al.Add("Read", "c.go", true)
	al.Add("Write", "a.go", true) // duplicate

	files := al.EditedFiles()
	if len(files) != 2 {
		t.Fatalf("EditedFiles() = %v, want 2 files", files)
	}
}

func TestActionLog_RenderAction(t *testing.T) {
	var buf bytes.Buffer
	al := NewActionLog(&buf)

	al.Add("Read", "file.go", true)
	output := buf.String()
	if !strings.Contains(output, "file.go") {
		t.Errorf("output should contain filename, got: %q", output)
	}
	if !strings.Contains(output, "\u2713") {
		t.Errorf("allowed action should show \u2713, got: %q", output)
	}
}

func TestActionLog_RenderDenied(t *testing.T) {
	var buf bytes.Buffer
	al := NewActionLog(&buf)

	al.Add("Bash", "rm -rf /", false)
	output := buf.String()
	if !strings.Contains(output, "\u2717") {
		t.Errorf("denied action should show \u2717, got: %q", output)
	}
}
