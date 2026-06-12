package agent

import (
	"context"
	"encoding/json"
	"testing"
)

func newModelTestTransport() (*ACPTransport, *mockPipe) {
	stdin := newMockPipe()
	t := &ACPTransport{
		config:    ACPConfig{Command: "test"},
		stdin:     stdin,
		sessionID: "test-session",
		messages:  make(chan []byte, 10),
		done:      make(chan struct{}),
	}
	return t, stdin
}

func TestACPTransport_NewSessionParsesModelConfig(t *testing.T) {
	transport, _ := newModelTestTransport()
	transport.sessionID = "" // newSession assigns it
	transport.messages <- []byte(`{"jsonrpc":"2.0","id":1,"result":{"sessionId":"sess-1","configOptions":[` +
		`{"id":"mode","category":"mode","currentValue":"default","options":[{"value":"default","name":"Default"}]},` +
		`{"id":"model","category":"model","currentValue":"sonnet","options":[` +
		`{"value":"default","name":"Default (recommended)","description":"Opus"},` +
		`{"value":"sonnet","name":"Sonnet","description":"Everyday"},` +
		`{"value":"haiku","name":"Haiku"}]}]}}`)

	sessionID, err := transport.newSession(context.Background(), "/tmp/hash-test")
	if err != nil {
		t.Fatalf("newSession: %v", err)
	}
	if sessionID != "sess-1" {
		t.Fatalf("sessionID = %q, want sess-1", sessionID)
	}

	if transport.modelConfigID != "model" {
		t.Fatalf("modelConfigID = %q, want model", transport.modelConfigID)
	}
	if got := transport.CurrentModel(); got != "Sonnet" {
		t.Fatalf("CurrentModel() = %q, want Sonnet", got)
	}
	models := transport.AvailableModels()
	if len(models) != 3 {
		t.Fatalf("AvailableModels() len = %d, want 3", len(models))
	}
	if models[2].Value != "haiku" || models[2].Name != "Haiku" {
		t.Fatalf("models[2] = %+v, want haiku/Haiku", models[2])
	}
}

func TestACPTransport_NoModelOptionMeansUnsupported(t *testing.T) {
	transport, _ := newModelTestTransport()
	transport.storeModelConfig([]configOption{
		{ID: "mode", Category: "mode", CurrentValue: "default",
			Options: []configOptionValue{{Value: "default", Name: "Default"}}},
	})

	if got := transport.CurrentModel(); got != "" {
		t.Fatalf("CurrentModel() = %q, want empty", got)
	}
	if got := transport.AvailableModels(); got != nil {
		t.Fatalf("AvailableModels() = %v, want nil", got)
	}
}

func TestACPTransport_SetModelSendsConfigOption(t *testing.T) {
	transport, stdin := newModelTestTransport()
	transport.modelConfigID = "model"
	transport.currentModelVal = "default"
	transport.availableModels = []ModelOption{{Value: "default", Name: "Default"}, {Value: "sonnet", Name: "Sonnet"}}
	transport.messages <- []byte(`{"jsonrpc":"2.0","id":1,"result":{"configOptions":[` +
		`{"id":"model","category":"model","currentValue":"sonnet","options":[` +
		`{"value":"default","name":"Default"},{"value":"sonnet","name":"Sonnet"}]}]}}`)

	if err := transport.setModel(context.Background(), "sonnet"); err != nil {
		t.Fatalf("setModel: %v", err)
	}

	stdin.mu.Lock()
	written := append([]byte(nil), stdin.written...)
	stdin.mu.Unlock()

	var req struct {
		Method string `json:"method"`
		Params struct {
			SessionID string `json:"sessionId"`
			ConfigID  string `json:"configId"`
			Value     string `json:"value"`
		} `json:"params"`
	}
	if err := json.Unmarshal(written, &req); err != nil {
		t.Fatalf("unmarshal set request: %v\n%s", err, string(written))
	}
	if req.Method != "session/set_config_option" {
		t.Fatalf("method = %q, want session/set_config_option", req.Method)
	}
	if req.Params.SessionID != "test-session" || req.Params.ConfigID != "model" || req.Params.Value != "sonnet" {
		t.Fatalf("params = %+v, want session/model/sonnet", req.Params)
	}
	if got := transport.CurrentModel(); got != "Sonnet" {
		t.Fatalf("CurrentModel() after set = %q, want Sonnet", got)
	}
}

func TestACPTransport_SetModelErrorsWhenNoModelOption(t *testing.T) {
	transport, _ := newModelTestTransport()
	if err := transport.setModel(context.Background(), "sonnet"); err == nil {
		t.Fatal("setModel: expected error when no model option, got nil")
	}
}

func TestACPTransport_ConfigOptionUpdateRefreshesModel(t *testing.T) {
	transport, _ := newModelTestTransport()
	transport.storeModelConfig([]configOption{
		{ID: "model", Category: "model", CurrentValue: "sonnet",
			Options: []configOptionValue{{Value: "sonnet", Name: "Sonnet"}, {Value: "haiku", Name: "Haiku"}}},
	})

	// Simulate the agent switching to haiku on its own.
	transport.storeModelConfig([]configOption{
		{ID: "model", Category: "model", CurrentValue: "haiku",
			Options: []configOptionValue{{Value: "sonnet", Name: "Sonnet"}, {Value: "haiku", Name: "Haiku"}}},
	})
	if got := transport.CurrentModel(); got != "Haiku" {
		t.Fatalf("CurrentModel() = %q, want Haiku", got)
	}
}

func TestACPTransport_StoreModelConfigIgnoresEmpty(t *testing.T) {
	transport, _ := newModelTestTransport()
	transport.storeModelConfig([]configOption{
		{ID: "model", Category: "model", CurrentValue: "sonnet",
			Options: []configOptionValue{{Value: "sonnet", Name: "Sonnet"}}},
	})
	transport.storeModelConfig(nil) // must not wipe known state
	if got := transport.CurrentModel(); got != "Sonnet" {
		t.Fatalf("CurrentModel() = %q, want Sonnet after empty update", got)
	}
}

func TestACPTransport_ReapplyPreferredModelDropsStalePin(t *testing.T) {
	transport, _ := newModelTestTransport()
	transport.modelConfigID = "model"
	transport.currentModelVal = "default"
	transport.availableModels = []ModelOption{{Value: "default", Name: "Default"}}
	transport.preferredModel = "sonnet" // no longer offered

	transport.reapplyPreferredModel(context.Background())

	if transport.preferredModel != "" {
		t.Fatalf("preferredModel = %q, want cleared", transport.preferredModel)
	}
}

func TestACPTransport_ReapplyPreferredModelNoopWhenMatching(t *testing.T) {
	transport, stdin := newModelTestTransport()
	transport.modelConfigID = "model"
	transport.currentModelVal = "sonnet"
	transport.availableModels = []ModelOption{{Value: "sonnet", Name: "Sonnet"}}
	transport.preferredModel = "sonnet" // already active

	transport.reapplyPreferredModel(context.Background())

	stdin.mu.Lock()
	written := len(stdin.written)
	stdin.mu.Unlock()
	if written != 0 {
		t.Fatalf("reapply sent %d bytes, want 0 (no-op)", written)
	}
}
