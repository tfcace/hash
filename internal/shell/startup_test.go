package shell

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tfcace/hash/internal/compat"
	"github.com/tfcace/hash/internal/config"
)

func TestStartup_LoginShell_SourcesProfileThenRC(t *testing.T) {
	// Clean environment to avoid interference from previous tests or migration
	os.Unsetenv("PROFILE_SOURCED")
	os.Unsetenv("RC_AFTER_PROFILE")

	// Isolate from system state by setting HOME and XDG_DATA_HOME to temp dir
	tmpDir := t.TempDir()
	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	os.Setenv("XDG_DATA_HOME", tmpDir)
	defer func() {
		os.Setenv("HOME", oldHome)
		os.Unsetenv("XDG_DATA_HOME")
	}()

	// Create test profile that sets a marker
	profilePath := filepath.Join(tmpDir, "profile")
	if err := os.WriteFile(profilePath, []byte("export PROFILE_SOURCED=1\n"), 0644); err != nil {
		t.Fatalf("failed to write profile: %v", err)
	}

	// Create test rc that checks profile was sourced first
	rcPath := filepath.Join(tmpDir, "rc")
	rcContent := `if [ "$PROFILE_SOURCED" = "1" ]; then
    export RC_AFTER_PROFILE=yes
else
    export RC_AFTER_PROFILE=no
fi
`
	if err := os.WriteFile(rcPath, []byte(rcContent), 0644); err != nil {
		t.Fatalf("failed to write rc: %v", err)
	}

	cfg := config.Default()
	cfg.Shell.StartupFiles.Login = []string{profilePath}
	cfg.Shell.StartupFiles.Interactive = []string{rcPath}
	cfg.Shell.InitCommands = nil

	mode := Mode{Login: true, Interactive: true}
	sh, err := NewWithMode(cfg, mode)
	if err != nil {
		t.Fatalf("failed to create shell: %v", err)
	}
	defer sh.Close()

	// Run startup
	ctx := context.Background()
	if err := sh.runStartup(ctx); err != nil {
		t.Fatalf("startup failed: %v", err)
	}

	// Check that rc ran after profile
	if os.Getenv("RC_AFTER_PROFILE") != "yes" {
		t.Errorf("expected RC_AFTER_PROFILE=yes, got '%s'", os.Getenv("RC_AFTER_PROFILE"))
	}
}

func TestStartup_NonLoginInteractive_SkipsProfile(t *testing.T) {
	// Isolate from system state by setting HOME and XDG_DATA_HOME to temp dir
	tmpDir := t.TempDir()
	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	os.Setenv("XDG_DATA_HOME", tmpDir)
	defer func() {
		os.Setenv("HOME", oldHome)
		os.Unsetenv("XDG_DATA_HOME")
	}()

	// Clean environment
	os.Unsetenv("PROFILE_RAN")
	os.Unsetenv("RC_RAN")

	// Create profile that would fail if sourced
	profilePath := filepath.Join(tmpDir, "profile")
	if err := os.WriteFile(profilePath, []byte("export PROFILE_RAN=1\n"), 0644); err != nil {
		t.Fatalf("failed to write profile: %v", err)
	}

	// Create rc
	rcPath := filepath.Join(tmpDir, "rc")
	if err := os.WriteFile(rcPath, []byte("export RC_RAN=1\n"), 0644); err != nil {
		t.Fatalf("failed to write rc: %v", err)
	}

	cfg := config.Default()
	cfg.Shell.StartupFiles.Login = []string{profilePath}
	cfg.Shell.StartupFiles.Interactive = []string{rcPath}
	cfg.Shell.InitCommands = nil

	mode := Mode{Login: false, Interactive: true}
	sh, err := NewWithMode(cfg, mode)
	if err != nil {
		t.Fatalf("failed to create shell: %v", err)
	}
	defer sh.Close()

	ctx := context.Background()
	if err := sh.runStartup(ctx); err != nil {
		t.Fatalf("startup failed: %v", err)
	}

	// Profile should NOT have run
	if os.Getenv("PROFILE_RAN") == "1" {
		t.Error("profile should not run for non-login shell")
	}

	// RC should have run
	if os.Getenv("RC_RAN") != "1" {
		t.Error("rc should run for interactive shell")
	}
}

