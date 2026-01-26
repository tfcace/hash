//go:build e2e

package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/tfcace/hash/internal/agent"
	"github.com/tfcace/hash/internal/clipboard"
	"github.com/tfcace/hash/internal/parser"
	"github.com/tfcace/hash/internal/shell"
)

// TestDevOps_KubernetesHealthCheck tests kubectl pod health recipes.
// Website promise: kubectl get pods -A | ?? show unhealthy pods and their restart counts
func TestDevOps_KubernetesHealthCheck(t *testing.T) {
	// Sample kubectl output
	kubectlOutput := `NAMESPACE     NAME                                READY   STATUS             RESTARTS   AGE
default       nginx-deployment-5d59c5d84-abc12   1/1     Running            0          5d
default       redis-master-0                     1/1     Running            3          10d
kube-system   coredns-5dd5756b68-xyz99          0/1     CrashLoopBackOff   15         2d
kube-system   metrics-server-abc123              1/1     Running            0          30d
monitoring    prometheus-0                       1/1     Running            1          7d`

	mock := NewScenarioMock().
		OnPipePromptContains("unhealthy", agent.Response{
			Type:    agent.ResponseTypeCommand,
			Command: `awk 'NR==1 || $4!="Running" || $5>0 {print $1, $2, $4, $5}'`,
		}).
		OnPipePromptContains("restart", agent.Response{
			Type:    agent.ResponseTypeCommand,
			Command: `awk '$5>0 {print $2": "$5" restarts"}'`,
		}).
		OnPipePromptContains("crashloop", agent.Response{
			Type:    agent.ResponseTypeCommand,
			Command: `grep -i crashloop`,
		})

	client := agent.NewClient(mock)
	handler := shell.NewAgentHandler(client)

	clipBuf := clipboard.NewBuffer(8192)
	handler.SetClipboard(clipBuf)
	clipBuf.AddCommand("kubectl get pods -A")
	clipBuf.SetOutput(kubectlOutput)

	ctx := context.Background()
	if err := mock.Connect(ctx); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	tests := []struct {
		name   string
		input  string
		hasCmd bool
	}{
		{
			name:   "show unhealthy pods",
			input:  "kubectl get pods -A | ?? show unhealthy pods and their restart counts",
			hasCmd: true,
		},
		{
			name:   "pods with restarts",
			input:  "kubectl get pods | ?? show pods with restart count > 0",
			hasCmd: true,
		},
		{
			name:   "find crashloops",
			input:  "kubectl get pods -A | ?? find pods in crashloop",
			hasCmd: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed := parser.Parse(tt.input)
			if parsed.Type != parser.CommandTypeAgentPipe {
				t.Fatalf("Parse type = %v, want AgentPipe", parsed.Type)
			}

			resp, err := handler.HandleRequest(ctx, parsed)
			if err != nil {
				t.Fatalf("HandleRequest() error = %v", err)
			}

			if tt.hasCmd && resp.Command == "" {
				t.Error("Expected command response")
			}
		})
	}
}

// TestDevOps_DockerCleanup tests docker cleanup recipes.
// Website promise: docker ps -a | ?? find exited containers from last week
func TestDevOps_DockerCleanup(t *testing.T) {
	// Sample docker ps output
	dockerOutput := `CONTAINER ID   IMAGE          COMMAND       CREATED        STATUS                     NAMES
abc123def456   nginx:latest   "nginx -g"    2 days ago     Exited (0) 2 days ago      web-old
fed789ghi012   redis:6        "redis..."    5 days ago     Exited (137) 5 days ago    cache-old
xyz456abc789   postgres:13    "docker..."   3 hours ago    Up 3 hours                 db-active
mno321pqr654   nginx:alpine   "nginx -g"    10 days ago    Exited (0) 10 days ago     web-ancient`

	mock := NewScenarioMock().
		OnPipePromptContains("exited", agent.Response{
			Type:    agent.ResponseTypeCommand,
			Command: `awk '/Exited/ && /days ago/ && $NF+0 < 8 {print $1}'`,
		}).
		OnPipePromptContains("dangling", agent.Response{
			Type:    agent.ResponseTypeCommand,
			Command: `docker images -f "dangling=true" -q`,
		}).
		OnPipePromptContains("remove", agent.Response{
			Type:    agent.ResponseTypeCommand,
			Command: `xargs docker rm`,
		})

	client := agent.NewClient(mock)
	handler := shell.NewAgentHandler(client)

	clipBuf := clipboard.NewBuffer(8192)
	handler.SetClipboard(clipBuf)
	clipBuf.AddCommand("docker ps -a")
	clipBuf.SetOutput(dockerOutput)

	ctx := context.Background()
	if err := mock.Connect(ctx); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	tests := []struct {
		name   string
		input  string
		hasCmd bool
	}{
		{
			name:   "find old exited containers",
			input:  "docker ps -a | ?? find exited containers from last week",
			hasCmd: true,
		},
		{
			name:   "find dangling images",
			input:  "docker images | ?? find dangling images",
			hasCmd: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed := parser.Parse(tt.input)
			resp, err := handler.HandleRequest(ctx, parsed)
			if err != nil {
				t.Fatalf("HandleRequest() error = %v", err)
			}

			if tt.hasCmd && resp.Command == "" {
				t.Error("Expected command response")
			}
		})
	}
}

