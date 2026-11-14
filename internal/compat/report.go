// internal/compat/report.go
package compat

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/tfcace/hash/internal/executor"
	"github.com/tfcace/hash/internal/tilde"
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
	File    string `json:"file,omitempty"` // set once the item's file is known
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
	Files         []string       `json:"files"` // the files it reports on, in order
	SourceShell   string         `json:"source_shell"`
	ImportTime    time.Time      `json:"import_time"`
	Summary       Summary        `json:"summary"`
	ImportedItems []ImportedItem `json:"imported_items,omitempty"`
	SkippedItems  []SkippedItem  `json:"skipped_items,omitempty"`
	// NotLoaded lists the files that did not load, or not all of them, each
	// once (NotLoad): the reported files, by the filter's word, and the
	// files they sourced as they ran, which the shell that ran them adds.
	NotLoaded []NotLoaded `json:"not_loaded,omitempty"`
}

// NotLoad records n, a file that did not load, unless the file is listed
// already: the filter's word on a migrated file and the run's, when another
// migrated file sources it, are one entry.
func (r *Report) NotLoad(n NotLoaded) {
	if !slices.ContainsFunc(r.NotLoaded, func(m NotLoaded) bool { return m.File == n.File }) {
		r.NotLoaded = append(r.NotLoaded, n)
	}
}

// SourcedNotLoaded is the files not loaded (NotLoaded) that are not the
// reported ones: those they sourced, known once they ran.
func (r *Report) SourcedNotLoaded() []NotLoaded {
	var sourced []NotLoaded
	for _, n := range r.NotLoaded {
		if !slices.Contains(r.Files, n.File) {
			sourced = append(sourced, n)
		}
	}
	return sourced
}

// NotLoaded is a file that did not load, or not all of it: a statement in
// it does not parse, at Line (0 when the parser gave none), for Reason.
// Load is executor.LoadedBefore when the statements before it ran, else
// executor.LoadedNone.
type NotLoaded struct {
	File   string              `json:"file"`
	Load   executor.SourceLoad `json:"load"`
	Line   int                 `json:"line"`
	Reason string              `json:"reason"`
}

// String says so in a line, naming the file as ~: "~/.bashrc: not loaded:
// line 3: ...", or "not loaded from line 3" when the lines before it ran.
func (n NotLoaded) String() string {
	where := ": not loaded"
	switch {
	case n.Load == executor.LoadedBefore:
		where = fmt.Sprintf(": not loaded from line %d", n.Line)
	case n.Line > 0:
		where = fmt.Sprintf(": not loaded: line %d", n.Line)
	}
	return tilde.Abbrev(n.File) + where + ": " + n.Reason
}

// NewReport creates a new migration report on the file at path, or on
// none for "".
func NewReport(path, sourceShell string) *Report {
	r := &Report{SourceShell: sourceShell, ImportTime: time.Now()}
	if path != "" {
		r.Files = []string{path}
	}
	return r
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

// Merge appends another file's results to this report.
func (r *Report) Merge(o *Report) {
	r.Files = append(r.Files, o.Files...)
	r.Summary.Aliases += o.Summary.Aliases
	r.Summary.Exports += o.Summary.Exports
	r.Summary.Functions += o.Summary.Functions
	r.Summary.Skipped += o.Summary.Skipped
	r.ImportedItems = append(r.ImportedItems, o.ImportedItems...)
	r.SkippedItems = append(r.SkippedItems, o.SkippedItems...)
	for _, n := range o.NotLoaded {
		r.NotLoad(n)
	}
}

// Count returns n with the noun for it: "1 alias", "2 aliases".
func Count(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// DefaultStatePath returns the default path for migration state.
func DefaultStatePath() string {
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "hash", "migration.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "hash", "migration.json")
}
