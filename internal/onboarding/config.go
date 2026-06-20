package onboarding

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// WriteAgentConfig creates a minimal config.toml with a stdio [agent] block for
// the chosen adapter. It never overwrites an existing file: if path already
// exists it returns an error and leaves the file untouched, so a user who has
// hand-written config is never clobbered by onboarding.
func WriteAgentConfig(path string, a Agent) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("config already exists at %s", path)
	} else if !os.IsNotExist(err) {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { //nolint:gosec // G301: standard config dir
		return err
	}

	var b strings.Builder
	b.WriteString("[agent]\n")
	b.WriteString("transport = \"stdio\"\n")
	fmt.Fprintf(&b, "command = %q\n", a.Command)
	if len(a.Args) > 0 {
		quoted := make([]string, len(a.Args))
		for i, arg := range a.Args {
			quoted[i] = fmt.Sprintf("%q", arg)
		}
		fmt.Fprintf(&b, "args = [%s]\n", strings.Join(quoted, ", "))
	}

	return os.WriteFile(path, []byte(b.String()), 0o644) //nolint:gosec // G306: non-sensitive config
}

// AgentConfigured reports whether path exists and declares an agent command.
func AgentConfigured(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return strings.Contains(string(data), "command")
}
