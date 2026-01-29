// internal/compat/firstrun.go
package compat

import (
	"os"
	"path/filepath"
)

// isFirstRun checks if this is the first time Hash is being run.
// Returns true if no .hashrc exists and no migration state exists.
func isFirstRun(home, dataDir string) (bool, error) {
	// Check for .hashrc
	hashrcPath := filepath.Join(home, ".hashrc")
	if _, err := os.Stat(hashrcPath); err == nil {
		return false, nil
	}

	// Check for migration state
	statePath := filepath.Join(dataDir, "migration.json")
	if _, err := os.Stat(statePath); err == nil {
		return false, nil
	}

	return true, nil
}

// CheckFirstRun checks if this is the first run using default paths.
func CheckFirstRun() (bool, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return false, err
	}

	dataDir := DefaultStatePath()
	dataDir = filepath.Dir(dataDir) // Get directory from file path

	return isFirstRun(home, dataDir)
}

// ShouldShowMigrationPrompt checks if we should show the migration prompt.
// Returns: shouldShow, previousShell, rcFile
func ShouldShowMigrationPrompt() (bool, string, string) {
	isFirst, err := CheckFirstRun()
	if err != nil || !isFirst {
		return false, "", ""
	}

	shell, rcFile, ok := DetectPreviousShell()
	if !ok {
		return false, "", ""
	}

	return true, shell, rcFile
}
