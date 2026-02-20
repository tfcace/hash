package completion

import (
	"context"
	"testing"
)

func TestSSHHandler_PrefixFilter(t *testing.T) {
	h := &SSHHandler{
		readFile: func(path string) ([]string, error) {
			return []string{
				"Host myserver",
				"Host devbox",
				"Host staging",
			}, nil
		},
	}

	result := h.Complete(context.Background(), nil, "my")
	if len(result.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(result.Items))
	}
	if result.Items[0].Value != "myserver" {
		t.Errorf("got %q, want %q", result.Items[0].Value, "myserver")
	}
}

func TestSSHHandler_NoMatch(t *testing.T) {
	h := &SSHHandler{
		readFile: func(path string) ([]string, error) {
			return []string{"Host devbox"}, nil
		},
	}

	result := h.Complete(context.Background(), nil, "prod")
	if len(result.Items) != 0 {
		t.Fatalf("expected 0 items, got %d", len(result.Items))
	}
}

func TestSSHHandler_SkipsWildcards(t *testing.T) {
	h := &SSHHandler{
		readFile: func(path string) ([]string, error) {
			return []string{
				"Host *",
				"Host *.example.com",
				"Host realhost",
			}, nil
		},
	}

	// Test parseSSHConfig directly to verify wildcard filtering
	hosts := h.parseSSHConfig("test")
	if len(hosts) != 1 {
		t.Fatalf("expected 1 host (skipping wildcards), got %d: %v", len(hosts), hosts)
	}
	if hosts[0] != "realhost" {
		t.Errorf("got %q, want %q", hosts[0], "realhost")
	}
}

func TestSSHHandler_SkipsFlagArgs(t *testing.T) {
	h := &SSHHandler{
		readFile: func(path string) ([]string, error) {
			return []string{"Host myserver"}, nil
		},
	}

	result := h.Complete(context.Background(), nil, "-p")
	if len(result.Items) != 0 {
		t.Fatalf("expected 0 items for flag arg, got %d", len(result.Items))
	}
}

func TestSSHHandler_KnownHosts(t *testing.T) {
	h := &SSHHandler{
		readFile: func(path string) ([]string, error) {
			return []string{
				"github.com ssh-rsa AAAA...",
				"192.168.1.1 ssh-ed25519 AAAA...",
				"|1|hash= ssh-rsa AAAA...", // hashed entry - should be skipped
			}, nil
		},
	}

	hosts := h.parseKnownHosts("testpath")
	if len(hosts) != 2 {
		t.Fatalf("expected 2 hosts, got %d: %v", len(hosts), hosts)
	}
}

func TestSSHHandler_EmptyConfig(t *testing.T) {
	h := &SSHHandler{
		readFile: func(path string) ([]string, error) {
			return nil, nil
		},
	}

	result := h.Complete(context.Background(), nil, "")
	if len(result.Items) != 0 {
		t.Fatalf("expected 0 items from empty config, got %d", len(result.Items))
	}
}
