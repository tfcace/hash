package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/tfcace/hash/internal/config"
	"github.com/tfcace/hash/internal/plugin"
)

type fakePluginLifecycle struct {
	installedSource string
	installedAll    bool
	upgradedID      string
	upgradedSource  string
	upgradedAll     bool
	uninstalledID   string
	uninstalledIDs  []string
}

func (f *fakePluginLifecycle) Install(_ context.Context, source string) (plugin.InstallResult, error) {
	f.installedSource = source
	return plugin.InstallResult{ID: "io.runhash.demo", Version: "0.1.0", Source: "github:owner/repo", Changed: true}, nil
}

func (f *fakePluginLifecycle) Upgrade(_ context.Context, id, source string) (plugin.InstallResult, error) {
	f.upgradedID, f.upgradedSource = id, source
	return plugin.InstallResult{ID: id, Version: "0.1.1", PreviousVersion: "0.1.0", Changed: true}, nil
}

func (f *fakePluginLifecycle) Uninstall(id string) error {
	f.uninstalledID = id
	f.uninstalledIDs = append(f.uninstalledIDs, id)
	return nil
}

func (f *fakePluginLifecycle) InstallAll(_ context.Context, source string) ([]plugin.InstallResult, error) {
	f.installedAll = true
	f.installedSource = source
	return []plugin.InstallResult{
		{ID: "io.runhash.alpha", Version: "0.1.0", Source: "github:owner/repo", Changed: true},
		{ID: "io.runhash.beta", Version: "0.1.0", Source: "github:owner/repo", Changed: true},
	}, nil
}

func (f *fakePluginLifecycle) UpgradeAll(_ context.Context, source string) (plugin.UpgradeAllResult, error) {
	f.upgradedAll = true
	f.upgradedSource = source
	return plugin.UpgradeAllResult{
		Results: []plugin.InstallResult{{ID: "io.runhash.alpha", PreviousVersion: "0.1.0", Version: "0.1.1", Changed: true}},
		Skipped: []string{"io.runhash.developer"},
	}, nil
}

