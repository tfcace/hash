package shell

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tfcace/hash/internal/config"
)

func TestStartPluginsKeepsHealthyPluginAfterPartialStartupFailure(t *testing.T) {
	if os.Getenv("HASH_SHELL_PLUGIN_HELPER") != "" {
		return
	}
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	t.Setenv("XDG_DATA_DIRS", t.TempDir())
	t.Setenv("HASH_SHELL_PLUGIN_HELPER", "partial-start")

	writeBundle := func(id string) {
		dir := filepath.Join(dataHome, "hash", "plugins", id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		helper := filepath.Join(dir, "helper")
		script := fmt.Sprintf("#!/bin/sh\nexec %q -test.run=TestShellPluginHelper\n", os.Args[0])
		if err := os.WriteFile(helper, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		manifest := fmt.Sprintf("manifest_version = 1\nid = %q\nname = %q\nversion = \"0.1.0\"\nprotocol_version = 1\nentrypoint = \"helper\"\nhooks = [\"editor.suggest\"]\ncommands = []\n", id, id)
		if err := os.WriteFile(filepath.Join(dir, "hash-plugin.toml"), []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeBundle("io.runhash.healthy")
	writeBundle("io.runhash.broken")

	cfg := config.Default()
	cfg.Plugins.Enabled = []string{"io.runhash.healthy", "io.runhash.broken"}
	sh := &Shell{mode: Mode{Interactive: true}, config: cfg}
	sh.startPlugins(context.Background())
	t.Cleanup(sh.stopPlugins)

	if sh.plugins == nil {
		t.Fatal("startPlugins() discarded the manager after a partial startup failure")
	}
	if sh.editorCfg.SuggestionFunc == nil {
		t.Fatal("startPlugins() did not connect the healthy plugin to the editor")
	}
}

func TestShellPluginHelper(t *testing.T) {
	if os.Getenv("HASH_SHELL_PLUGIN_HELPER") != "partial-start" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var request struct {
			ID     int64           `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			os.Exit(2)
		}
		switch request.Method {
		case "initialize":
			var params struct {
				Plugin struct {
					ID string `json:"id"`
				} `json:"plugin"`
			}
			_ = json.Unmarshal(request.Params, &params)
			if strings.Contains(params.Plugin.ID, "broken") {
				_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "error": map[string]any{"code": -32001, "message": "storage unavailable"}})
				continue
			}
			_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{"protocol_version": 1}})
		case "shutdown":
			_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{}})
			return
		}
	}
}
