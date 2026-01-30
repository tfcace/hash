package shell

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tfcace/hash/internal/version"
)

// Welcome handles first-run welcome message.
type Welcome struct {
	configDir string
	flagFile  string
}

// NewWelcome creates a new Welcome handler.
func NewWelcome(configDir string) *Welcome {
	return &Welcome{
		configDir: configDir,
		flagFile:  filepath.Join(configDir, ".welcome_shown"),
	}
}

// ShouldShow returns true if welcome message should be displayed.
func (w *Welcome) ShouldShow() bool {
	_, err := os.Stat(w.flagFile)
	return os.IsNotExist(err)
}

// MarkShown marks the welcome as shown (creates flag file).
func (w *Welcome) MarkShown() error {
	if err := os.MkdirAll(w.configDir, 0o755); err != nil { //nolint:gosec // G301: standard config dir
		return err
	}
	return os.WriteFile(w.flagFile, []byte(""), 0o644) //nolint:gosec // G306: non-sensitive flag file
}

// Message returns the welcome message.
func (w *Welcome) Message() string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("\033[1mWelcome to Hash %s\033[0m\n\n", version.Version))
	b.WriteString("Quick start:\n")
	b.WriteString("  \033[36m??\033[0m          Ask the AI for help (e.g., ?? find large files)\n")
	b.WriteString("  \033[36mcmd | ??\033[0m    Pipe output to AI (e.g., git diff | ?? summarize)\n")
	b.WriteString("  \033[36mCtrl+R\033[0m      Search history\n")
	b.WriteString("  \033[36mCtrl+P\033[0m      Pick context for AI\n")
	b.WriteString("  \033[36mCtrl+Y\033[0m      Copy last command\n")
	b.WriteString("  \033[36mCtrl+O\033[0m      Copy last output\n")
	b.WriteString("\n")
	b.WriteString("Run '\033[36mtips\033[0m' for more features.\n")

	return b.String()
}