func TestRunPluginLifecycleInstallUpgradeAndUninstall(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("HASH_CONFIG_DIR", configDir)
	if err := config.SetPluginEnabled(configDir, "io.runhash.demo", true); err != nil {
		t.Fatal(err)
	}
	lifecycle := &fakePluginLifecycle{}

	var stdout, stderr bytes.Buffer
	if code := runPluginLifecycle([]string{"install", "github:owner/repo@v0.1.0"}, lifecycle, &stdout, &stderr); code != 0 {
		t.Fatalf("install exit=%d stderr=%s", code, stderr.String())
	}
	if lifecycle.installedSource != "github:owner/repo@v0.1.0" || !bytes.Contains(stdout.Bytes(), []byte("Installed io.runhash.demo 0.1.0")) {
		t.Fatalf("install source/output = %q / %s", lifecycle.installedSource, stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := runPluginLifecycle([]string{"upgrade", "io.runhash.demo"}, lifecycle, &stdout, &stderr); code != 0 {
		t.Fatalf("upgrade exit=%d stderr=%s", code, stderr.String())
	}
	if lifecycle.upgradedID != "io.runhash.demo" || !bytes.Contains(stdout.Bytes(), []byte("0.1.0 -> 0.1.1")) {
		t.Fatalf("upgrade state/output = %+v / %s", lifecycle, stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := runPluginLifecycle([]string{"uninstall", "io.runhash.demo"}, lifecycle, &stdout, &stderr); code != 0 {
		t.Fatalf("uninstall exit=%d stderr=%s", code, stderr.String())
	}
	if lifecycle.uninstalledID != "io.runhash.demo" {
		t.Fatalf("uninstalled ID = %q", lifecycle.uninstalledID)
	}
	cfg, err := config.Load(configDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Plugins.Enabled) != 0 {
		t.Fatalf("uninstall left plugin enabled: %v", cfg.Plugins.Enabled)
	}
}

func TestParseInstallArgsSupportsIDBeforeOrAfterSource(t *testing.T) {
	for _, tc := range []struct {
		args       []string
		source, id string
		installAll bool
	}{
		{[]string{"install", "github:owner/repo", "--id", "io.runhash.adaptive-prediction"}, "github:owner/repo", "io.runhash.adaptive-prediction", false},
		{[]string{"install", "--id=io.runhash.adaptive-prediction", "github:owner/repo"}, "github:owner/repo", "io.runhash.adaptive-prediction", false},
		{[]string{"install", "github:owner/repo"}, "github:owner/repo", "", false},
		{[]string{"install", "--all", "github:owner/repo"}, "github:owner/repo", "", true},
		{[]string{"install", "github:owner/repo", "--all"}, "github:owner/repo", "", true},
	} {
		source, id, installAll, ok := parseInstallArgs(tc.args)
		if !ok || source != tc.source || id != tc.id || installAll != tc.installAll {
			t.Fatalf("parseInstallArgs(%v) = %q, %q, %v, %v", tc.args, source, id, installAll, ok)
		}
	}
	for _, args := range [][]string{{"install", "--id"}, {"install", "--nope", "x"}, {"install", "x", "--id", "bad"}, {"install", "--all", "--id", "io.runhash.demo", "x"}, {"install", "--all", "--all", "x"}} {
		if _, _, _, ok := parseInstallArgs(args); ok {
			t.Fatalf("parseInstallArgs(%v) accepted invalid input", args)
		}
	}
}

func TestRunPluginLifecycleInstallAllLeavesEveryPluginDisabled(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("HASH_CONFIG_DIR", configDir)
	if err := config.SetPluginEnabled(configDir, "io.runhash.alpha", true); err != nil {
		t.Fatal(err)
	}
	lifecycle := &fakePluginLifecycle{}
	var stdout, stderr bytes.Buffer
	if code := runPluginLifecycle([]string{"install", "--all", "github:owner/repo"}, lifecycle, &stdout, &stderr); code != 0 {
		t.Fatalf("install --all exit=%d stderr=%s", code, stderr.String())
	}
	if !lifecycle.installedAll || lifecycle.installedSource != "github:owner/repo" {
		t.Fatalf("InstallAll() call = all:%v source:%q", lifecycle.installedAll, lifecycle.installedSource)
	}
	cfg, err := config.Load(configDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Plugins.Enabled) != 0 {
		t.Fatalf("install --all left plugins enabled: %v", cfg.Plugins.Enabled)
	}
	if !bytes.Contains(stdout.Bytes(), []byte("Installed io.runhash.alpha 0.1.0")) || !bytes.Contains(stdout.Bytes(), []byte("Installed io.runhash.beta 0.1.0")) {
		t.Fatalf("install --all output = %s", stdout.String())
	}
}

func TestRunPluginLifecycleUpgradeAllReportsDeveloperLinksWithoutChangingThem(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("HASH_CONFIG_DIR", configDir)
	if err := config.SetPluginEnabled(configDir, "io.runhash.alpha", true); err != nil {
		t.Fatal(err)
	}
	lifecycle := &fakePluginLifecycle{}
	var stdout, stderr bytes.Buffer
	if code := runPluginLifecycle([]string{"upgrade", "--all", "github:owner/repo"}, lifecycle, &stdout, &stderr); code != 0 {
		t.Fatalf("upgrade --all exit=%d stderr=%s", code, stderr.String())
	}
	if !lifecycle.upgradedAll || lifecycle.upgradedSource != "github:owner/repo" {
		t.Fatalf("UpgradeAll() call = all:%v source:%q", lifecycle.upgradedAll, lifecycle.upgradedSource)
	}
	if !bytes.Contains(stdout.Bytes(), []byte("Skipped io.runhash.developer: not managed by Hash (left unchanged)")) {
		t.Fatalf("upgrade --all output = %s", stdout.String())
	}
	cfg, err := config.Load(configDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Plugins.Enabled) != 1 || cfg.Plugins.Enabled[0] != "io.runhash.alpha" {
		t.Fatalf("upgrade --all changed enabled state: %v", cfg.Plugins.Enabled)
	}
}

func TestParseUpgradeArgsSupportsAllWithOptionalSource(t *testing.T) {
	for _, tc := range []struct {
		args                []string
		id, source          string
		upgradeAll, isValid bool
	}{
		{[]string{"upgrade", "io.runhash.demo"}, "io.runhash.demo", "", false, true},
		{[]string{"upgrade", "io.runhash.demo", "github:owner/repo"}, "io.runhash.demo", "github:owner/repo", false, true},
		{[]string{"upgrade", "--all"}, "", "", true, true},
		{[]string{"upgrade", "--all", "github:owner/repo"}, "", "github:owner/repo", true, true},
		{[]string{"upgrade", "--all", "--id"}, "", "", false, false},
	} {
		id, source, upgradeAll, ok := parseUpgradeArgs(tc.args)
		if id != tc.id || source != tc.source || upgradeAll != tc.upgradeAll || ok != tc.isValid {
			t.Fatalf("parseUpgradeArgs(%v) = %q, %q, %v, %v", tc.args, id, source, upgradeAll, ok)
		}
	}
}

func TestRunPluginLifecycleRollsBackInstallWhenConfigurationCannotBeDisabled(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(configPath, []byte("blocked"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HASH_CONFIG_DIR", configPath)
	lifecycle := &fakePluginLifecycle{}

	var stdout, stderr bytes.Buffer
	if code := runPluginLifecycle([]string{"install", "github:owner/repo"}, lifecycle, &stdout, &stderr); code != 1 {
		t.Fatalf("install exit=%d, want 1; stderr=%s", code, stderr.String())
	}
	if lifecycle.uninstalledID != "io.runhash.demo" {
		t.Fatalf("failed configuration left installed plugin; rollback ID=%q", lifecycle.uninstalledID)
	}
}

func TestDisableInstalledPluginsRestoresPriorEnabledStateWhenLaterDisableFails(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("HASH_CONFIG_DIR", configDir)
	if err := config.SetPluginEnabled(configDir, "io.runhash.alpha", true); err != nil {
		t.Fatal(err)
	}
	originalSetPluginEnabled := setPluginEnabled
	t.Cleanup(func() { setPluginEnabled = originalSetPluginEnabled })
	setPluginEnabled = func(dir, id string, enabled bool) error {
		if id == "io.runhash.beta" && !enabled {
			return errors.New("simulated config failure")
		}
		return config.SetPluginEnabled(dir, id, enabled)
	}
	lifecycle := &fakePluginLifecycle{}
	err := disableInstalledPlugins(lifecycle, []plugin.InstallResult{
		{ID: "io.runhash.alpha"},
		{ID: "io.runhash.beta"},
	})
	if err == nil || !bytes.Contains([]byte(err.Error()), []byte("installation rolled back")) {
		t.Fatalf("disableInstalledPlugins() error = %v", err)
	}
	cfg, err := config.Load(configDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Plugins.Enabled) != 1 || cfg.Plugins.Enabled[0] != "io.runhash.alpha" {
		t.Fatalf("enabled state after rollback = %v", cfg.Plugins.Enabled)
	}
	if len(lifecycle.uninstalledIDs) != 2 || lifecycle.uninstalledIDs[0] != "io.runhash.beta" || lifecycle.uninstalledIDs[1] != "io.runhash.alpha" {
		t.Fatalf("rollback uninstall order = %v", lifecycle.uninstalledIDs)
	}
}

func TestRunPluginLinkAndEnable(t *testing.T) {
	data := t.TempDir()
	config := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	t.Setenv("XDG_DATA_DIRS", "")
	t.Setenv("HASH_CONFIG_DIR", config)
	bundle := filepath.Join(t.TempDir(), "demo")
	if err := os.MkdirAll(bundle, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `manifest_version = 1
id = "io.runhash.demo"
name = "Demo"
version = "0.1.0"
protocol_version = 1
entrypoint = "bin/demo"
`
	if err := os.WriteFile(filepath.Join(bundle, "hash-plugin.toml"), []byte(manifest), 0o644); err != nil { //nolint:gosec // test fixture
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := runPlugin([]string{"link", bundle}, &stdout, &stderr); code != 0 {
		t.Fatalf("link exit = %d, stderr = %s", code, stderr.String())
	}
	if code := runPlugin([]string{"enable", "io.runhash.demo"}, &stdout, &stderr); code != 0 {
		t.Fatalf("enable exit = %d, stderr = %s", code, stderr.String())
	}
	if _, err := os.Lstat(filepath.Join(data, "hash", "plugins", "io.runhash.demo")); err != nil {
		t.Fatalf("linked bundle missing: %v", err)
	}
	contents, err := os.ReadFile(filepath.Join(config, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(contents, []byte("io.runhash.demo")) {
		t.Fatalf("config does not enable plugin: %s", contents)
	}
}

func TestPluginDoctorPerformsHandshakeForSelectedID(t *testing.T) {
	data := t.TempDir()
	configDir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	t.Setenv("XDG_DATA_DIRS", t.TempDir())
	t.Setenv("HASH_CONFIG_DIR", configDir)
	bundle := filepath.Join(data, "hash", "plugins", "io.runhash.doctor")
	if err := os.MkdirAll(filepath.Join(bundle, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "manifest_version = 1\nid = \"io.runhash.doctor\"\nname = \"Doctor\"\nversion = \"0.1.0\"\nprotocol_version = 1\nentrypoint = \"bin/doctor\"\n"
	if err := os.WriteFile(filepath.Join(bundle, "hash-plugin.toml"), []byte(manifest), 0o644); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	script := "#!/bin/sh\nread init\nprintf '%s\\n' '{\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"protocol_version\":1}}'\nread shutdown\nprintf '%s\\n' '{\"jsonrpc\":\"2.0\",\"id\":2,\"result\":{}}'\n"
	if err := os.WriteFile(filepath.Join(bundle, "bin", "doctor"), []byte(script), 0o755); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := runPlugin([]string{"doctor", "io.runhash.doctor"}, &stdout, &stderr); code != 0 {
		t.Fatalf("doctor exit=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !bytes.Contains(stdout.Bytes(), []byte("handshake and shutdown passed")) {
		t.Fatalf("doctor did not validate protocol: %s", stdout.String())
	}
}
