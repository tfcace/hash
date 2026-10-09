package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDir(t *testing.T) {
	t.Run("HASH_CONFIG_DIR wins", func(t *testing.T) {
		t.Setenv("HASH_CONFIG_DIR", "/explicit/hash")
		t.Setenv("XDG_CONFIG_HOME", "/xdg")
		if got := Dir(); got != "/explicit/hash" {
			t.Errorf("Dir() = %q, want %q", got, "/explicit/hash")
		}
	})
	t.Run("XDG_CONFIG_HOME/hash", func(t *testing.T) {
		t.Setenv("HASH_CONFIG_DIR", "")
		t.Setenv("XDG_CONFIG_HOME", "/xdg")
		if got, want := Dir(), filepath.Join("/xdg", "hash"); got != want {
			t.Errorf("Dir() = %q, want %q", got, want)
		}
	})
	t.Run("home default", func(t *testing.T) {
		t.Setenv("HASH_CONFIG_DIR", "")
		t.Setenv("XDG_CONFIG_HOME", "")
		home, _ := os.UserHomeDir()
		if got, want := Dir(), filepath.Join(home, ".config", "hash"); got != want {
			t.Errorf("Dir() = %q, want %q", got, want)
		}
	})
}
