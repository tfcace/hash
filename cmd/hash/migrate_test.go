// cmd/hash/migrate_test.go
package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tfcace/hash/internal/compat"
	"github.com/tfcace/hash/internal/executor"
)

func TestMigrateStatus_NoState(t *testing.T) {
	var stdout bytes.Buffer
	err := runMigrateStatus(&stdout, "/nonexistent/migration.json")
	if err == nil {
		t.Error("expected error when no migration state exists")
	}
}

func TestMigrateShellDialectReadsConfig(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HASH_CONFIG_DIR", tmpDir)

	configPath := filepath.Join(tmpDir, "config.toml")
	if err := os.WriteFile(configPath, []byte("[shell]\ndialect = \"zsh\"\n"), 0o644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	if got := migrateShellDialect(); got != "zsh" {
		t.Fatalf("migrateShellDialect() = %q, want zsh", got)
	}
}

func migrateHome(t *testing.T, files map[string]string) (home, statePath string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("HASH_CONFIG_DIR", filepath.Join(home, "cfg"))
	t.Setenv("SHELL", "/bin/zsh")
	t.Setenv("HASH_SHELL", "")
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(home, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return home, compat.DefaultStatePath()
}

// `hash migrate` used to overwrite ~/.hashrc, wiping the user's additions.
func TestMigrate_KeepsExistingHashrc(t *testing.T) {
	home, statePath := migrateHome(t, map[string]string{
		".zshrc":  "alias ll='ls -la'\n",
		".hashrc": "alias mine=1\n",
	})
	var out bytes.Buffer
	if code := migrateInteractive(strings.NewReader("y\n"), &out, statePath); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if data, _ := os.ReadFile(filepath.Join(home, ".hashrc")); string(data) != "alias mine=1\n" {
		t.Errorf(".hashrc overwritten:\n%s", data)
	}
	if state, err := compat.LoadState(statePath); err != nil || state.Status != compat.StatusImported {
		t.Errorf("state = %+v err = %v, want imported", state, err)
	}
}

// Answering no to a re-run of `hash migrate` keeps an existing import
// instead of silently turning it into a decline.
func TestMigrate_NoKeepsExistingImport(t *testing.T) {
	_, statePath := migrateHome(t, map[string]string{".zshrc": "alias ll='ls -la'\n"})
	var out bytes.Buffer
	migrateInteractive(strings.NewReader("y\n"), &out, statePath)
	migrateInteractive(strings.NewReader("n\n"), &out, statePath)

	state, err := compat.LoadState(statePath)
	if err != nil || state.Status != compat.StatusImported {
		t.Fatalf("state = %+v err = %v, want still imported", state, err)
	}
}

func TestMigrate_NoWithoutImportDeclines(t *testing.T) {
	_, statePath := migrateHome(t, map[string]string{".zshrc": "alias ll='ls -la'\n"})
	var out bytes.Buffer
	migrateInteractive(strings.NewReader("n\n"), &out, statePath)
	state, err := compat.LoadState(statePath)
	if err != nil || state.Status != compat.StatusDeclined {
		t.Fatalf("state = %+v err = %v, want declined", state, err)
	}
	out.Reset()
	if err := runMigrateStatus(&out, statePath); err != nil || !strings.Contains(out.String(), "declined") {
		t.Errorf("status = %q err = %v, want it to say declined", out.String(), err)
	}
}

func TestMigrate_EOFChangesNothing(t *testing.T) {
	_, statePath := migrateHome(t, map[string]string{".zshrc": "alias ll='ls -la'\n"})
	var out bytes.Buffer
	if code := migrateInteractive(strings.NewReader(""), &out, statePath); code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Errorf("EOF should save nothing, stat err = %v", err)
	}
}

// `hash migrate` typed in hash runs in a child process: nothing it imports
// loads into the session it was typed in, so it says the import loads in
// new hash sessions instead of that it loaded.
func TestMigrate_InHashSaysItLoadsInNewSessions(t *testing.T) {
	_, statePath := migrateHome(t, map[string]string{".zshrc": "alias ll='ls -la'\n"})
	t.Setenv("HASH_SHELL", "1")
	var out bytes.Buffer
	if code := migrateInteractive(strings.NewReader("y\n"), &out, statePath); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	got := out.String()
	if strings.Contains(got, "Loaded from") || !strings.Contains(got, "Will load from ~/.zshrc:") ||
		!strings.Contains(got, "Loads in new hash sessions: open a new tab, or run 'exec hash -l'.") {
		t.Errorf("output does not say the import loads in new sessions:\n%s", got)
	}
}

// Outside hash, the summary is the one hash's first start prints.
func TestMigrate_OutsideHashSaysLoaded(t *testing.T) {
	_, statePath := migrateHome(t, map[string]string{".zshrc": "alias ll='ls -la'\n"})
	var out bytes.Buffer
	if code := migrateInteractive(strings.NewReader("y\n"), &out, statePath); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if got := out.String(); !strings.Contains(got, "Loaded from ~/.zshrc:") || strings.Contains(got, "new hash sessions") {
		t.Errorf("output:\n%s", got)
	}
}

// --from used to record an import without creating ~/.hashrc.
func TestMigrateFrom_CreatesHashrcAndState(t *testing.T) {
	home, statePath := migrateHome(t, map[string]string{".zshrc": "alias ll='ls -la'\n"})
	if code := runMigrateFrom("zsh", nil); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if _, err := os.Stat(filepath.Join(home, ".hashrc")); err != nil {
		t.Errorf("no .hashrc after migrate --from: %v", err)
	}
	state, err := compat.LoadState(statePath)
	if err != nil || state.Status != compat.StatusImported || len(state.Paths) != 1 || state.Import.Mtimes[state.Paths[0]].IsZero() {
		t.Fatalf("state = %+v err = %v", state, err)
	}
	var status bytes.Buffer
	if err := runMigrateStatus(&status, statePath); err != nil || !strings.Contains(status.String(), "1 alias\n") {
		t.Errorf("status = %q err = %v", status.String(), err)
	}
}

// A broken unrelated section must not reset the configured dialect.
func TestMigrateShellDialect_PartialConfigKeepsDialect(t *testing.T) {
	home, _ := migrateHome(t, nil)
	cfgDir := filepath.Join(home, "cfg")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte("[shell]\ndialect = \"zsh\"\n[history]\nenabled = 3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := migrateShellDialect(); got != "zsh" {
		t.Fatalf("migrateShellDialect() = %q, want zsh", got)
	}
}

// A state file that exists but cannot be read is not "no migration": status
// names the problem (startup prints it on every launch) and how to fix it.
func TestMigrateStatus_UnreadableStateSaysWhy(t *testing.T) {
	for name, content := range map[string]string{
		"corrupt":        "{not json",
		"unknown status": `{"status": "bogus"}`,
	} {
		t.Run(name, func(t *testing.T) {
			statePath := filepath.Join(t.TempDir(), "migration.json")
			if err := os.WriteFile(statePath, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
			err := runMigrateStatus(&bytes.Buffer{}, statePath)
			if err == nil {
				t.Fatal("runMigrateStatus() = nil, want an error")
			}
			if msg := err.Error(); strings.Contains(msg, "no migration found") || !strings.Contains(msg, statePath) || !strings.Contains(msg, "hash migrate") {
				t.Errorf("error = %q, want the state file's problem and a hint to re-run hash migrate", msg)
			}
		})
	}
}

// blockStateDir puts a file where the state's directory should be, so the
// state cannot be saved.
func blockStateDir(t *testing.T, statePath string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Dir(statePath)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Dir(statePath), []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// An import whose state cannot be saved did not happen: exit non-zero.
func TestMigrate_SaveFailureExitsNonZero(t *testing.T) {
	home, statePath := migrateHome(t, map[string]string{".zshrc": "alias ll='ls -la'\n"})
	blockStateDir(t, statePath)
	var out bytes.Buffer
	if code := migrateInteractive(strings.NewReader("y\n"), &out, statePath); code == 0 {
		t.Errorf("exit = 0 after the state could not be saved")
	}
	if _, err := os.Stat(filepath.Join(home, ".hashrc")); !os.IsNotExist(err) {
		t.Errorf(".hashrc created although nothing was recorded (stat err %v)", err)
	}
}

// A decline that cannot be saved is not recorded either.
func TestMigrate_DeclineSaveFailureExitsNonZero(t *testing.T) {
	_, statePath := migrateHome(t, map[string]string{".zshrc": "alias ll='ls -la'\n"})
	blockStateDir(t, statePath)
	var out bytes.Buffer
	if code := migrateInteractive(strings.NewReader("n\n"), &out, statePath); code == 0 {
		t.Errorf("exit = 0 after the decline could not be saved")
	}
	if strings.Contains(out.String(), "No problem") {
		t.Errorf("output claims the decline was recorded:\n%s", out.String())
	}
}

// Output that is not a terminal carries no color codes.
func TestMigrate_PipedOutputHasNoColors(t *testing.T) {
	_, statePath := migrateHome(t, map[string]string{".zshrc": "alias ll='ls -la'\nsetopt autocd\n"})
	var out bytes.Buffer
	if code := migrateInteractive(strings.NewReader("y\n"), &out, statePath); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if strings.Contains(out.String(), "\x1b[") {
		t.Errorf("piped output has escape codes:\n%q", out.String())
	}
}

// The .hashrc header points at `hash migrate status` to see what was
// skipped, so status lists the skipped lines, and counts read as English.
func TestMigrateStatus_ListsSkippedLines(t *testing.T) {
	_, statePath := migrateHome(t, map[string]string{".zshrc": "alias ll='ls -la'\nsetopt autocd\n"})
	var out bytes.Buffer
	if code := migrateInteractive(strings.NewReader("y\n"), &out, statePath); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	out.Reset()
	if err := runMigrateStatus(&out, statePath); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"1 alias\n", "Skipped: 1 item\n", "~/.zshrc:2: setopt autocd (zsh shell option"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("status lacks %q:\n%s", want, out.String())
		}
	}
}

// migrate status names the migrated files with home as ~, as it does the
// skipped lines.
func TestMigrateStatus_WritesHomeAsTilde(t *testing.T) {
	_, statePath := migrateHome(t, map[string]string{".zshrc": "alias ll='ls -la'\nsetopt autocd\n"})
	if code := runMigrateFrom("zsh", nil); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	var status bytes.Buffer
	if err := runMigrateStatus(&status, statePath); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(status.String(), "Migration from ~/.zshrc (shell: zsh)") || !strings.Contains(status.String(), "  ~/.zshrc:2:") {
		t.Errorf("status = %q", status.String())
	}
}

// migrate status names a file the zsh parser rejected, from which line it
// is not loaded, and why.
func TestMigrateStatus_NamesFilesNotLoadedFromALine(t *testing.T) {
	_, statePath := migrateHome(t, map[string]string{".zshrc": "alias ll='ls -la'\nfor f (a b); do echo $f; done\nalias la='ls -a'\n"})
	if code := runMigrateFrom("zsh", nil); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	var status bytes.Buffer
	if err := runMigrateStatus(&status, statePath); err != nil {
		t.Fatal(err)
	}
	out := status.String()
	if !strings.Contains(out, "~/.zshrc: not loaded from line 2: ") || strings.Contains(out, "line by line") {
		t.Errorf("status = %q", out)
	}
}

// A migrated file the filter cannot load does not disappear from the status:
// it is listed as not loaded, with why.
func TestMigrateStatus_ListsFilesNotLoaded(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads any file")
	}
	home, statePath := migrateHome(t, map[string]string{".zshrc": "alias ll='ls -la'\n"})
	if code := runMigrateFrom("zsh", nil); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	rc := filepath.Join(home, ".zshrc")
	if err := os.Chmod(rc, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(rc, 0o644) })
	var status bytes.Buffer
	if err := runMigrateStatus(&status, statePath); err != nil {
		t.Fatal(err)
	}
	if out := status.String(); !strings.Contains(out, "Not loaded:\n  ") || !strings.Contains(out, ".zshrc: permission denied") {
		t.Errorf("status = %q", out)
	}
}

