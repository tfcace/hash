package shell

import (
	"strings"
	"testing"
)

func TestWelcome_FirstRun(t *testing.T) {
	tmpDir := t.TempDir()
	w := NewWelcome(tmpDir)

	// First run should show welcome
	if !w.ShouldShow() {
		t.Error("Should show welcome on first run")
	}

	// Get the message
	msg := w.Message()
	if !strings.Contains(msg, "Welcome to Hash") {
		t.Error("Should contain welcome header")
	}
	if !strings.Contains(msg, "??") {
		t.Error("Should mention ?? feature")
	}
	if !strings.Contains(msg, "Ctrl+R") {
		t.Error("Should mention Ctrl+R")
	}
	if !strings.Contains(msg, "Tab switches tabs") {
		t.Error("Should explain how to switch Ctrl+R result tabs")
	}

	// Mark as shown
	w.MarkShown()

	// Second check should not show
	w2 := NewWelcome(tmpDir)
	if w2.ShouldShow() {
		t.Error("Should not show welcome after first run")
	}
}