// TestDevOps_DirectCommands tests non-pipe devops commands.
func TestDevOps_DirectCommands(t *testing.T) {
	mock := NewScenarioMock().
		OnPromptContains("disk space", agent.Response{
			Type:    agent.ResponseTypeCommand,
			Command: `df -h / | tail -1 | awk '{print $5}'`,
		}).
		OnPromptContains("memory usage", agent.Response{
			Type:    agent.ResponseTypeCommand,
			Command: `free -h | awk '/^Mem:/ {print $3"/"$2}'`,
		}).
		OnPromptContains("top cpu", agent.Response{
			Type:    agent.ResponseTypeCommand,
			Command: `ps aux --sort=-%cpu | head -5`,
		}).
		OnPromptContains("listening ports", agent.Response{
			Type:    agent.ResponseTypeCommand,
			Command: `ss -tlnp`,
		})

	client := agent.NewClient(mock)
	handler := shell.NewAgentHandler(client)

	ctx := context.Background()
	if err := mock.Connect(ctx); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	tests := []struct {
		name        string
		input       string
		wantCommand string
	}{
		{
			name:        "disk space",
			input:       "?? check disk space usage",
			wantCommand: `df -h / | tail -1 | awk '{print $5}'`,
		},
		{
			name:        "memory usage",
			input:       "?? show memory usage",
			wantCommand: `free -h | awk '/^Mem:/ {print $3"/"$2}'`,
		},
		{
			name:        "top cpu processes",
			input:       "?? find top cpu consuming processes",
			wantCommand: `ps aux --sort=-%cpu | head -5`,
		},
		{
			name:        "listening ports",
			input:       "?? show all listening ports",
			wantCommand: `ss -tlnp`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed := parser.Parse(tt.input)
			if parsed.Type != parser.CommandTypeAgent {
				t.Fatalf("Parse type = %v, want Agent", parsed.Type)
			}

			resp, err := handler.HandleRequest(ctx, parsed)
			if err != nil {
				t.Fatalf("HandleRequest() error = %v", err)
			}

			if resp.Command != tt.wantCommand {
				t.Errorf("Command = %q, want %q", resp.Command, tt.wantCommand)
			}
		})
	}
}

// TestDevOps_WithChaos tests devops scenarios under chaos conditions.
func TestDevOps_WithChaos(t *testing.T) {
	mock := NewScenarioMockWithSeed(42).
		OnPromptContains("pods", agent.Response{
			Type:    agent.ResponseTypeCommand,
			Command: `kubectl get pods --all-namespaces`,
		}).
		WithChaos(ChaosConfig{
			FailureRate:   0.2,  // 20% failure rate
			MinDelay:      5 * time.Millisecond,
			MaxDelay:      20 * time.Millisecond,
			TimeoutRate:   0.1,  // 10% timeout rate
			ErrorMessages: []string{
				"connection to cluster timed out",
				"unable to connect to the server",
				"context deadline exceeded",
			},
		})

	client := agent.NewClient(mock)
	handler := shell.NewAgentHandler(client)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := mock.Connect(ctx); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	parsed := parser.Parse("?? list all pods")

	// Run multiple times to exercise chaos
	successCount := 0
	iterations := 30

	for i := 0; i < iterations; i++ {
		resp, err := handler.HandleRequest(ctx, parsed)
		if err == nil && resp.Type == agent.ResponseTypeCommand {
			successCount++
		}
	}

	t.Logf("DevOps chaos test: %d/%d succeeded", successCount, iterations)

	// With 20% failure + 10% timeout, expect ~70% success
	expectedMinSuccess := int(float64(iterations) * 0.5) // At least 50%
	if successCount < expectedMinSuccess {
		t.Errorf("Success rate too low: %d/%d (expected at least %d)", successCount, iterations, expectedMinSuccess)
	}
}
