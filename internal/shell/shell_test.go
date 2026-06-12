package shell

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tfcace/hash/internal/agent"
	"github.com/tfcace/hash/internal/allowlist"
	"github.com/tfcace/hash/internal/config"
	"github.com/tfcace/hash/internal/executor"
	"github.com/tfcace/hash/internal/prompt"
)

func TestNewShell(t *testing.T) {
	cfg := config.Default()

	sh, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if sh == nil {
		t.Error("New() returned nil")
	}
}

func TestWriteAgentNotConfiguredHintUsesACPDefault(t *testing.T) {
	var out strings.Builder

	writeAgentNotConfiguredHint(&out)
	output := out.String()

	if !strings.Contains(output, "command = \"claude-agent-acp\"") {
		t.Fatalf("expected claude-agent-acp config snippet, got:\n%s", output)
	}
	if strings.Contains(output, "command = \"claude\"") {
		t.Fatalf("did not expect deprecated claude command snippet, got:\n%s", output)
	}
	if !strings.Contains(output, "npm install -g @agentclientprotocol/claude-agent-acp") {
		t.Fatalf("expected install command in setup hint, got:\n%s", output)
	}
}

func TestNewShell_HistoryDisabled(t *testing.T) {
	cfg := config.Default()
	cfg.History.Enabled = false

	sh, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer sh.Close()

	if sh.history != nil {
		t.Fatal("history should be nil when history.enabled is false")
	}
}

func TestNewShell_HistoryPathFromConfig(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := config.Default()
	cfg.History.Path = filepath.Join(tmpDir, "custom-history.db")

	sh, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer sh.Close()

	if sh.history == nil {
		t.Fatal("history should be initialized")
	}
	if sh.historyPath != cfg.History.Path {
		t.Fatalf("historyPath = %q, want %q", sh.historyPath, cfg.History.Path)
	}
	if _, err := os.Stat(cfg.History.Path); err != nil {
		t.Fatalf("history database was not created at configured path: %v", err)
	}
}

func TestShell_ChpwdHook(t *testing.T) {
	// Save and restore working directory
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get cwd: %v", err)
	}
	defer os.Chdir(origDir)

	tmpDir := t.TempDir()
	tmpDirResolved, _ := filepath.EvalSymlinks(tmpDir)

	// Create a marker file path for the hook to write to
	markerFile := filepath.Join(tmpDir, "chpwd-marker")

	cfg := config.Default()
	cfg.Shell.Hooks.Chpwd = []string{
		`echo "$PWD" >> ` + markerFile,
	}

	// Create a minimal shell-like setup to test runChpwdHook
	e := executor.New()

	sh := &Shell{
		config:   cfg,
		executor: e,
		prevCwd:  origDir,
	}

	ctx := context.Background()

	// Before cd: hook should not fire (same dir)
	sh.runChpwdHook(ctx)
	if _, err := os.Stat(markerFile); err == nil {
		t.Error("chpwd hook should not fire when directory hasn't changed")
	}

	// Change directory
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("chdir failed: %v", err)
	}
	e.SetExportedEnv("PWD", tmpDir)
	e.SyncRunnerDir()

	// Now hook should fire
	sh.runChpwdHook(ctx)

	content, err := os.ReadFile(markerFile)
	if err != nil {
		t.Fatalf("marker file should exist after chpwd: %v", err)
	}

	got := strings.TrimSpace(string(content))
	gotResolved, _ := filepath.EvalSymlinks(got)
	if gotResolved != tmpDirResolved {
		t.Errorf("chpwd hook recorded %q, want %q", got, tmpDirResolved)
	}

	// Running hook again without directory change should NOT append
	sh.runChpwdHook(ctx)
	content2, _ := os.ReadFile(markerFile)
	lines1 := strings.Count(strings.TrimSpace(string(content)), "\n") + 1
	lines2 := strings.Count(strings.TrimSpace(string(content2)), "\n") + 1
	if lines2 != lines1 {
		t.Errorf("chpwd hook should not fire again when directory hasn't changed (got %d lines, want %d)", lines2, lines1)
	}
}

func TestShell_ChpwdHook_NoConfig(t *testing.T) {
	cfg := config.Default()
	// No hooks configured - should be a no-op
	sh := &Shell{
		config:   cfg,
		executor: executor.New(),
		prevCwd:  "/tmp",
	}

	// Should not panic
	sh.runChpwdHook(context.Background())
}

func TestShell_ZoxideWorksWithDisabledCdBuiltin(t *testing.T) {
	if _, err := exec.LookPath("zoxide"); err != nil {
		t.Skip("zoxide not installed")
	}

	origDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get cwd: %v", err)
	}
	defer os.Chdir(origDir) //nolint:errcheck

	cfg := config.Default()
	cfg.Shell.DisableBuiltins = []string{"cd"}

	e := executor.New()
	sh := &Shell{
		config:   cfg,
		executor: e,
	}

	ctx := context.Background()
	_, err = e.Execute(ctx, `eval "$(zoxide init bash)"`, nil, nil)
	if err != nil {
		t.Fatalf("failed to initialize zoxide: %v", err)
	}

	if err := sh.executeRegularCommand(ctx, "z ~"); err != nil {
		t.Fatalf("z command failed: %v", err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get cwd after z: %v", err)
	}
	cwdResolved, _ := filepath.EvalSymlinks(cwd)

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("failed to get home: %v", err)
	}
	homeResolved, _ := filepath.EvalSymlinks(home)

	if cwdResolved != homeResolved {
		t.Fatalf("z should change to home: got %q, want %q", cwdResolved, homeResolved)
	}
}

