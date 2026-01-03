package readline

import (
	"testing"
)

func TestNewReadline(t *testing.T) {
	cfg := Config{
		Prompt:      "$ ",
		Keybindings: "emacs",
	}

	rl, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer rl.Close()

	if rl == nil {
		t.Error("New() returned nil")
	}
}

func TestConfig_HistoryFile(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Prompt:      "$ ",
		HistoryFile: tmpDir + "/history",
	}

	rl, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer rl.Close()
}
