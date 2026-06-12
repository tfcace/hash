package completion

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"
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

func TestSSHHandler_CachesCollectedHosts(t *testing.T) {
	calls := 0
	h := &SSHHandler{
		cacheTTL: time.Minute,
		readFile: func(path string) ([]string, error) {
			calls++
			if strings.Contains(path, "known_hosts") {
				return nil, nil
			}
			return []string{
				"Host devbox",
				"Host prod",
			}, nil
		},
	}

	first := h.Complete(context.Background(), nil, "dev")
	second := h.Complete(context.Background(), nil, "pro")

	if calls != 2 {
		t.Fatalf("expected config and known_hosts to be read once, got %d reads", calls)
	}
	if len(first.Items) != 1 || first.Items[0].Value != "devbox" {
		t.Fatalf("unexpected first result: %#v", first.Items)
	}
	if len(second.Items) != 1 || second.Items[0].Value != "prod" {
		t.Fatalf("unexpected cached second result: %#v", second.Items)
	}
}

func TestSSHHandler_ReturnsWhenReadFileBlocks(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	h := &SSHHandler{
		readFile: func(path string) ([]string, error) {
			<-release
			return []string{"Host devbox"}, nil
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	start := time.Now()
	result := h.Complete(ctx, nil, "dev")
	elapsed := time.Since(start)

	if elapsed > 100*time.Millisecond {
		t.Fatalf("SSH completion took %s after context cancellation, want under 100ms", elapsed)
	}
	if len(result.Items) != 0 {
		t.Fatalf("expected no items after context cancellation, got %#v", result.Items)
	}
}

func TestSSHHandler_CoalescesBlockedReadFile(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	var calls atomic.Int32
	h := &SSHHandler{
		readFile: func(path string) ([]string, error) {
			calls.Add(1)
			<-release
			return []string{"Host devbox"}, nil
		},
	}

	for i := 0; i < 2; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		_ = h.Complete(ctx, nil, "dev")
		cancel()
	}

	if got := calls.Load(); got != 1 {
		t.Fatalf("expected one in-flight SSH file read, got %d", got)
	}
}

func TestSSHHandler_DoesNotCacheCanceledRead(t *testing.T) {
	release := make(chan struct{})
	finished := make(chan struct{})
	var first atomic.Bool
	first.Store(true)
	h := &SSHHandler{
		cacheTTL: time.Minute,
		readFile: func(path string) ([]string, error) {
			if first.Swap(false) {
				<-release
				close(finished)
			}
			return []string{"Host devbox"}, nil
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	result := h.Complete(ctx, nil, "dev")
	cancel()
	if len(result.Items) != 0 {
		t.Fatalf("expected no items after timeout, got %#v", result.Items)
	}

	close(release)
	<-finished

	result = h.Complete(context.Background(), nil, "dev")
	if len(result.Items) != 1 || result.Items[0].Value != "devbox" {
		t.Fatalf("canceled read should not cache an empty result, got %#v", result.Items)
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

func TestSSHHandler_KnownHostsIPv6(t *testing.T) {
	h := &SSHHandler{
		readFile: func(path string) ([]string, error) {
			return []string{
				"[2001:db8::1]:2222 ssh-ed25519 AAAA...",
				"[::1]:22 ssh-rsa AAAA...",
				"plain.example.com ssh-rsa AAAA...",
			}, nil
		},
	}

	hosts := h.parseKnownHosts("testpath")
	expected := []string{"2001:db8::1", "::1", "plain.example.com"}
	if len(hosts) != len(expected) {
		t.Fatalf("expected %d hosts, got %d: %v", len(expected), len(hosts), hosts)
	}
	for i, want := range expected {
		if hosts[i] != want {
			t.Errorf("hosts[%d] = %q, want %q", i, hosts[i], want)
		}
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
