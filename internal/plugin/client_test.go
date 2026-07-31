package plugin

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestProcessClient_InitializesAndCallsHook(t *testing.T) {
	if os.Getenv("HASH_PLUGIN_TEST_HELPER") == "1" {
		return
	}
	t.Setenv("HASH_PLUGIN_TEST_HELPER", "1")
	bundle := t.TempDir()
	helper := filepath.Join(bundle, "helper")
	if err := os.Symlink(os.Args[0], helper); err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{
		ManifestVersion: 1,
		ID:              "io.runhash.test",
		Name:            "Test",
		Version:         "0.1.0",
		ProtocolVersion: ProtocolVersion,
		Entrypoint:      "helper",
		Directory:       bundle,
	}

	client, err := StartProcess(context.Background(), manifest, map[string]any{"enabled": true})
	if err != nil {
		t.Fatalf("StartProcess() error = %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	var result struct {
		Text string `json:"text"`
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.Call(ctx, "editor.suggest", map[string]string{"line": "git"}, &result); err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	if result.Text != "git status" {
		t.Fatalf("result.Text = %q, want git status", result.Text)
	}
}

func TestProcessClientHelper(t *testing.T) {
	if os.Getenv("HASH_PLUGIN_TEST_HELPER") != "1" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var request struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			os.Exit(2)
		}
		switch request.Method {
		case "initialize":
			_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{"protocol_version": ProtocolVersion}})
		case "editor.suggest":
			_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{"text": "git status"}})
		case "shutdown":
			_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{}})
			return
		default:
			_, _ = fmt.Fprintln(os.Stderr, "unexpected method", request.Method)
			os.Exit(3)
		}
	}
}
