// internal/compat/migration_test.go
package compat

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func migrationHome(t *testing.T) (home, statePath string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	return home, filepath.Join(home, "data", "migration.json")
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func setMtime(t *testing.T, path string, mtime time.Time) {
	t.Helper()
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func TestReadMigration_FirstRunWithoutStateOrHashrc(t *testing.T) {
	_, statePath := migrationHome(t)
	state, err := ReadMigration(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != StatusFirstRun {
		t.Errorf("Status = %q, want first-run", state.Status)
	}
}

func TestReadMigration_ManualWhenHashrcExists(t *testing.T) {
	home, statePath := migrationHome(t)
	writeFile(t, filepath.Join(home, ".hashrc"), "# config")
	state, err := ReadMigration(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != StatusManual {
		t.Errorf("Status = %q, want manual", state.Status)
	}
}

func TestReadMigration_PersistedStateWins(t *testing.T) {
	_, statePath := migrationHome(t)
	if err := RecordDecline(statePath, "zsh", []string{"/x/.zshrc"}); err != nil {
		t.Fatal(err)
	}
	state, err := ReadMigration(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != StatusDeclined {
		t.Errorf("Status = %q, want declined", state.Status)
	}
}

func TestState_SaveRefusesDerivedStatus(t *testing.T) {
	_, statePath := migrationHome(t)
	for _, st := range []MigrationStatus{StatusFirstRun, StatusManual, ""} {
		if err := (&State{Status: st}).Save(statePath); err == nil {
			t.Errorf("Save with status %q should fail", st)
		}
	}
}

func TestLoadState_RejectsUnknownStatus(t *testing.T) {
	_, statePath := migrationHome(t)
	_ = os.MkdirAll(filepath.Dir(statePath), 0o755)
	writeFile(t, statePath, `{"status":"bogus"}`)
	if _, err := LoadState(statePath); err == nil {
		t.Error("expected an error for an unknown status")
	}
}

func TestLoadState_UpgradesLegacyFormat(t *testing.T) {
	_, statePath := migrationHome(t)
	_ = os.MkdirAll(filepath.Dir(statePath), 0o755)
	mtime := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	writeFile(t, statePath, `{"source_file":"/a/.zprofile, /a/.zshrc","source_files":["/a/.zprofile","/a/.zshrc"],"source_shell":"zsh","source_mtime":"2026-01-02T03:04:05Z","declined":false}`)
	state, err := LoadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != StatusImported || len(state.Paths) != 2 {
		t.Fatalf("state = %+v, want imported with two files", state)
	}
	if len(state.Import.Mtimes) != 0 {
		t.Errorf("multi-file legacy mtimes should be unknown, got %v", state.Import.Mtimes)
	}

	writeFile(t, statePath, `{"source_files":["/a/.zshrc"],"source_mtime":"2026-01-02T03:04:05Z"}`)
	state, err = LoadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if got := state.Import.Mtimes["/a/.zshrc"]; !got.Equal(mtime) {
		t.Errorf("single-file legacy mtime = %v, want %v", got, mtime)
	}

	writeFile(t, statePath, `{"source_file":"/a/.bashrc","declined":true}`)
	state, err = LoadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != StatusDeclined {
		t.Errorf("Status = %q, want declined", state.Status)
	}
}

// Every file of a multi-file migration is tracked: a change to the second
// file must be noticed (the old state stat'ed "a, b" as one path).
func TestState_ResourcedDetectsChangeInAnyFile(t *testing.T) {
	home, statePath := migrationHome(t)
	zprofile := filepath.Join(home, ".zprofile")
	zshrc := filepath.Join(home, ".zshrc")
	writeFile(t, zprofile, "export A=1\n")
	writeFile(t, zshrc, "alias ll='ls -la'\n")

	files, report, errs := FilterFiles([]string{zprofile, zshrc}, "zsh", "bash")
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if _, hashrcErr, err := RecordImport(statePath, "zsh", files, report); err != nil || hashrcErr != nil {
		t.Fatal(err)
	}

	writeFile(t, zshrc, "alias ll='ls -la'\nsetopt foo\n")
	setMtime(t, zshrc, time.Now().Add(time.Hour))

	state, _ := LoadState(statePath)
	files, report, _ = FilterFiles(state.Paths, state.SourceShell, "bash")
	changed, updated := state.Resourced(files, report)
	if len(changed) != 1 || changed[0] != zshrc || !updated {
		t.Fatalf("changed = %v updated = %v, want [%s] true", changed, updated, zshrc)
	}
	if state.Import.Summary.Skipped != 1 {
		t.Errorf("Summary.Skipped = %d, want the fresh count 1", state.Import.Summary.Skipped)
	}

	// Once recorded, the same change is not reported again.
	if err := state.Save(statePath); err != nil {
		t.Fatal(err)
	}
	state, _ = LoadState(statePath)
	files, report, _ = FilterFiles(state.Paths, state.SourceShell, "bash")
	if changed, updated := state.Resourced(files, report); len(changed) != 0 || updated {
		t.Errorf("second startup: changed = %v updated = %v, want none", changed, updated)
	}
}

func TestState_ResourcedAdoptsUnknownMtimeSilently(t *testing.T) {
	home, _ := migrationHome(t)
	zshrc := filepath.Join(home, ".zshrc")
	writeFile(t, zshrc, "alias ll='ls -la'\n")

	state := &State{Status: StatusImported, Paths: []string{zshrc}, Import: &ImportRecord{}}
	files, report, _ := FilterFiles(state.Paths, "zsh", "bash")
	changed, updated := state.Resourced(files, report)
	if len(changed) != 0 || !updated {
		t.Fatalf("changed = %v updated = %v, want none and true", changed, updated)
	}
	if _, ok := state.Import.Mtimes[zshrc]; !ok {
		t.Error("unknown mtime should be adopted")
	}
}

func TestFilterFiles_SkipsMissingFiles(t *testing.T) {
	home, _ := migrationHome(t)
	zshrc := filepath.Join(home, ".zshrc")
	writeFile(t, zshrc, "alias ll='ls -la'\n")

	files, report, errs := FilterFiles([]string{filepath.Join(home, ".zprofile"), zshrc}, "zsh", "bash")
	if len(errs) != 0 || len(files) != 1 || report == nil || !slices.Equal(report.Files, []string{zshrc}) {
		t.Fatalf("files = %v report = %v errs = %v", files, report, errs)
	}
}

func TestRecordImport_KeepsExistingHashrc(t *testing.T) {
	home, statePath := migrationHome(t)
	zshrc := filepath.Join(home, ".zshrc")
	hashrc := filepath.Join(home, ".hashrc")
	writeFile(t, zshrc, "alias ll='ls -la'\n")
	writeFile(t, hashrc, "alias mine=1\n")

	files, report, _ := FilterFiles([]string{zshrc}, "zsh", "bash")
	created, hashrcErr, err := RecordImport(statePath, "zsh", files, report)
	if err != nil || hashrcErr != nil || created {
		t.Fatalf("created = %v errs = %v %v, want false nil nil", created, hashrcErr, err)
	}
	if data, _ := os.ReadFile(hashrc); string(data) != "alias mine=1\n" {
		t.Errorf(".hashrc was rewritten:\n%s", data)
	}
}

func TestRecordImport_CreatesHashrc(t *testing.T) {
	home, statePath := migrationHome(t)
	zshrc := filepath.Join(home, ".zshrc")
	writeFile(t, zshrc, "alias ll='ls -la'\n")

	files, report, _ := FilterFiles([]string{zshrc}, "zsh", "bash")
	created, hashrcErr, err := RecordImport(statePath, "zsh", files, report)
	if err != nil || hashrcErr != nil || !created {
		t.Fatalf("created = %v errs = %v %v, want true nil nil", created, hashrcErr, err)
	}
	data, _ := os.ReadFile(filepath.Join(home, ".hashrc"))
	if !strings.Contains(string(data), "~/.zshrc") {
		t.Errorf(".hashrc should name the migrated file:\n%s", data)
	}
	state, err := LoadState(statePath)
	if err != nil || state.Status != StatusImported || state.Import.Summary.Aliases != 1 {
		t.Fatalf("state = %+v err = %v", state, err)
	}
}

func TestAskMigration(t *testing.T) {
	tests := []struct {
		input string
		want  Decision
	}{
		{"\n", DecisionImport},
		{"Y\n", DecisionImport},
		{"yes\n", DecisionImport},
		{"n\n", DecisionDecline},
		{"NO\n", DecisionDecline},
		{"?\nn\n", DecisionDecline},
		{"maybe\ny\n", DecisionImport},
		{"", DecisionLater},
		{"maybe\n", DecisionLater},
		{"n", DecisionDecline}, // answer without a trailing newline before EOF
	}
	for _, tt := range tests {
		var out bytes.Buffer
		if got := AskMigration(LineReader(strings.NewReader(tt.input)), &out, "Load?", "HELP"); got != tt.want {
			t.Errorf("AskMigration(%q) = %v, want %v", tt.input, got, tt.want)
		}
		if strings.HasPrefix(tt.input, "?") && !strings.Contains(out.String(), "HELP") {
			t.Errorf("AskMigration(%q) should print help", tt.input)
		}
	}
}

// Re-importing from another shell rewrites the generated header of ~/.hashrc
// so it names the files now loaded; the user's lines below it are kept.
func TestRecordImport_RefreshesGeneratedHashrcHeader(t *testing.T) {
	home, statePath := migrationHome(t)
	zshrc, bashrc := filepath.Join(home, ".zshrc"), filepath.Join(home, ".bashrc")
	hashrc := filepath.Join(home, ".hashrc")
	writeFile(t, zshrc, "alias ll='ls -la'\n")
	writeFile(t, bashrc, "alias la='ls -A'\n")

	files, report, _ := FilterFiles([]string{zshrc}, "zsh", "bash")
	if _, hashrcErr, err := RecordImport(statePath, "zsh", files, report); err != nil || hashrcErr != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(hashrc)
	writeFile(t, hashrc, string(data)+"alias mine=1\n")

	files, report, _ = FilterFiles([]string{bashrc}, "bash", "bash")
	if _, hashrcErr, err := RecordImport(statePath, "bash", files, report); err != nil || hashrcErr != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(hashrc)
	got := string(data)
	if strings.Contains(got, "~/.zshrc") || !strings.Contains(got, "~/.bashrc") {
		t.Errorf(".hashrc header should list ~/.bashrc only:\n%s", got)
	}
	if !strings.HasSuffix(got, "# Add your own customizations below:\n\nalias mine=1\n") {
		t.Errorf("the user's lines were not kept below the header:\n%s", got)
	}
}

// A decline records which files were declined, nothing about an import.
func TestRecordDecline_WritesNoImportPayload(t *testing.T) {
	_, statePath := migrationHome(t)
	if err := RecordDecline(statePath, "zsh", []string{"/a/.zshrc"}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(statePath)
	for _, field := range []string{"mtime", "import", "summary", "last_import"} {
		if strings.Contains(string(data), `"`+field+`"`) {
			t.Errorf("declined state has %q:\n%s", field, data)
		}
	}
	state, err := LoadState(statePath)
	if err != nil || state.Status != StatusDeclined || state.Import != nil || state.Paths[0] != "/a/.zshrc" {
		t.Fatalf("state = %+v err = %v", state, err)
	}
}

// Save only writes the two persisted shapes.
func TestState_SaveRefusesMismatchedPayload(t *testing.T) {
	_, statePath := migrationHome(t)
	for _, state := range []*State{
		{Status: StatusImported},
		{Status: StatusDeclined, Import: &ImportRecord{}},
	} {
		if err := state.Save(statePath); err == nil {
			t.Errorf("Save(%+v) = nil, want an error", state)
		}
	}
}

// The first status format kept last_import and summary at the top level and
// wrote zero mtimes for unknown and declined files.
func TestLoadState_UpgradesFirstStatusFormat(t *testing.T) {
	_, statePath := migrationHome(t)
	_ = os.MkdirAll(filepath.Dir(statePath), 0o755)
	writeFile(t, statePath, `{"status":"imported","source_shell":"zsh","files":[{"path":"/a/.zshrc","mtime":"0001-01-01T00:00:00Z"}],"last_import":"2026-01-02T03:04:05Z","summary":{"aliases":2}}`)
	state, err := LoadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if state.Import == nil || state.Import.Summary.Aliases != 2 || state.Import.Time.Year() != 2026 || len(state.Import.Mtimes) != 0 || state.Paths[0] != "/a/.zshrc" {
		t.Errorf("imported upgrade = %+v (import %+v)", state, state.Import)
	}

	writeFile(t, statePath, `{"status":"declined","source_shell":"zsh","files":[{"path":"/a/.zshrc","mtime":"0001-01-01T00:00:00Z"}],"summary":{}}`)
	state, err = LoadState(statePath)
	if err != nil || state.Import != nil || state.Paths[0] != "/a/.zshrc" {
		t.Errorf("declined upgrade = %+v err = %v", state, err)
	}
}

// hash migrate reads os.Stdin before the shell's line editor starts. A
// buffered reader read past the answer and the typed-ahead first command was
// lost with it; the answer must be read up to its newline only.
func TestAskMigration_LeavesTypedAheadInput(t *testing.T) {
	in := strings.NewReader("maybe\ny\nls -la\n")
	if got := AskMigration(LineReader(in), &bytes.Buffer{}, "Load?", "HELP"); got != DecisionImport {
		t.Fatalf("AskMigration() = %v, want import", got)
	}
	rest, _ := io.ReadAll(in)
	if string(rest) != "ls -la\n" {
		t.Errorf("input left after the answer = %q, want the typed-ahead command", rest)
	}
}

// When the state cannot be saved, ~/.hashrc must not be created: the next
// startup would take it for a manual setup and never source the files.
func TestRecordImport_SaveFailureCreatesNoHashrc(t *testing.T) {
	home, statePath := migrationHome(t)
	zshrc := filepath.Join(home, ".zshrc")
	writeFile(t, zshrc, "alias ll='ls -la'\n")
	writeFile(t, filepath.Dir(statePath), "a file where the state directory should be")

	files, report, _ := FilterFiles([]string{zshrc}, "zsh", "bash")
	created, _, err := RecordImport(statePath, "zsh", files, report)
	if err == nil || created {
		t.Fatalf("created = %v err = %v, want false and an error", created, err)
	}
	if _, err := os.Stat(filepath.Join(home, ".hashrc")); !os.IsNotExist(err) {
		t.Errorf(".hashrc exists after a failed save (stat err %v)", err)
	}
	if err := os.Remove(filepath.Dir(statePath)); err != nil {
		t.Fatal(err)
	}
	if state, err := ReadMigration(statePath); err != nil || state.Status != StatusFirstRun {
		t.Errorf("next startup: state = %+v err = %v, want first-run", state, err)
	}
}

// The per-file format kept each file's mtime beside its path.
func TestLoadState_UpgradesPerFileFormat(t *testing.T) {
	_, statePath := migrationHome(t)
	_ = os.MkdirAll(filepath.Dir(statePath), 0o755)
	writeFile(t, statePath, `{"status":"imported","source_shell":"zsh","files":[{"path":"/a/.zprofile","mtime":"2026-01-02T03:04:05Z"},{"path":"/a/.zshrc"}],"import":{"time":"2026-01-02T03:04:05Z","summary":{"aliases":1}}}`)
	state, err := LoadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(state.Paths, []string{"/a/.zprofile", "/a/.zshrc"}) || len(state.Import.Mtimes) != 1 ||
		state.Import.Mtimes["/a/.zprofile"].Year() != 2026 {
		t.Errorf("upgrade = %+v (import %+v)", state, state.Import)
	}
	writeFile(t, statePath, `{"status":"declined","source_shell":"zsh","files":[{"path":"/a/.zshrc","mtime":"2026-01-02T03:04:05Z"}]}`)
	if state, err = LoadState(statePath); err != nil || state.Import != nil || !slices.Equal(state.Paths, []string{"/a/.zshrc"}) {
		t.Errorf("declined upgrade = %+v err = %v", state, err)
	}
}

// An imported migration that names no files is no state startup can act
// on: `{}` upgraded to Imported with nothing to source, and startup then
// skipped the first-run prompt for good. It is an error naming the file.
func TestLoadState_ImportedWithoutFilesIsInvalid(t *testing.T) {
	_, statePath := migrationHome(t)
	_ = os.MkdirAll(filepath.Dir(statePath), 0o755)
	for _, content := range []string{`{}`, `{"status":"imported","import":{"time":"2026-01-02T03:04:05Z"}}`} {
		writeFile(t, statePath, content)
		state, err := LoadState(statePath)
		if err == nil || !strings.Contains(err.Error(), statePath) || !strings.Contains(err.Error(), "names no files") {
			t.Errorf("LoadState(%s) = %+v, %v; want an error that it names no files", content, state, err)
		}
	}
	writeFile(t, statePath, `{"status":"declined"}`)
	if _, err := LoadState(statePath); err != nil {
		t.Errorf("a declined migration needs no files: %v", err)
	}
}

// FileError names the file once, home as ~: a PathError of the file itself
// gives only its cause, one of another file stays whole.
func TestFileError_NamesTheFileOnce(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home")
	}
	rc := filepath.Join(home, ".zshrc")
	for _, tc := range []struct {
		err  error
		want string
	}{
		{&fs.PathError{Op: "open", Path: rc, Err: fs.ErrPermission}, "~/.zshrc: permission denied"},
		{&fs.PathError{Op: "open", Path: "/etc/other", Err: fs.ErrPermission}, "~/.zshrc: open /etc/other: permission denied"},
		{errors.New("not loaded"), "~/.zshrc: not loaded"},
	} {
		got := FileError(rc, tc.err)
		if got.Error() != tc.want || !errors.Is(got, tc.err) && !errors.Is(got, fs.ErrPermission) {
			t.Errorf("FileError(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}
}
