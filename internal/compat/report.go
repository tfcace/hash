// internal/compat/report.go
package compat

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// ItemType represents the type of imported item.
type ItemType string

const (
	ItemAlias    ItemType = "alias"
	ItemExport   ItemType = "export"
	ItemFunction ItemType = "function"
)

// SkippedItem represents a line that was skipped during import.
type SkippedItem struct {
	Line    int    `json:"line"`
	Content string `json:"content"`
	Reason  string `json:"reason"`
}

// ImportedItem represents a successfully imported item.
type ImportedItem struct {
	Type  ItemType `json:"type"`
	Name  string   `json:"name"`
	Value string   `json:"value,omitempty"`
}

// Summary contains counts of imported/skipped items.
type Summary struct {
	Aliases   int `json:"aliases"`
	Exports   int `json:"exports"`
	Functions int `json:"functions"`
	Skipped   int `json:"skipped"`
}

// Report tracks the results of a migration import.
type Report struct {
	SourceFile    string         `json:"source_file"`
	SourceShell   string         `json:"source_shell"`
	SourceMtime   time.Time      `json:"source_mtime"`
	ImportTime    time.Time      `json:"import_time"`
	Summary       Summary        `json:"summary"`
	ImportedItems []ImportedItem `json:"imported_items,omitempty"`
	SkippedItems  []SkippedItem  `json:"skipped_items,omitempty"`
}

// NewReport creates a new migration report.
func NewReport(sourceFile, sourceShell string) *Report {
	return &Report{
		SourceFile:  sourceFile,
		SourceShell: sourceShell,
		ImportTime:  time.Now(),
	}
}

// AddImported records a successfully imported item.
func (r *Report) AddImported(itemType ItemType, name, value string) {
	r.ImportedItems = append(r.ImportedItems, ImportedItem{
		Type:  itemType,
		Name:  name,
		Value: value,
	})
	switch itemType {
	case ItemAlias:
		r.Summary.Aliases++
	case ItemExport:
		r.Summary.Exports++
	case ItemFunction:
		r.Summary.Functions++
	}
}

// AddSkipped records a skipped line.
func (r *Report) AddSkipped(line int, content, reason string) {
	r.SkippedItems = append(r.SkippedItems, SkippedItem{
		Line:    line,
		Content: content,
		Reason:  reason,
	})
	r.Summary.Skipped++
}

// State represents the persisted migration state.
type State struct {
	SourceFile  string    `json:"source_file"`
	SourceFiles []string  `json:"source_files,omitempty"` // Individual file paths for sourcing
	SourceShell string    `json:"source_shell"`
	SourceMtime time.Time `json:"source_mtime"`
	LastImport  time.Time `json:"last_import"`
	Declined    bool      `json:"declined"`
	Summary     Summary   `json:"summary"`
}

// Save writes the state to a JSON file.
func (s *State) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil { //nolint:gosec // G301: standard user data dir perms
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644) //nolint:gosec // G306: non-sensitive migration state
}

// LoadState reads the state from a JSON file.
func LoadState(path string) (*State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

// DefaultStatePath returns the default path for migration state.
func DefaultStatePath() string {
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "hash", "migration.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "hash", "migration.json")
}
