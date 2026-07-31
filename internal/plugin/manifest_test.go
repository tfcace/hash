package plugin

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverFindsManifestAndResolvesRelativeEntrypoint(t *testing.T) {
	root := t.TempDir()
	bundle := filepath.Join(root, "io.runhash.demo")
	if err := os.MkdirAll(filepath.Join(bundle, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `manifest_version = 1
id = "io.runhash.demo"
name = "Demo"
version = "0.1.0"
protocol_version = 1
entrypoint = "bin/demo"
hooks = ["editor.suggest"]
commands = ["demo"]

[capabilities]
context = ["editor"]
host_services = ["history.query"]
`
	if err := os.WriteFile(filepath.Join(bundle, ManifestFilename), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	plugins, err := Discover([]string{root})
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(plugins) != 1 {
		t.Fatalf("Discover() returned %d plugins, want 1", len(plugins))
	}
	got := plugins[0]
	if got.ID != "io.runhash.demo" || got.Entrypoint != "bin/demo" || got.Executable() != filepath.Join(bundle, "bin", "demo") {
		t.Fatalf("manifest = %+v", got)
	}
}

func TestDiscoverRejectsDuplicateIDs(t *testing.T) {
	first := t.TempDir()
	second := t.TempDir()
	for _, root := range []string{first, second} {
		bundle := filepath.Join(root, "demo")
		if err := os.MkdirAll(bundle, 0o755); err != nil {
			t.Fatal(err)
		}
		content := `manifest_version = 1
id = "io.runhash.demo"
name = "Demo"
version = "0.1.0"
protocol_version = 1
entrypoint = "demo"
`
		if err := os.WriteFile(filepath.Join(bundle, ManifestFilename), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := Discover([]string{first, second}); err == nil {
		t.Fatal("Discover() error = nil, want duplicate ID error")
	}
}

func TestManifestRejectsUnsafeEntrypoint(t *testing.T) {
	dir := t.TempDir()
	content := `manifest_version = 1
id = "io.runhash.demo"
name = "Demo"
version = "0.1.0"
protocol_version = 1
entrypoint = "../demo"
`
	if err := os.WriteFile(filepath.Join(dir, ManifestFilename), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadManifest(dir); err == nil {
		t.Fatal("LoadManifest() error = nil, want unsafe entrypoint error")
	}
}

func TestManifestRejectsUnsafePluginID(t *testing.T) {
	manifest := Manifest{
		ManifestVersion: 1,
		ID:              "../outside",
		Name:            "Unsafe",
		Version:         "0.1.0",
		ProtocolVersion: ProtocolVersion,
		Entrypoint:      "bin/plugin",
	}
	if err := manifest.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want invalid plugin ID")
	}
}
