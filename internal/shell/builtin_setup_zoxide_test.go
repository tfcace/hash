package shell

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tfcace/hash/internal/config"
)

func TestSetupZoxide_IsBuiltin(t *testing.T) {
	if !isBuiltin("setup-zoxide") {
		t.Error("setup-zoxide should be a builtin")
	}
}

func TestCdBuiltinDisabled(t *testing.T) {
	tests := []struct {
		name     string
		disabled []string
		want     bool
	}{
		{"empty", nil, false},
		{"other", []string{"pwd"}, false},
		{"cd", []string{"cd"}, true},
		{"cd and others", []string{"pwd", "cd"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{
				Shell: config.ShellConfig{
					DisableBuiltins: tt.disabled,
				},
			}
			if got := cdBuiltinDisabled(cfg); got != tt.want {
				t.Errorf("cdBuiltinDisabled() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestZoxideChpwdHookExists(t *testing.T) {
	tests := []struct {
		name  string
		hooks []string
		want  bool
	}{
		{"empty", nil, false},
		{"other hook", []string{"echo changed"}, false},
		{"zoxide hook", []string{`zoxide add -- "$PWD"`}, true},
		{"zoxide among others", []string{"echo hi", `zoxide add -- "$PWD"`}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{
				Shell: config.ShellConfig{
					Hooks: config.HooksConfig{Chpwd: tt.hooks},
				},
			}
			if got := zoxideChpwdHookExists(cfg); got != tt.want {
				t.Errorf("zoxideChpwdHookExists() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBuildMinimalZoxideConfig(t *testing.T) {
	t.Run("both settings", func(t *testing.T) {
		content := buildMinimalZoxideConfig(true, true)
		if !strings.Contains(content, `disable_builtins = ["cd"]`) {
			t.Error("should contain disable_builtins")
		}
		if !strings.Contains(content, "[shell.hooks]") {
			t.Error("should contain [shell.hooks]")
		}
		if !strings.Contains(content, "chpwd") {
			t.Error("should contain chpwd")
		}
	})

	t.Run("only disable cd", func(t *testing.T) {
		content := buildMinimalZoxideConfig(true, false)
		if !strings.Contains(content, `disable_builtins = ["cd"]`) {
			t.Error("should contain disable_builtins")
		}
		if strings.Contains(content, "[shell.hooks]") {
			t.Error("should not contain [shell.hooks]")
		}
	})

	t.Run("only chpwd hook", func(t *testing.T) {
		content := buildMinimalZoxideConfig(false, true)
		if strings.Contains(content, "disable_builtins") {
			t.Error("should not contain disable_builtins")
		}
		if !strings.Contains(content, "[shell.hooks]") {
			t.Error("should contain [shell.hooks]")
		}
	})
}

func TestAppendZoxideToConfig(t *testing.T) {
	t.Run("append to config with no shell section", func(t *testing.T) {
		existing := "[prompt]\nmode = \"starship\"\n"
		result := appendZoxideToConfig(existing, true, true)

		if !strings.Contains(result, "[shell]") {
			t.Error("should add [shell] section")
		}
		if !strings.Contains(result, `disable_builtins = ["cd"]`) {
			t.Error("should add disable_builtins")
		}
		if !strings.Contains(result, "[shell.hooks]") {
			t.Error("should add [shell.hooks]")
		}
		if !strings.Contains(result, "chpwd") {
			t.Error("should add chpwd hook")
		}
	})

	t.Run("append to config with existing shell section", func(t *testing.T) {
		existing := "[shell]\nkeybindings = \"helix\"\n"
		result := appendZoxideToConfig(existing, true, true)

		// disable_builtins should be inserted after [shell]
		shellIdx := strings.Index(result, "[shell]")
		disableIdx := strings.Index(result, `disable_builtins = ["cd"]`)
		keybindingsIdx := strings.Index(result, "keybindings")

		if disableIdx < shellIdx {
			t.Error("disable_builtins should come after [shell]")
		}
		if disableIdx > keybindingsIdx {
			t.Error("disable_builtins should be inserted before existing keys")
		}
	})

	t.Run("append to config with existing shell.hooks section", func(t *testing.T) {
		existing := "[shell]\nkeybindings = \"helix\"\n\n[shell.hooks]\n"
		result := appendZoxideToConfig(existing, false, true)

		// chpwd should be inserted after [shell.hooks], not create new section
		count := strings.Count(result, "[shell.hooks]")
		if count != 1 {
			t.Errorf("[shell.hooks] should appear once, got %d", count)
		}
		if !strings.Contains(result, "chpwd") {
			t.Error("should add chpwd hook")
		}
	})

	t.Run("only disable cd", func(t *testing.T) {
		existing := "[prompt]\nmode = \"starship\"\n"
		result := appendZoxideToConfig(existing, true, false)

		if !strings.Contains(result, `disable_builtins = ["cd"]`) {
			t.Error("should add disable_builtins")
		}
		if strings.Contains(result, "[shell.hooks]") {
			t.Error("should not add [shell.hooks]")
		}
	})

	t.Run("only chpwd hook", func(t *testing.T) {
		existing := "[shell]\nkeybindings = \"helix\"\n"
		result := appendZoxideToConfig(existing, false, true)

		if strings.Contains(result, "disable_builtins") {
			t.Error("should not add disable_builtins")
		}
		if !strings.Contains(result, "[shell.hooks]") {
			t.Error("should add [shell.hooks]")
		}
	})
}

func TestSetupZoxideUpdateHashrc(t *testing.T) {
	t.Run("creates new hashrc", func(t *testing.T) {
		dir := t.TempDir()
		hashrcPath := filepath.Join(dir, ".hashrc")

		changed, err := setupZoxideUpdateHashrc(hashrcPath)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !changed {
			t.Error("should report changed")
		}

		content, err := os.ReadFile(hashrcPath)
		if err != nil {
			t.Fatalf("failed to read hashrc: %v", err)
		}
		if !strings.Contains(string(content), zoxideInitLine) {
			t.Error("hashrc should contain zoxide init line")
		}
		if !strings.Contains(string(content), "# zoxide") {
			t.Error("hashrc should contain comment")
		}
	})

	t.Run("appends to existing hashrc", func(t *testing.T) {
		dir := t.TempDir()
		hashrcPath := filepath.Join(dir, ".hashrc")

		existing := "alias ll='ls -la'\n"
		if err := os.WriteFile(hashrcPath, []byte(existing), 0o644); err != nil {
			t.Fatal(err)
		}

		changed, err := setupZoxideUpdateHashrc(hashrcPath)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !changed {
			t.Error("should report changed")
		}

		content, err := os.ReadFile(hashrcPath)
		if err != nil {
			t.Fatalf("failed to read hashrc: %v", err)
		}
		// Original content preserved
		if !strings.Contains(string(content), "alias ll") {
			t.Error("should preserve existing content")
		}
		// New content added
		if !strings.Contains(string(content), zoxideInitLine) {
			t.Error("should contain zoxide init line")
		}
	})

	t.Run("idempotent - already has zoxide", func(t *testing.T) {
		dir := t.TempDir()
		hashrcPath := filepath.Join(dir, ".hashrc")

		existing := "# zoxide\n" + zoxideInitLine + "\n"
		if err := os.WriteFile(hashrcPath, []byte(existing), 0o644); err != nil {
			t.Fatal(err)
		}

		changed, err := setupZoxideUpdateHashrc(hashrcPath)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if changed {
			t.Error("should not report changed when already configured")
		}
	})
}

func TestSetupZoxideUpdateConfig(t *testing.T) {
	t.Run("creates new config file", func(t *testing.T) {
		dir := t.TempDir()
		configDir := filepath.Join(dir, "hash")

		cfg := config.Default()
		changes, err := setupZoxideUpdateConfig(configDir, cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(changes) != 2 {
			t.Fatalf("expected 2 changes, got %d: %v", len(changes), changes)
		}

		content, err := os.ReadFile(filepath.Join(configDir, "config.toml"))
		if err != nil {
			t.Fatalf("failed to read config: %v", err)
		}
		if !strings.Contains(string(content), `disable_builtins`) {
			t.Error("config should contain disable_builtins")
		}
		if !strings.Contains(string(content), "chpwd") {
			t.Error("config should contain chpwd hook")
		}

		// Verify in-memory config was updated
		if !cdBuiltinDisabled(cfg) {
			t.Error("in-memory config should have cd disabled")
		}
		if !zoxideChpwdHookExists(cfg) {
			t.Error("in-memory config should have chpwd hook")
		}
	})

	t.Run("appends to existing config", func(t *testing.T) {
		dir := t.TempDir()
		configDir := filepath.Join(dir, "hash")
		if err := os.MkdirAll(configDir, 0o755); err != nil {
			t.Fatal(err)
		}

		existing := "[prompt]\nmode = \"starship\"\n"
		if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(existing), 0o644); err != nil {
			t.Fatal(err)
		}

		cfg := config.Default()
		changes, err := setupZoxideUpdateConfig(configDir, cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(changes) != 2 {
			t.Fatalf("expected 2 changes, got %d", len(changes))
		}

		content, err := os.ReadFile(filepath.Join(configDir, "config.toml"))
		if err != nil {
			t.Fatal(err)
		}
		// Original content preserved
		if !strings.Contains(string(content), `mode = "starship"`) {
			t.Error("should preserve existing config")
		}
	})

	t.Run("idempotent - already configured", func(t *testing.T) {
		dir := t.TempDir()
		configDir := filepath.Join(dir, "hash")
		if err := os.MkdirAll(configDir, 0o755); err != nil {
			t.Fatal(err)
		}

		cfg := config.Default()
		cfg.Shell.DisableBuiltins = []string{"cd"}
		cfg.Shell.Hooks.Chpwd = []string{zoxideChpwdHook}

		changes, err := setupZoxideUpdateConfig(configDir, cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(changes) != 0 {
			t.Errorf("expected no changes, got %d: %v", len(changes), changes)
		}
	})
}