func TestStartup_NonInteractive_SkipsRC(t *testing.T) {
	// Isolate from system state by setting HOME and XDG_DATA_HOME to temp dir
	tmpDir := t.TempDir()
	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	os.Setenv("XDG_DATA_HOME", tmpDir)
	defer func() {
		os.Setenv("HOME", oldHome)
		os.Unsetenv("XDG_DATA_HOME")
	}()

	// Clean environment
	os.Unsetenv("RC_RAN")

	rcPath := filepath.Join(tmpDir, "rc")
	if err := os.WriteFile(rcPath, []byte("export RC_RAN=1\n"), 0644); err != nil {
		t.Fatalf("failed to write rc: %v", err)
	}

	cfg := config.Default()
	cfg.Shell.StartupFiles.Login = nil
	cfg.Shell.StartupFiles.Interactive = []string{rcPath}
	cfg.Shell.InitCommands = nil

	mode := Mode{Login: false, Interactive: false}
	sh, err := NewWithMode(cfg, mode)
	if err != nil {
		t.Fatalf("failed to create shell: %v", err)
	}
	defer sh.Close()

	ctx := context.Background()
	if err := sh.runStartup(ctx); err != nil {
		t.Fatalf("startup failed: %v", err)
	}

	// RC should NOT have run
	if os.Getenv("RC_RAN") == "1" {
		t.Error("rc should not run for non-interactive shell")
	}
}

func TestStartup_SourceMigrationFilesUsesZshDialect(t *testing.T) {
	const envName = "HASH_TEST_ZSH_MIGRATION_MARKER"
	tmpDir := t.TempDir()
	dataDir := filepath.Join(tmpDir, "data")
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", dataDir)
	t.Setenv(envName, "")

	zshrc := filepath.Join(tmpDir, ".zshrc")
	content := `: &!
export ` + envName + `=loaded
`
	if err := os.WriteFile(zshrc, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write zshrc: %v", err)
	}

	state := &compat.State{
		Status:      compat.StatusImported,
		SourceShell: "zsh",
		Paths:       []string{zshrc},
		Import:      &compat.ImportRecord{},
	}
	if err := state.Save(compat.DefaultStatePath()); err != nil {
		t.Fatalf("failed to save migration state: %v", err)
	}

	cfg := config.Default()
	cfg.Shell.Dialect = "zsh"
	cfg.Shell.StartupFiles.Login = nil
	cfg.Shell.StartupFiles.Interactive = nil
	cfg.Shell.InitCommands = nil

	sh, err := NewWithMode(cfg, Mode{Interactive: true})
	if err != nil {
		t.Fatalf("failed to create shell: %v", err)
	}
	defer sh.Close()

	sh.runMigration(context.Background(), sh.startup(), compat.LineReader(strings.NewReader("")), io.Discard)

	var stdout, stderr bytes.Buffer
	if _, err := sh.executor.Execute(context.Background(), `echo "$`+envName+`"`, &stdout, &stderr); err != nil {
		t.Fatalf("echo failed: %v, stderr: %s", err, stderr.String())
	}
	if got := strings.TrimSpace(stdout.String()); got != "loaded" {
		t.Fatalf("migration export = %q, want loaded", got)
	}
}

func TestStartup_ConfiguredZshStartupFileUsesZshDialect(t *testing.T) {
	const envName = "HASH_TEST_ZSH_STARTUP_MARKER"
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", filepath.Join(tmpDir, "data"))
	t.Setenv(envName, "")

	zshrc := filepath.Join(tmpDir, ".zshrc")
	content := `: &!
export ` + envName + `=loaded
`
	if err := os.WriteFile(zshrc, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write zshrc: %v", err)
	}
	if err := (&compat.State{Status: compat.StatusDeclined}).Save(compat.DefaultStatePath()); err != nil {
		t.Fatalf("failed to save migration state: %v", err)
	}

	cfg := config.Default()
	cfg.Shell.Dialect = "zsh"
	cfg.Shell.StartupFiles.Login = nil
	cfg.Shell.StartupFiles.Interactive = []string{zshrc}
	cfg.Shell.InitCommands = nil

	sh, err := NewWithMode(cfg, Mode{Interactive: true})
	if err != nil {
		t.Fatalf("failed to create shell: %v", err)
	}
	defer sh.Close()

	if err := sh.runStartup(context.Background()); err != nil {
		t.Fatalf("startup failed: %v", err)
	}

	var stdout, stderr bytes.Buffer
	if _, err := sh.executor.Execute(context.Background(), `echo "$`+envName+`"`, &stdout, &stderr); err != nil {
		t.Fatalf("echo failed: %v, stderr: %s", err, stderr.String())
	}
	if got := strings.TrimSpace(stdout.String()); got != "loaded" {
		t.Fatalf("startup export = %q, want loaded", got)
	}
}

