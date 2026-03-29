package shell

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/tfcace/hash/internal/config"
	"github.com/tfcace/hash/internal/executor"
)

const (
	zoxideInitLine  = `eval "$(zoxide init bash)"`
	zoxideAliasLine = `alias cd="z"`
	zoxideChpwdHook = `zoxide add -- "$PWD"`
)

type zoxideHashrcChanges struct {
	AddedInit  bool
	AddedAlias bool
}

// builtinSetupZoxide sets up zoxide integration with Hash.
// It disables the built-in cd, adds zoxide init to ~/.hashrc,
// and configures the chpwd hook for directory tracking.
func (s *Shell) builtinSetupZoxide(ctx context.Context, args []string) error {
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Println(`Usage: setup-zoxide

Set up zoxide integration with Hash.

This command:
  - Adds zoxide init to ~/.hashrc
  - Adds 'alias cd="z"' to ~/.hashrc
  - Disables the built-in cd command
  - Configures chpwd hook for directory tracking

Requires zoxide to be installed.
See: https://github.com/ajeetdsouza/zoxide`)
		return nil
	}

	// Check if zoxide is installed
	if _, err := exec.LookPath("zoxide"); err != nil {
		fmt.Println("zoxide is not installed.")
		fmt.Println()
		fmt.Println("Install it first:")
		fmt.Println("  https://github.com/ajeetdsouza/zoxide#installation")
		return nil //nolint:nilerr // intentional: not-installed is a user message, not an error
	}

	var changes []string

	cfgCopy := cloneConfigForZoxide(s.config)

	// 1. Update ~/.hashrc first so failures don't leave cd disabled without a replacement.
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}
	hashrcPath := filepath.Join(home, ".hashrc")
	hashrcChanges, err := setupZoxideUpdateHashrc(hashrcPath)
	if err != nil {
		return fmt.Errorf("hashrc update failed: %w", err)
	}
	if hashrcChanges.AddedInit {
		changes = append(changes, "Added zoxide init to ~/.hashrc")
	}
	if hashrcChanges.AddedAlias {
		changes = append(changes, `Added 'alias cd="z"' to ~/.hashrc`)
	}

	// 2. Update config.toml (disable cd builtin, add chpwd hook)
	configDir := getConfigDir()
	configChanges, err := setupZoxideUpdateConfig(configDir, cfgCopy)
	if err != nil {
		return fmt.Errorf("config update failed: %w", err)
	}
	changes = append(changes, configChanges...)

	s.config = cfgCopy

	// 3. Report results
	if len(changes) == 0 {
		fmt.Println("zoxide is already configured.")
	} else {
		fmt.Println("zoxide setup complete:")
		for _, c := range changes {
			fmt.Printf("  + %s\n", c)
		}

		// Try to activate in the current session
		if s.executor != nil {
			evalErr := activateZoxideInSession(ctx, s.executor, hashrcChanges)
			if evalErr == nil {
				fmt.Println()
				fmt.Println("zoxide is active in this session.")
			} else {
				fmt.Println()
				fmt.Println("Restart hash to apply changes.")
			}
		}
	}

	return nil
}