func TestShell_ModeMarkers(t *testing.T) {
	// Clear markers first
	os.Unsetenv("HASH_LOGIN")
	os.Unsetenv("HASH_INTERACTIVE")

	cfg := config.Default()
	mode := Mode{Login: true, Interactive: true}

	sh, err := NewWithMode(cfg, mode)
	if err != nil {
		t.Fatalf("failed to create shell: %v", err)
	}
	defer sh.Close()

	// Check that mode is stored
	if !sh.mode.Login {
		t.Error("expected Login mode to be true")
	}
	if !sh.mode.Interactive {
		t.Error("expected Interactive mode to be true")
	}
}

func TestShell_HandleToolPermission_RefreshesProjectAllowlist(t *testing.T) {
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get cwd: %v", err)
	}
	defer os.Chdir(origDir) //nolint:errcheck

	projectA := t.TempDir()
	projectB := t.TempDir()
	configDir := t.TempDir()

	allowA := allowlist.New("project", projectA, configDir)
	if err := allowA.Allow("git status"); err != nil {
		t.Fatalf("Allow(projectA): %v", err)
	}

	allowB := allowlist.New("project", projectB, configDir)
	if err := allowB.Allow("npm test"); err != nil {
		t.Fatalf("Allow(projectB): %v", err)
	}

	sh := &Shell{
		allowlist:    allowlist.New("project", projectA, configDir),
		agentOutput:  NewAgentOutputCoordinator(io.Discard),
		responseUI:   NewResponseUI(io.Discard),
		colorPalette: prompt.DefaultPalette(),
		readKey: func(context.Context) byte {
			return 'n'
		},
	}

	if err := os.Chdir(projectB); err != nil {
		t.Fatalf("chdir(projectB): %v", err)
	}

	allow, always := sh.handleToolPermission(context.Background(), agent.ToolPermissionRequest{Command: "git status", ToolName: "Bash"})
	if allow || always {
		t.Fatalf("projectA approval should not be reused in projectB, got allow=%v always=%v", allow, always)
	}

	allow, always = sh.handleToolPermission(context.Background(), agent.ToolPermissionRequest{Command: "npm test", ToolName: "Bash"})
	if !allow || always {
		t.Fatalf("projectB allowlist should be loaded after chdir, got allow=%v always=%v", allow, always)
	}
}

func TestShell_ExecuteRegularCommand_BuiltinFailureUpdatesLastError(t *testing.T) {
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get cwd: %v", err)
	}
	defer os.Chdir(origDir) //nolint:errcheck

	storeDir := t.TempDir()
	if err := os.Chdir(storeDir); err != nil {
		t.Fatalf("chdir(storeDir): %v", err)
	}

	sh := &Shell{
		config:       config.Default(),
		executor:     executor.New(),
		lastCommand:  "echo ok",
		lastStderr:   "old stderr",
		lastExitCode: 7,
	}

	missingDir := filepath.Join(storeDir, "missing")
	command := "cd " + missingDir
	if err := sh.executeRegularCommand(context.Background(), command); err != nil {
		t.Fatalf("executeRegularCommand(): %v", err)
	}

	if sh.lastExitCode != 1 {
		t.Fatalf("lastExitCode = %d, want 1", sh.lastExitCode)
	}
	if sh.lastCommand != command {
		t.Fatalf("lastCommand = %q, want %q", sh.lastCommand, command)
	}
	if !strings.Contains(sh.lastStderr, "no such file or directory") {
		t.Fatalf("lastStderr = %q, want builtin failure message", sh.lastStderr)
	}
	lastCwdResolved, _ := filepath.EvalSymlinks(sh.lastCwd)
	storeDirResolved, _ := filepath.EvalSymlinks(storeDir)
	if lastCwdResolved != storeDirResolved {
		t.Fatalf("lastCwd = %q, want %q", sh.lastCwd, storeDir)
	}
}

func TestConfirmationTypeForAgentResponse(t *testing.T) {
	t.Run("command requires confirmation", func(t *testing.T) {
		confirmType, ok := confirmationTypeForAgentResponse(agent.Response{Type: agent.ResponseTypeCommand}, false)
		if !ok {
			t.Fatal("expected command response to require confirmation")
		}
		if confirmType != ConfirmTypeCommand {
			t.Fatalf("confirmType = %v, want %v", confirmType, ConfirmTypeCommand)
		}
	})

	t.Run("explanation skips confirmation without reply", func(t *testing.T) {
		if _, ok := confirmationTypeForAgentResponse(agent.Response{Type: agent.ResponseTypeExplanation}, false); ok {
			t.Fatal("expected explanation response to skip confirmation")
		}
	})

	t.Run("explanation can request reply confirmation", func(t *testing.T) {
		confirmType, ok := confirmationTypeForAgentResponse(agent.Response{Type: agent.ResponseTypeExplanation}, true)
		if !ok {
			t.Fatal("expected explanation response to require confirmation when reply is allowed")
		}
		if confirmType != ConfirmTypeExplanation {
			t.Fatalf("confirmType = %v, want %v", confirmType, ConfirmTypeExplanation)
		}
	})
}
