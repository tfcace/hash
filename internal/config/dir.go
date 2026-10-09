package config

import (
	"os"
	"path/filepath"
)

// Dir returns the directory Hash keeps its configuration in: HASH_CONFIG_DIR
// when set, else $XDG_CONFIG_HOME/hash, else ~/.config/hash. Everything that
// lives beside config.toml (welcome flag, completion specs, onboarding
// output) resolves through here so one override moves all of it.
func Dir() string {
	if dir := os.Getenv("HASH_CONFIG_DIR"); dir != "" {
		return dir
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "hash")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "hash")
}