// setupZoxideUpdateConfig updates config.toml with zoxide settings.
// It modifies the config in-place and writes the file.
func setupZoxideUpdateConfig(configDir string, cfg *config.Config) ([]string, error) { //nolint:gocyclo // linear config assembly
	configPath := filepath.Join(configDir, "config.toml")

	needsCd := !cdBuiltinDisabled(cfg)
	needsHook := !zoxideChpwdHookExists(cfg)

	if !needsCd && !needsHook {
		return nil, nil
	}

	if err := os.MkdirAll(configDir, 0o750); err != nil {
		return nil, err
	}

	// Read existing file
	raw, err := os.ReadFile(configPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	existing := string(raw)

	// Check if we need to modify existing TOML values (requires full rewrite)
	// vs. just appending new keys/sections
	needsFullRewrite := (needsCd && strings.Contains(existing, "disable_builtins")) ||
		(needsHook && strings.Contains(existing, "chpwd"))

	// Update in-memory config
	if needsCd {
		cfg.Shell.DisableBuiltins = append(cfg.Shell.DisableBuiltins, "cd")
	}
	if needsHook {
		cfg.Shell.Hooks.Chpwd = append(cfg.Shell.Hooks.Chpwd, zoxideChpwdHook)
	}

	var content string
	switch {
	case needsFullRewrite:
		// Existing TOML arrays need modification - full rewrite is the safe approach
		data, marshalErr := toml.Marshal(cfg)
		if marshalErr != nil {
			return nil, marshalErr
		}
		content = string(data)
	case strings.TrimSpace(existing) == "":
		// No config file - create minimal one
		content = buildMinimalZoxideConfig(needsCd, needsHook)
	default:
		// Existing file - append new sections/keys
		content = appendZoxideToConfig(existing, needsCd, needsHook)
	}

	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil { //nolint:gosec // G306: user config file
		return nil, err
	}

	var changes []string
	if needsCd {
		changes = append(changes, "Disabled built-in cd in config.toml")
	}
	if needsHook {
		changes = append(changes, "Added chpwd hook in config.toml")
	}
	return changes, nil
}

// cdBuiltinDisabled returns true if the cd builtin is already disabled.
func cdBuiltinDisabled(cfg *config.Config) bool {
	for _, b := range cfg.Shell.DisableBuiltins {
		if b == "cd" {
			return true
		}
	}
	return false
}

// zoxideChpwdHookExists returns true if a zoxide chpwd hook is already configured.
func zoxideChpwdHookExists(cfg *config.Config) bool {
	for _, h := range cfg.Shell.Hooks.Chpwd {
		if strings.Contains(h, "zoxide") {
			return true
		}
	}
	return false
}

func cloneConfigForZoxide(cfg *config.Config) *config.Config {
	if cfg == nil {
		return config.Default()
	}

	clone := *cfg
	clone.Shell = cfg.Shell
	clone.Shell.DisableBuiltins = append([]string(nil), cfg.Shell.DisableBuiltins...)
	clone.Shell.Hooks = cfg.Shell.Hooks
	clone.Shell.Hooks.Chpwd = append([]string(nil), cfg.Shell.Hooks.Chpwd...)

	return &clone
}

// buildMinimalZoxideConfig creates a minimal config.toml with only zoxide settings.
func buildMinimalZoxideConfig(disableCd, chpwdHook bool) string {
	var buf strings.Builder
	if disableCd {
		buf.WriteString("[shell]\n")
		buf.WriteString("disable_builtins = [\"cd\"]\n")
	}
	if chpwdHook {
		if disableCd {
			buf.WriteString("\n")
		}
		buf.WriteString("[shell.hooks]\n")
		buf.WriteString("chpwd = ['zoxide add -- \"$PWD\"']\n")
	}
	return buf.String()
}

// appendZoxideToConfig appends zoxide configuration to an existing config file.
// It inserts keys after the appropriate section headers, or appends new sections.
func appendZoxideToConfig(existing string, disableCd, chpwdHook bool) string {
	lines := strings.Split(existing, "\n")
	var result []string
	cdInserted := false
	hookInserted := false

	for _, line := range lines {
		result = append(result, line)
		trimmed := strings.TrimSpace(line)

		// Insert disable_builtins right after [shell] header
		if disableCd && !cdInserted && trimmed == "[shell]" {
			result = append(result, `disable_builtins = ["cd"]`)
			cdInserted = true
		}

		// Insert chpwd right after [shell.hooks] header
		if chpwdHook && !hookInserted && trimmed == "[shell.hooks]" {
			result = append(result, `chpwd = ['zoxide add -- "$PWD"']`)
			hookInserted = true
		}
	}

	// If [shell] section didn't exist, append it
	if disableCd && !cdInserted {
		result = append(result, "", "[shell]", `disable_builtins = ["cd"]`)
	}

	// If [shell.hooks] section didn't exist, append it
	if chpwdHook && !hookInserted {
		result = append(result, "", "[shell.hooks]", `chpwd = ['zoxide add -- "$PWD"']`)
	}

	text := strings.Join(result, "\n")
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return text
}

func activateZoxideInSession(ctx context.Context, runner interface {
	Execute(context.Context, string, io.Writer, io.Writer) (*executor.Result, error)
}, changes zoxideHashrcChanges) error {
	if changes.AddedInit {
		if _, err := runner.Execute(ctx, zoxideInitLine, nil, os.Stderr); err != nil {
			return err
		}
	}
	if changes.AddedAlias {
		if _, err := runner.Execute(ctx, zoxideAliasLine, nil, os.Stderr); err != nil {
			return err
		}
	}
	return nil
}

func hasHashrcCdAlias(content string) bool {
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "alias cd=") || strings.HasPrefix(trimmed, "alias cd =") {
			return true
		}
	}
	return false
}

// setupZoxideUpdateHashrc adds zoxide init and alias to the hashrc file.
func setupZoxideUpdateHashrc(hashrcPath string) (zoxideHashrcChanges, error) {
	raw, err := os.ReadFile(hashrcPath)
	if err != nil && !os.IsNotExist(err) {
		return zoxideHashrcChanges{}, err
	}

	content := string(raw)
	changes := zoxideHashrcChanges{
		AddedInit:  !strings.Contains(content, "zoxide init"),
		AddedAlias: !hasHashrcCdAlias(content),
	}
	if !changes.AddedInit && !changes.AddedAlias {
		return changes, nil
	}

	var buf strings.Builder
	if len(raw) > 0 {
		buf.Write(raw)
		if !strings.HasSuffix(content, "\n") {
			buf.WriteString("\n")
		}
	}
	if changes.AddedInit {
		if len(raw) > 0 {
			buf.WriteString("\n")
		}
		buf.WriteString("# zoxide - smarter cd command\n")
		buf.WriteString(zoxideInitLine + "\n")
	}
	if changes.AddedAlias {
		buf.WriteString(zoxideAliasLine + "\n")
	}

	if err := os.WriteFile(hashrcPath, []byte(buf.String()), 0o644); err != nil { //nolint:gosec // G306: user shell config
		return zoxideHashrcChanges{}, err
	}
	return changes, nil
}
