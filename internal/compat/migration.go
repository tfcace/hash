// internal/compat/migration.go
package compat

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/tfcace/hash/internal/tilde"
)

// MigrationStatus is where the user stands with migrating their previous
// shell's config. migration.json is the source of truth: Imported and
// Declined are persisted, and hash writes the state before anything else, so
// a ~/.hashrc that hash created always comes with one. Only without a state
// file is the status derived, from the one thing there is to go by: a
// ~/.hashrc the user wrote by hand means Manual, none means FirstRun.
// "Source changed" is not a status of its own: it is found per startup by
// comparing the recorded mtimes with the disk (see State.Resourced).
type MigrationStatus string

const (
	// StatusFirstRun: no state file and no ~/.hashrc. Startup offers to import.
	StatusFirstRun MigrationStatus = "first-run"
	// StatusManual: no state file, but ~/.hashrc exists, so hash was set up
	// without migrating. Startup leaves it alone.
	StatusManual MigrationStatus = "manual"
	// StatusDeclined: the user said no. Startup sources nothing.
	StatusDeclined MigrationStatus = "declined"
	// StatusImported: startup sources Files on every interactive launch.
	StatusImported MigrationStatus = "imported"
)

// ImportRecord is what the last import (or re-source) loaded. Only an
// Imported state has one.
type ImportRecord struct {
	Time    time.Time `json:"time"`
	Summary Summary   `json:"summary"`
	// Mtimes holds each file's mtime when startup last sourced it. A file
	// without one has an unknown mtime (a state upgraded from the old
	// single-mtime format); the next startup adopts it without a notice.
	Mtimes map[string]time.Time `json:"mtimes,omitempty"`
	// SourcedNotLoaded lists the files the migrated files sourced that did
	// not load, or not all of them, when startup last sourced them.
	SourcedNotLoaded []NotLoaded `json:"sourced_not_loaded,omitempty"`
}

// State is the persisted migration state (migration.json). Import is set
// exactly when Status is StatusImported.
type State struct {
	Status      MigrationStatus `json:"status"`
	SourceShell string          `json:"source_shell"`
	Paths       []string        `json:"paths,omitempty"` // the migrated (or declined) files, in source order
	Import      *ImportRecord   `json:"import,omitempty"`
}

// legacyState holds the fields of older formats, read only to upgrade an
// existing migration.json: the pre-status format (source_files, one
// source_mtime, declined), the first status format, which kept the import
// time and summary at the top level for every status, and the per-file
// format, which kept each file's mtime (zero or absent: unknown) beside it.
type legacyState struct {
	SourceFiles []string  `json:"source_files"`
	SourceMtime time.Time `json:"source_mtime"`
	Declined    bool      `json:"declined"`
	LastImport  time.Time `json:"last_import"`
	Summary     Summary   `json:"summary"`
	Files       []struct {
		Path  string     `json:"path"`
		Mtime *time.Time `json:"mtime"`
	} `json:"files"`
}

// Resourced records that startup re-sourced an imported migration. Files whose
// mtime differs from the recorded one are returned as changed; an unknown
// recorded mtime is adopted without reporting a change. The state takes the
// new mtimes and summary, and the files they sourced that did not load, and
// updated reports whether it needs saving.
func (s *State) Resourced(files []FilteredFile, report *Report) (changed []string, updated bool) {
	if s.Import == nil {
		return nil, false
	}
	for _, f := range files {
		recorded, known := s.Import.Mtimes[f.Path]
		if known && recorded.Equal(f.Mtime) {
			continue
		}
		if known {
			changed = append(changed, f.Path)
		}
		s.Import.setMtime(f.Path, f.Mtime)
		updated = true
	}
	if report != nil {
		if sourced := report.SourcedNotLoaded(); !slices.Equal(s.Import.SourcedNotLoaded, sourced) {
			s.Import.SourcedNotLoaded = sourced
			updated = true
		}
	}
	if updated && report != nil {
		s.Import.Summary = report.Summary
	}
	return changed, updated
}

// PathsFor returns the migrated files that a shell reads (ReadByPath), in
// order: a login shell, an interactive one, both or neither, as the
// previous shell would.
func (s *State) PathsFor(login, interactive bool) []string {
	var paths []string
	for _, path := range s.Paths {
		switch ReadByPath(path) {
		case ReadByLogin:
			if !login {
				continue
			}
		case ReadByInteractive:
			if !interactive {
				continue
			}
		}
		paths = append(paths, path)
	}
	return paths
}

func (r *ImportRecord) setMtime(path string, mtime time.Time) {
	if r.Mtimes == nil {
		r.Mtimes = make(map[string]time.Time)
	}
	r.Mtimes[path] = mtime
}

