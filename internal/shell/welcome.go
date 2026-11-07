package shell

import (
	"os"
	"path/filepath"
)

// Welcome records whether the first-run panel (runOnboarding) was shown.
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

// ShouldShow returns true if the first-run panel should be displayed.
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
