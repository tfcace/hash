package plugin

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProcessClient_InitializesAndCallsHook(t *testing.T) {
	if os.Getenv("HASH_PLUGIN_TEST_HELPER") != "" {
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

func TestProcessClient_HandlesNestedHostRequest(t *testing.T) {
	if os.Getenv("HASH_PLUGIN_TEST_HELPER") != "" {
		return
	}
	t.Setenv("HASH_PLUGIN_TEST_HELPER", "nested")
	bundle := t.TempDir()
	helper := filepath.Join(bundle, "helper")
	if err := os.Symlink(os.Args[0], helper); err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{ManifestVersion: 1, ID: "io.runhash.nested", Name: "Nested", Version: "0.1.0", ProtocolVersion: 1, Entrypoint: "helper", Directory: bundle, Capabilities: Capabilities{HostServices: []string{"history.query"}}}
	handler := func(ctx context.Context, _ Manifest, method string, params json.RawMessage) (any, *RPCError) {
		if method != "host.history.query" {
			return nil, &RPCError{Code: -32601, Message: "unsupported"}
		}
		return HistoryQueryResult{Entries: []HistoryEntry{{Line: "git status", ExitCode: 0}}}, nil
	}
	client, err := StartProcessWithHandler(context.Background(), manifest, nil, handler)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	var result CommandFinishedResult
	if err := client.Call(context.Background(), "command.finished", CommandFinishedParams{ExecutedLine: "git sttaus", ExitCode: 1}, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Corrections) != 1 || result.Corrections[0] != "git status" {
		t.Fatalf("result = %#v", result)
	}
}

func TestProcessClient_CloseReportsShutdownTimeout(t *testing.T) {
	if os.Getenv("HASH_PLUGIN_TEST_HELPER") != "" {
		return
	}
	t.Setenv("HASH_PLUGIN_TEST_HELPER", "no-shutdown")
	bundle := t.TempDir()
	helper := filepath.Join(bundle, "helper")
	if err := os.Symlink(os.Args[0], helper); err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{ManifestVersion: 1, ID: "io.runhash.no-shutdown", Name: "No shutdown", Version: "0.1.0", ProtocolVersion: 1, Entrypoint: "helper", Directory: bundle}
	client, err := StartProcess(context.Background(), manifest, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Close(); err == nil || !strings.Contains(err.Error(), "shutdown") {
		t.Fatalf("Close() error = %v, want shutdown failure", err)
	}
}

func TestProcessClient_RejectsMalformedProtocolOutput(t *testing.T) {
	if os.Getenv("HASH_PLUGIN_TEST_HELPER") != "" {
		return
	}
	t.Setenv("HASH_PLUGIN_TEST_HELPER", "malformed")
	bundle := t.TempDir()
	helper := filepath.Join(bundle, "helper")
	if err := os.Symlink(os.Args[0], helper); err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{ManifestVersion: 1, ID: "io.runhash.malformed", Name: "Malformed", Version: "0.1.0", ProtocolVersion: 1, Entrypoint: "helper", Directory: bundle}
	if _, err := StartProcess(context.Background(), manifest, nil); err == nil || !strings.Contains(err.Error(), "invalid JSON-RPC") {
		t.Fatalf("StartProcess() error = %v, want invalid JSON-RPC", err)
	}
}

func TestProcessClientHelper(t *testing.T) {
	mode := os.Getenv("HASH_PLUGIN_TEST_HELPER")
	if mode != "1" && mode != "nested" && mode != "crash-once" && mode != "priority" && mode != "no-shutdown" && mode != "malformed" {
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
			if mode == "malformed" {
				_, _ = fmt.Fprintln(os.Stdout, "not-json")
				return
			}
			if mode == "priority" {
				var params struct {
					Plugin struct {
						ID string `json:"id"`
					} `json:"plugin"`
				}
				_ = json.Unmarshal(request.Params, &params)
				_ = os.Setenv("HASH_PLUGIN_PRIORITY_ID", params.Plugin.ID)
			}
			_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{"protocol_version": ProtocolVersion}})
		case "editor.suggest":
			_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{"text": "git status"}})
		case "command.finished":
			if mode == "priority" {
				if strings.Contains(os.Getenv("HASH_PLUGIN_PRIORITY_ID"), "slow") {
					time.Sleep(time.Second)
				}
				_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{"corrections": []string{"git status"}}})
				continue
			}
			if mode == "crash-once" {
				state := os.Getenv("HASH_PLUGIN_CRASH_STATE")
				if _, err := os.Stat(state); os.IsNotExist(err) {
					_ = os.WriteFile(state, []byte("crashed"), 0o600)
					os.Exit(7)
				}
				_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{"corrections": []string{"git status"}}})
				continue
			}
			if mode != "nested" {
				os.Exit(4)
			}
			_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": 99, "method": "host.history.query", "params": map[string]any{"parent_request_id": request.ID, "limit": 10}})
			if !scanner.Scan() {
				os.Exit(5)
			}
			var hostResponse struct {
				ID     int64 `json:"id"`
				Result struct {
					Entries []HistoryEntry `json:"entries"`
				} `json:"result"`
			}
			_ = json.Unmarshal(scanner.Bytes(), &hostResponse)
			if hostResponse.ID != 99 || len(hostResponse.Result.Entries) == 0 {
				os.Exit(6)
			}
			_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{"corrections": []string{hostResponse.Result.Entries[0].Line}}})
		case "shutdown":
			if mode == "no-shutdown" {
				time.Sleep(time.Second)
				continue
			}
			_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{}})
			return
		default:
			_, _ = fmt.Fprintln(os.Stderr, "unexpected method", request.Method)
			os.Exit(3)
		}
	}
}
