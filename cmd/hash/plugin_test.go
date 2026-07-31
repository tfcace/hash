package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

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