// When no migrated file loads, status says so: it showed the last import's
// counts as what was imported while listing every file as not loaded.
func TestMigrateStatus_NothingLoaded(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads any file")
	}
	home, statePath := migrateHome(t, map[string]string{".zshrc": "alias ll='ls -la'\n"})
	if code := runMigrateFrom("zsh", nil); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	rc := filepath.Join(home, ".zshrc")
	if err := os.Chmod(rc, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(rc, 0o644) })
	var status bytes.Buffer
	if err := runMigrateStatus(&status, statePath); err != nil {
		t.Fatal(err)
	}
	out := status.String()
	if strings.Contains(out, "1 alias") || !strings.Contains(out, "Imported: nothing") {
		t.Errorf("status = %q", out)
	}
	if !strings.Contains(out, "  ~/.zshrc: permission denied\n") {
		t.Errorf("status names the file other than once, as ~/.zshrc: %q", out)
	}
}

// migrate status lists a migrated file in which a statement does not parse
// under Not loaded, with the line, and does not count it a skipped item.
func TestMigrateStatus_ListsAFileThatDoesNotParse(t *testing.T) {
	_, statePath := migrateHome(t, map[string]string{".zshrc": "alias ll='ls -la'\n}\n"})
	if code := runMigrateFrom("zsh", nil); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	var status bytes.Buffer
	if err := runMigrateStatus(&status, statePath); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Skipped: 0 items\n", "Not loaded:\n  ~/.zshrc: not loaded: line 2: "} {
		if !strings.Contains(status.String(), want) {
			t.Errorf("status lacks %q:\n%s", want, status.String())
		}
	}
}

