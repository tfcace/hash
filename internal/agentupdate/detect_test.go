package agentupdate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeNpmTree builds <root>/lib/node_modules/@agentclientprotocol/claude-agent-acp
// with a package.json and dist/index.js, plus <root>/bin/claude-agent-acp as a
// symlink to the entry point, the way npm lays out a global install.
func fakeNpmTree(t *testing.T, version string) (pkgDir, bin string) {
	t.Helper()
	root := t.TempDir()
	pkgDir = filepath.Join(root, "lib", "node_modules", "@agentclientprotocol", "claude-agent-acp")
	if err := os.MkdirAll(filepath.Join(pkgDir, "dist"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	writePackageJSON(t, pkgDir, version)
	entry := filepath.Join(pkgDir, "dist", "index.js")
	if err := os.WriteFile(entry, []byte("#!/usr/bin/env node\n"), 0o755); err != nil { //nolint:gosec // test file
		t.Fatal(err)
	}
	bin = filepath.Join(root, "bin", "claude-agent-acp")
	if err := os.Symlink(entry, bin); err != nil {
		t.Fatal(err)
	}
	return pkgDir, bin
}

func writePackageJSON(t *testing.T, pkgDir, version string) {
	t.Helper()
	data := fmt.Sprintf(`{"name":%q,"version":%q}`, Package, version)
	if err := os.WriteFile(filepath.Join(pkgDir, "package.json"), []byte(data), 0o644); err != nil { //nolint:gosec // test file
		t.Fatal(err)
	}
}

func lookPathFor(bin string) func(string) (string, error) {
	return func(name string) (string, error) {
		if name == "claude-agent-acp" {
			return bin, nil
		}
		return "", errors.New("not found")
	}
}

func TestDetect_FindsNpmPackage(t *testing.T) {
	_, bin := fakeNpmTree(t, "0.42.0")

	inst, ok := Detect("claude-agent-acp", lookPathFor(bin))
	if !ok {
		t.Fatal("Detect() = false, want true")
	}
	if inst.Version != "0.42.0" {
		t.Errorf("Version = %q, want 0.42.0", inst.Version)
	}
	wantDir := filepath.Join("node_modules", "@agentclientprotocol", "claude-agent-acp")
	if !strings.HasSuffix(inst.Dir, wantDir) {
		t.Errorf("Dir = %q, want suffix %q", inst.Dir, wantDir)
	}
	if !strings.HasSuffix(inst.BinPath, filepath.Join("dist", "index.js")) {
		t.Errorf("BinPath = %q, want the symlink target", inst.BinPath)
	}
	if inst.InstalledAt.IsZero() {
		t.Error("InstalledAt should be the package.json mtime")
	}
}

func TestDetect_IgnoresOtherPrograms(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "gemini")
	if err := os.WriteFile(bin, []byte(""), 0o755); err != nil { //nolint:gosec // test file
		t.Fatal(err)
	}
	lookPath := func(string) (string, error) { return bin, nil }
	if _, ok := Detect("gemini --experimental-acp", lookPath); ok {
		t.Error("Detect() recognized a program outside the npm package")
	}
}

func TestDetect_MissingCommand(t *testing.T) {
	lookPath := func(string) (string, error) { return "", errors.New("not found") }
	if _, ok := Detect("claude-agent-acp", lookPath); ok {
		t.Error("Detect() = true for a command not on PATH")
	}
	if _, ok := Detect("", lookPath); ok {
		t.Error("Detect() = true for an empty command")
	}
}

func TestDetect_RejectsWrongPackageName(t *testing.T) {
	pkgDir, bin := fakeNpmTree(t, "0.42.0")
	data := []byte(`{"name":"@zed-industries/claude-code-acp","version":"0.12.6"}`)
	if err := os.WriteFile(filepath.Join(pkgDir, "package.json"), data, 0o644); err != nil { //nolint:gosec // test file
		t.Fatal(err)
	}
	if _, ok := Detect("claude-agent-acp", lookPathFor(bin)); ok {
		t.Error("Detect() accepted a package.json naming a different package")
	}
}