func TestStartup_ConfiguredZshStartupFileFiltersSkippedLineInsideIf(t *testing.T) {
	const envName = "HASH_TEST_ZSH_STARTUP_FILTER_MARKER"
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", filepath.Join(tmpDir, "data"))
	t.Setenv("TERM", "xterm-256color")
	t.Setenv(envName, "")

	zshrc := filepath.Join(tmpDir, ".zshrc")
	content := `if [[ "${TERM:-}" != "dumb" ]]; then
  _cached_eval starship starship init zsh
fi
export ` + envName + `=loaded
`
	if err := os.WriteFile(zshrc, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write zshrc: %v", err)
	}
	if err := (&compat.State{Status: compat.StatusDeclined}).Save(compat.DefaultStatePath()); err != nil {
		t.Fatalf("failed to save migration state: %v", err)
	}

	cfg := config.Default()
	cfg.Shell.StartupFiles.Login = nil
	cfg.Shell.StartupFiles.Interactive = []string{zshrc}
	cfg.Shell.InitCommands = nil

	sh, err := NewWithMode(cfg, Mode{Interactive: true})
	if err != nil {
		t.Fatalf("failed to create shell: %v", err)
	}
	defer sh.Close()

	if err := sh.runStartup(context.Background()); err != nil {
		t.Fatalf("startup failed: %v", err)
	}

	var stdout, stderr bytes.Buffer
	if _, err := sh.executor.Execute(context.Background(), `echo "$`+envName+`"`, &stdout, &stderr); err != nil {
		t.Fatalf("echo failed: %v, stderr: %s", err, stderr.String())
	}
	if got := strings.TrimSpace(stdout.String()); got != "loaded" {
		t.Fatalf("startup export = %q, want loaded", got)
	}
}

// migrationShell returns an interactive shell in an isolated HOME with the
// given previous-shell files, and the migration state path.
func migrationShell(t *testing.T, shellEnv string, files map[string]string) (*Shell, string, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("SHELL", shellEnv)
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(home, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Default()
	cfg.Shell.StartupFiles.Login = nil
	cfg.Shell.InitCommands = nil
	sh, err := NewWithMode(cfg, Mode{Interactive: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sh.Close() })
	return sh, home, compat.DefaultStatePath()
}

func shellVar(t *testing.T, sh *Shell, name string) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if _, err := sh.executor.Execute(context.Background(), `echo "$`+name+`"`, &stdout, &stderr); err != nil {
		t.Fatalf("echo failed: %v, stderr: %s", err, stderr.String())
	}
	return strings.TrimSpace(stdout.String())
}

// Accepting the first-run prompt must source the files once: importing used
// to source them and then the re-source step sourced them again.
func TestStartup_FirstRunImportSourcesOnce(t *testing.T) {
	sh, home, statePath := migrationShell(t, "/bin/zsh", map[string]string{
		".zshrc": "HASH_TEST_SOURCE_COUNT=\"${HASH_TEST_SOURCE_COUNT}x\"\n",
	})

	var out bytes.Buffer
	sh.runMigration(context.Background(), sh.startup(), compat.LineReader(strings.NewReader("y\n")), &out)

	if got := shellVar(t, sh, "HASH_TEST_SOURCE_COUNT"); got != "x" {
		t.Fatalf("rc sourced %d times (%q), want once", len(got), got)
	}
	state, err := compat.LoadState(statePath)
	if err != nil || state.Status != compat.StatusImported {
		t.Fatalf("state = %+v err = %v, want imported", state, err)
	}
	if _, err := os.Stat(filepath.Join(home, ".hashrc")); err != nil {
		t.Errorf("first-run import should create .hashrc: %v", err)
	}

	// Next launch re-sources once, without a change notice.
	sh2, err := NewWithMode(sh.config, Mode{Interactive: true})
	if err != nil {
		t.Fatal(err)
	}
	defer sh2.Close()
	out.Reset()
	sh2.runMigration(context.Background(), sh2.startup(), compat.LineReader(strings.NewReader("")), &out)
	if got := shellVar(t, sh2, "HASH_TEST_SOURCE_COUNT"); got != "x" {
		t.Errorf("second launch sourced %q, want x", got)
	}
	if strings.Contains(out.String(), "changed") {
		t.Errorf("unexpected change notice: %q", out.String())
	}
}

