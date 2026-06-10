package agent

import (
	"context"
	"testing"
)

func TestHTTPTransport_ModelMethods(t *testing.T) {
	transport := NewHTTPTransport(HTTPConfig{
		URL:   "http://localhost:11434/api/generate",
		Model: "codellama",
	})

	if got := transport.CurrentModel(); got != "codellama" {
		t.Fatalf("CurrentModel() = %q, want codellama", got)
	}
	if got := transport.AvailableModels(); got != nil {
		t.Fatalf("AvailableModels() = %v, want nil", got)
	}
	if err := transport.EnsureModelInfo(context.Background()); err != nil {
		t.Fatalf("EnsureModelInfo() = %v, want nil", err)
	}
	if err := transport.SetModel(context.Background(), "llama3"); err == nil {
		t.Fatal("SetModel() = nil, want unsupported error")
	}
}
