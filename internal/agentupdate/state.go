package agentupdate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// State is what hash remembers between sessions about update checks. It is
// shared by every hash session on the machine, which is what throttles the
// registry to one request a day however many shells are open.
type State struct {
	CheckedAt       time.Time `json:"checked_at"`
	Latest          string    `json:"latest,omitempty"`
	NotifiedAt      time.Time `json:"notified_at"`
	NotifiedVersion string    `json:"notified_version,omitempty"`
}

// LoadState reads the state file. A missing or unreadable file is an empty
// state: the worst outcome is one extra registry request.
func LoadState(path string) State {
	data, err := os.ReadFile(path)
	if err != nil {
		return State{}
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return State{}
	}
	return st
}

// SaveState writes the state atomically (temp file + rename) so a session
// reading while another writes never sees a half-written file.
func SaveState(path string, st State) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // G301: data dir
		return err
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".agent-update-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}
