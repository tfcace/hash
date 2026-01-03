package agent

import (
	"os"
	"testing"
)

func TestContextBuilder_Cwd(t *testing.T) {
	cwd, _ := os.Getwd()
	builder := NewContextBuilder()
	ctx := builder.Build()

	if ctx.Cwd != cwd {
		t.Errorf("Cwd = %q, want %q", ctx.Cwd, cwd)
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
