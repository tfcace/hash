// Package tilde expands a leading ~ in a path the way the shell does, for
// the paths hash reads itself (startup files from the config), which no
// shell expands for it, and writes the home directory as ~ in the paths it
// shows.
package tilde

import (
	"os"
	"os/user"
	"path/filepath"
	"strings"
)

// Expand expands a leading ~ (the user's home) or ~name (name's home) in
// path. Anything else, and a name with no such user, is left as it is, as
// in bash.
func Expand(path string) string {
	rest, ok := strings.CutPrefix(path, "~")
	if !ok {
		return path
	}
	name, tail, _ := strings.Cut(rest, "/")
	var home string
	if name == "" {
		home, _ = os.UserHomeDir()
	} else if u, err := user.Lookup(name); err == nil {
		home = u.HomeDir
	}
	if home == "" {
		return path
	}
	return filepath.Join(home, tail)
}

// Abbrev returns path with the home directory written as ~: only a path
// that is the home directory or inside it, by whole path components.
func Abbrev(path string) string {
	home := strings.TrimSuffix(os.Getenv("HOME"), string(filepath.Separator))
	switch {
	case home == "":
		return path
	case path == home:
		return "~"
	case strings.HasPrefix(path, home+string(filepath.Separator)):
		return "~" + path[len(home):]
	}
	return path
}
