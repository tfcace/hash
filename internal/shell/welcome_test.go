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

	// Mark as shown
	w.MarkShown()

	// Second check should not show
	w2 := NewWelcome(tmpDir)
	if w2.ShouldShow() {
		t.Error("Should not show welcome after first run")
	}
}

// Ctrl+Y and Ctrl+O copy in the Ctrl+R picker; the prompt binds neither.
// Tips offered them as prompt keys that did nothing. (The first-run panel
// has its own test, in internal/onboarding.)
func TestTips_ScopeTheCopyKeysToThePicker(t *testing.T) {
	sh := &Shell{}
	tips := captureStdout(t, sh, func() {
		if err := sh.builtinTips(nil); err != nil {
			t.Error(err)
		}
	})
	for _, line := range strings.Split(tips, "\n") {
		if (strings.Contains(line, "Ctrl+Y") || strings.Contains(line, "Ctrl+O")) && !strings.Contains(line, "Ctrl+R") {
			t.Errorf("tips offers a picker key at the prompt: %q", line)
		}
	}
}