// A declined single-file migration had a zero mtime, so every startup
// reported the file as changed.
func TestStartup_DeclinedMigrationIsQuiet(t *testing.T) {
	sh, _, statePath := migrationShell(t, "/bin/bash", map[string]string{
		".bashrc": "HASH_TEST_DECLINED=loaded\n",
	})

	var out bytes.Buffer
	sh.runMigration(context.Background(), sh.startup(), compat.LineReader(strings.NewReader("n\n")), &out)
	state, err := compat.LoadState(statePath)
	if err != nil || state.Status != compat.StatusDeclined {
		t.Fatalf("state = %+v err = %v, want declined", state, err)
	}

	out.Reset()
	sh.runMigration(context.Background(), sh.startup(), compat.LineReader(strings.NewReader("")), &out)
	if out.Len() != 0 {
		t.Errorf("declined startup printed %q, want nothing", out.String())
	}
	if got := shellVar(t, sh, "HASH_TEST_DECLINED"); got != "" {
		t.Errorf("declined migration was sourced: %q", got)
	}
}

// No answer (EOF, e.g. Ctrl-D) means "ask later": nothing is saved.
func TestStartup_FirstRunEOFAsksAgainLater(t *testing.T) {
	sh, _, statePath := migrationShell(t, "/bin/zsh", map[string]string{".zshrc": "alias ll='ls -la'\n"})

	var out bytes.Buffer
	sh.runMigration(context.Background(), sh.startup(), compat.LineReader(strings.NewReader("")), &out)
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Fatalf("EOF should save no state, stat err = %v", err)
	}
	state, err := compat.ReadMigration(statePath)
	if err != nil || state.Status != compat.StatusFirstRun {
		t.Fatalf("state = %+v err = %v, want first-run", state, err)
	}
}

// The change notice is printed once per change, then the new mtime is recorded.
func TestStartup_ChangeNoticeShownOnce(t *testing.T) {
	sh, home, _ := migrationShell(t, "/bin/zsh", map[string]string{
		".zprofile": "export HASH_TEST_PROFILE=1\n",
		".zshrc":    "alias ll='ls -la'\n",
	})
	var out bytes.Buffer
	sh.runMigration(context.Background(), sh.startup(), compat.LineReader(strings.NewReader("y\n")), &out)

	zshrc := filepath.Join(home, ".zshrc")
	if err := os.WriteFile(zshrc, []byte("alias ll='ls -la'\nsetopt autocd\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(zshrc, future, future); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	sh.runMigration(context.Background(), sh.startup(), compat.LineReader(strings.NewReader("")), &out)
	if !strings.Contains(out.String(), "~/.zshrc changed: 1 item skipped") {
		t.Fatalf("notice = %q, want ~/.zshrc changed with the fresh skipped count", out.String())
	}

	out.Reset()
	sh.runMigration(context.Background(), sh.startup(), compat.LineReader(strings.NewReader("")), &out)
	if out.Len() != 0 {
		t.Errorf("notice repeated on the next startup: %q", out.String())
	}
}

// ~name is name's home directory, not a directory called name in $HOME.
func TestSourceFile_TildeUserIsNotUnderHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, "hash_no_such_user", "rc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := startupFile("~hash_no_such_user/rc"); !os.IsNotExist(err) {
		t.Errorf("startupFile(~hash_no_such_user/rc) = %v, want a not-exist error", err)
	}
}
