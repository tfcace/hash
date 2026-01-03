package agent

import (
	"testing"
)

func TestACPTransport_New(t *testing.T) {
	cfg := ACPConfig{
		Command: "claude-code-acp",
		Args:    []string{},
	}

	transport := NewACPTransport(cfg)
	if transport == nil {
		t.Fatal("NewACPTransport() returned nil")
	}
	if transport.Name() != "acp" {
		t.Errorf("Name() = %q, want %q", transport.Name(), "acp")
	}
}

func TestACPTransport_ImplementsInterface(t *testing.T) {
	var _ Transport = (*ACPTransport)(nil)
}
