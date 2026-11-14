package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestContextBuilder_Cwd(t *testing.T) {
	cwd, _ := os.Getwd()
	builder := NewContextBuilder()
	ctx := builder.Build()

	if ctx.Cwd != cwd {
		t.Errorf("Cwd = %q, want %q", ctx.Cwd, cwd)
	}
}

func TestContextBuilder_HungProbes(t *testing.T) {
	for _, name := range []string{"git", "kubectl"} {
		t.Run(name, func(t *testing.T) {
			bin := t.TempDir()
			if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\nexec /bin/sleep 2\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin)
			started := time.Now()
			builder := NewContextBuilder()
			if name == "git" {
				builder.DetectGitBranch(context.Background())
			} else {
				builder.DetectKubeContext(context.Background())
			}
			if elapsed := time.Since(started); elapsed > time.Second {
				t.Fatalf("optional %s probe blocked for %v", name, elapsed)
			}
			if got := builder.Build(); got.GitBranch != "" || got.KubeContext != "" {
				t.Fatalf("hung probe supplied context: %+v", got)
			}
		})
	}
}

func TestContextBuilder_ProbeResults(t *testing.T) {
	bin := t.TempDir()
	for _, name := range []string{"git", "kubectl"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\nprintf '  detected  \\n'\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	ctx := context.Background()
	got := NewContextBuilder().DetectGitBranch(ctx).DetectKubeContext(ctx).Build()
	if got.GitBranch != "detected" || got.KubeContext != "detected" {
		t.Fatalf("context = %+v, want detected branch and kube context", got)
	}
}

func TestContextBuilder_CanceledProbesDoNotStart(t *testing.T) {
	bin := t.TempDir()
	marker := filepath.Join(bin, "started")
	for _, name := range []string{"git", "kubectl"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\nprintf started > \"$HASH_PROBE_MARKER\"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	t.Setenv("HASH_PROBE_MARKER", marker)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got := NewContextBuilder().DetectGitBranch(ctx).DetectKubeContext(ctx).Build()
	if got.GitBranch != "" || got.KubeContext != "" {
		t.Fatalf("canceled probes supplied context: %+v", got)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("probe started despite cancellation: %v", err)
	}
}

func TestContextBuilder_ProbeDescendantHoldingOutput(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "kubectl"), []byte("#!/bin/sh\n/bin/sleep 2 &\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	started := time.Now()
	got := NewContextBuilder().DetectKubeContext(context.Background()).Build()
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("descendant kept the context probe blocked for %v", elapsed)
	}
	if got.KubeContext != "" {
		t.Fatalf("incomplete probe supplied context %q", got.KubeContext)
	}
}

func TestContextBuilder_WithHistory(t *testing.T) {
	builder := NewContextBuilder()
	builder.WithHistory([]string{"ls", "pwd", "cd .."})
	ctx := builder.Build()

	if len(ctx.History) != 3 {
		t.Errorf("History length = %d, want 3", len(ctx.History))
	}
	if ctx.History[0] != "ls" {
		t.Errorf("History[0] = %q, want %q", ctx.History[0], "ls")
	}
}

func TestContextBuilder_WithEnvVars(t *testing.T) {
	builder := NewContextBuilder()
	builder.WithEnvVars([]string{"HOME", "USER"})
	ctx := builder.Build()

	if _, ok := ctx.EnvVars["HOME"]; !ok {
		t.Error("EnvVars missing HOME")
	}
}

func TestContextBuilder_WithLastOutput(t *testing.T) {
	builder := NewContextBuilder()
	builder.WithLastOutput("some output")
	ctx := builder.Build()

	if ctx.LastOutput != "some output" {
		t.Errorf("LastOutput = %q, want %q", ctx.LastOutput, "some output")
	}
}