// valid reports whether the state is one that is persisted: Imported with
// its import record and the files it names, or Declined without a record.
func (s *State) valid() error {
	switch {
	case s.Status == StatusImported && s.Import == nil:
		return fmt.Errorf("imported migration has no import record")
	case s.Status == StatusImported && len(s.Paths) == 0:
		return fmt.Errorf("imported migration names no files")
	case s.Status == StatusDeclined && s.Import != nil:
		return fmt.Errorf("declined migration has an import record")
	case s.Status != StatusImported && s.Status != StatusDeclined:
		return fmt.Errorf("migration status %q is not persisted", s.Status)
	}
	return nil
}

// Save atomically writes a persisted (Imported or Declined) state.
func (s *State) Save(path string) error {
	if err := s.valid(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { //nolint:gosec // G301: standard user data dir perms
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".migration-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) //nolint:errcheck // gone after a successful rename
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil { //nolint:gosec // G302: non-sensitive migration state
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// LoadState reads a persisted state, upgrading older formats. A missing file
// is returned as an fs.ErrNotExist error.
func LoadState(path string) (*State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw struct {
		State
		legacyState
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	state := raw.State
	switch state.Status {
	case StatusImported, StatusDeclined:
	case "":
		state.upgradePreStatus(raw.legacyState)
	default:
		return nil, fmt.Errorf("%s: unknown migration status %q", path, state.Status)
	}
	state.upgradeImportRecord(raw.legacyState)
	if err := state.valid(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &state, nil
}

func (s *State) upgradePreStatus(old legacyState) {
	s.Status = StatusImported
	if old.Declined {
		s.Status = StatusDeclined
	}
	s.Paths = old.SourceFiles
}

// upgradeImportRecord moves a top-level import time and summary into the
// import record of an Imported state, drops what a Declined one carried, and
// moves known per-file mtimes into the record. The pre-status format kept a
// single mtime, taken from the first file only, so it is trustworthy only for
// a single-file migration.
func (s *State) upgradeImportRecord(old legacyState) {
	switch {
	case s.Status == StatusImported && s.Import == nil:
		s.Import = &ImportRecord{Time: old.LastImport, Summary: old.Summary}
	case s.Status == StatusDeclined:
		s.Import = nil
	}
	if len(s.Paths) == 0 {
		for _, f := range old.Files {
			s.Paths = append(s.Paths, f.Path)
		}
	}
	if s.Import == nil {
		return
	}
	for _, f := range old.Files {
		if f.Mtime != nil && !f.Mtime.IsZero() {
			s.Import.setMtime(f.Path, *f.Mtime)
		}
	}
	if len(old.SourceFiles) == 1 && !old.SourceMtime.IsZero() {
		s.Import.setMtime(old.SourceFiles[0], old.SourceMtime)
	}
}

// ReadMigration returns the migration state for this startup: the persisted
// state, or a FirstRun/Manual state when there is none yet.
func ReadMigration(statePath string) (*State, error) {
	state, err := LoadState(statePath)
	if err == nil {
		return state, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if _, err := os.Stat(HashrcPath()); err == nil {
		return &State{Status: StatusManual}, nil
	}
	return &State{Status: StatusFirstRun}, nil
}

// RecordImport saves the Imported state for files that were just filtered,
// then creates ~/.hashrc if it does not exist yet (see ensureHashrc for what
// an existing one keeps). Both the first-run prompt and `hash migrate` go
// through here. err means nothing was recorded, and ~/.hashrc is left alone
// so the next startup still offers the import. hashrcErr means the import
// stands but ~/.hashrc could not be created or updated.
func RecordImport(statePath, shell string, files []FilteredFile, report *Report) (hashrcCreated bool, hashrcErr, err error) {
	state := &State{
		Status:      StatusImported,
		SourceShell: shell,
		Import:      &ImportRecord{Time: report.ImportTime, Summary: report.Summary, SourcedNotLoaded: report.SourcedNotLoaded()},
	}
	for _, f := range files {
		state.Paths = append(state.Paths, f.Path)
		state.Import.setMtime(f.Path, f.Mtime)
	}
	if err := state.Save(statePath); err != nil {
		return false, nil, err
	}
	created, hashrcErr := ensureHashrc(state.Paths)
	return created, hashrcErr, nil
}

// RecordDecline saves the Declined state so startup stops offering to import.
func RecordDecline(statePath, shell string, paths []string) error {
	state := &State{Status: StatusDeclined, SourceShell: shell, Paths: paths}
	return state.Save(statePath)
}

// HashrcPath returns the path of the user's ~/.hashrc.
func HashrcPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".hashrc")
}

// ensureHashrc creates ~/.hashrc describing the migrated files. An existing
// ~/.hashrc is the user's: only the header hash generated at its top is
// rewritten to name the files now migrated, and only while that header is
// intact; every other byte is left as it is.
func ensureHashrc(paths []string) (created bool, err error) {
	path := HashrcPath()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644) //nolint:gosec // G302: user shell config file
	if errors.Is(err, fs.ErrExist) {
		return false, refreshHashrcHeader(path, paths)
	}
	if err != nil {
		return false, err
	}
	if _, err := f.WriteString(FormatHashrcCommentFiles(paths)); err != nil {
		_ = f.Close()
		return true, err
	}
	return true, f.Close()
}

// refreshHashrcHeader replaces the generated header at the top of an existing
// ~/.hashrc, if it is still there, with one naming paths.
func refreshHashrcHeader(path string, paths []string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	content := string(data)
	if !strings.HasPrefix(content, hashrcHeaderStart) {
		return nil
	}
	end := strings.Index(content, hashrcHeaderEnd)
	if end < 0 {
		return nil
	}
	updated := FormatHashrcCommentFiles(paths) + content[end+len(hashrcHeaderEnd):]
	if updated == content {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".hashrc-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) //nolint:errcheck // gone after a successful rename
	if _, err := tmp.WriteString(updated); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), info.Mode().Perm()); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// FilteredFile is one migrated file prepared for the target dialect.
type FilteredFile struct {
	Path    string
	Mtime   time.Time
	Content string
}

// FilterFiles filters each existing file for the target dialect and merges the
// reports into one, whose Files names all of them. Files that no longer
// exist are skipped silently; other failures are returned in errs, each
// naming its file. The report is nil when no file could be filtered.
func FilterFiles(paths []string, shell, dialect string) (files []FilteredFile, report *Report, errs []error) {
	for _, path := range paths {
		// The mtime is taken before reading, so an edit made meanwhile shows
		// up as a change on the next startup.
		info, err := os.Stat(path)
		var content string
		var r *Report
		if err == nil {
			content, r, err = FilterWithDialect(path, shell, dialect)
		}
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			errs = append(errs, FileError(path, err))
			continue
		}
		files = append(files, FilteredFile{Path: path, Mtime: info.ModTime(), Content: content})
		if report == nil {
			report = r
		} else {
			report.Merge(r)
		}
	}
	return files, report, errs
}

// FileError returns err, from reading, filtering or running the file at
// path, with the file named once, home as ~: a PathError of the file itself
// names it with its full path and the call that failed.
func FileError(path string, err error) error {
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) && pathErr.Path == path {
		err = pathErr.Err
	}
	return fmt.Errorf("%s: %w", tilde.Abbrev(path), err)
}

// Decision is the answer to a migration prompt.
type Decision int

const (
	// DecisionLater: no answer (EOF, Ctrl+D or Ctrl+C, hash ending). Nothing
	// is saved, so the first-run prompt is offered again on the next launch.
	DecisionLater Decision = iota
	DecisionImport
	DecisionDecline
)

// AskMigration asks label until it gets Y/n (Enter means yes), printing help
// for "?". readLine reads each answer; no answer (it reports false: the
// input ended, the user canceled, hash is ending) is DecisionLater.
func AskMigration(readLine func() (string, bool), out io.Writer, label, help string) Decision {
	for {
		fmt.Fprintf(out, "\n%s [Y/n/?]: ", label)
		input, ok := readLine()
		if !ok {
			fmt.Fprintln(out)
			return DecisionLater
		}
		switch strings.TrimSpace(strings.ToLower(input)) {
		case "", "y", "yes":
			return DecisionImport
		case "n", "no":
			return DecisionDecline
		case "?":
			fmt.Fprint(out, help)
		default:
			fmt.Fprintln(out, "Please enter Y, n, or ?")
		}
	}
}

// LineReader reads answers from in, each up to its newline and no further,
// so what the user types ahead (the first command) is left for whatever
// reads in next. A last line the input ends without a newline is an answer
// too; an empty one is none.
func LineReader(in io.Reader) func() (string, bool) {
	return func() (string, bool) {
		line, err := readLine(in)
		return line, err == nil || strings.TrimSpace(line) != ""
	}
}

// readLine reads one line from in a byte at a time, so nothing after the
// newline is consumed. The newline is included; err is set at EOF or failure.
func readLine(in io.Reader) (string, error) {
	var line []byte
	b := make([]byte, 1)
	for {
		n, err := in.Read(b)
		if n == 1 {
			line = append(line, b[0])
			if b[0] == '\n' {
				return string(line), nil
			}
		}
		if err != nil {
			return string(line), err
		}
	}
}

// FormatChangeNotice returns a one-line notice about migrated files that
// changed since they were last sourced.
func FormatChangeNotice(changed []string, skipped int) string {
	names := make([]string, len(changed))
	for i, p := range changed {
		names[i] = tilde.Abbrev(p)
	}
	return fmt.Sprintf("hash: %s changed: %s skipped (hash migrate status)\n",
		strings.Join(names, ", "), Count(skipped, "item", "items"))
}