// migrate status lists the files the migrated files source that did not
// load at the last startup.
func TestMigrateStatus_ListsSourcedFilesNotLoaded(t *testing.T) {
	home, statePath := migrateHome(t, map[string]string{".zshrc": "source ~/omz.sh\n"})
	if code := runMigrateFrom("zsh", nil); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	state, err := compat.LoadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	state.Import.SourcedNotLoaded = []compat.NotLoaded{{File: filepath.Join(home, "omz.sh"), Line: 24, Reason: "boom", Load: executor.LoadedBefore}}
	if err := state.Save(statePath); err != nil {
		t.Fatal(err)
	}
	var status bytes.Buffer
	if err := runMigrateStatus(&status, statePath); err != nil {
		t.Fatal(err)
	}
	if want := "Not loaded:\n  ~/omz.sh: not loaded from line 24: boom\n"; !strings.Contains(status.String(), want) {
		t.Errorf("status lacks %q:\n%s", want, status.String())
	}
}

// migrate status lists a file not loaded once: a migrated file that the
// last startup also found not loaded where another one sourced it (the
// state of an earlier hash names it) was listed twice.
func TestMigrateStatus_ListsAFileNotLoadedOnce(t *testing.T) {
	home, statePath := migrateHome(t, map[string]string{".zshrc": "alias ll='ls -la'\n}\n"})
	if code := runMigrateFrom("zsh", nil); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	state, err := compat.LoadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	state.Import.SourcedNotLoaded = []compat.NotLoaded{{File: filepath.Join(home, ".zshrc"), Line: 2, Reason: "boom"}}
	if err := state.Save(statePath); err != nil {
		t.Fatal(err)
	}
	var status bytes.Buffer
	if err := runMigrateStatus(&status, statePath); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(status.String(), "~/.zshrc: not loaded"); n != 1 {
		t.Errorf("status lists ~/.zshrc not loaded %d times, want once:\n%s", n, status.String())
	}
}
