package shell

import "testing"

// The documented HASH_CONFIG_DIR override must apply to everything the shell
// keeps in its config directory, not only to config.toml loading in main.
func TestGetConfigDir_HonorsHashConfigDir(t *testing.T) {
	t.Setenv("HASH_CONFIG_DIR", "/explicit/hash")
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	if got := getConfigDir(); got != "/explicit/hash" {
		t.Errorf("getConfigDir() = %q, want %q", got, "/explicit/hash")
	}
}
