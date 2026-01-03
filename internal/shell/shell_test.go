package shell

import (
	"os"
	"testing"

	"github.com/tfcace/hash/internal/config"
)

func TestNewShell(t *testing.T) {
	cfg := config.Default()

	sh, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if sh == nil {
		t.Error("New() returned nil")
	}
}

func TestShell_ModeMarkers(t *testing.T) {
	// Clear markers first
	os.Unsetenv("HASH_LOGIN")
	os.Unsetenv("HASH_INTERACTIVE")

	cfg := config.Default()
	mode := Mode{Login: true, Interactive: true}

	sh, err := NewWithMode(cfg, mode)
	if err != nil {
		t.Fatalf("failed to create shell: %v", err)
	}
	defer sh.Close()

	// Check that mode is stored
	if !sh.mode.Login {
		t.Error("expected Login mode to be true")
	}
	if !sh.mode.Interactive {
		t.Error("expected Interactive mode to be true")
	}
}
