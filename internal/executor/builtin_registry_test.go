package executor

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func registerHist(ex *Executor) {
	ex.RegisterBuiltin("hist", func(_ context.Context, args []string, stdout, _ io.Writer) error {
		if len(args) > 0 && args[0] == "boom" {
			return fmt.Errorf("exploded")
		}
		fmt.Fprintln(stdout, "jj new")
		fmt.Fprintln(stdout, "ls -la")
		return nil
	})
}

func TestRegisteredBuiltin_RunsInsidePipeline(t *testing.T) {
	ex := New()
	registerHist(ex)
	var out, errb bytes.Buffer
	res, err := ex.Execute(context.Background(), "hist | grep jj", &out, &errb)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := out.String(); got != "jj new\n" {
		t.Errorf("stdout = %q, want %q (stderr %q)", got, "jj new\n", errb.String())
	}
	if res.ExitCode != 0 {
		t.Errorf("exit code = %d, want 0", res.ExitCode)
	}
}

func TestRegisteredBuiltin_HonoursRedirection(t *testing.T) {
	ex := New()
	registerHist(ex)
	f := filepath.Join(t.TempDir(), "out")
	var out, errb bytes.Buffer
	if _, err := ex.Execute(context.Background(), "hist > "+f, &out, &errb); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	data, _ := os.ReadFile(f)
	if string(data) != "jj new\nls -la\n" {
		t.Errorf("file = %q, want both lines", string(data))
	}
	if out.Len() != 0 {
		t.Errorf("stdout = %q, want nothing (output was redirected)", out.String())
	}
}

func TestRegisteredBuiltin_ErrorSetsExitStatus(t *testing.T) {
	ex := New()
	registerHist(ex)
	var out, errb bytes.Buffer
	res, err := ex.Execute(context.Background(), "hist boom; echo rc=$?", &out, &errb)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := out.String(); got != "rc=1\n" {
		t.Errorf("stdout = %q, want %q", got, "rc=1\n")
	}
	if got := errb.String(); got != "hash: hist: exploded\n" {
		t.Errorf("stderr = %q, want %q", got, "hash: hist: exploded\n")
	}
	if res.ExitCode != 0 {
		t.Errorf("exit code = %d, want 0 (last command was echo)", res.ExitCode)
	}
}

func TestSimpleCommandWords(t *testing.T) {
	ex := New()
	tests := []struct {
		line string
		want []string // nil means "not a simple command"
	}{
		{"history", []string{"history"}},
		{"history search jj", []string{"history", "search", "jj"}},
		{`history search 'jj new'`, []string{"history", "search", "jj new"}},
		{`cd "my dir"`, []string{"cd", "my dir"}},
		{"cd ~/projects", []string{"cd", "~/projects"}},
		{"  cd   ..  ", []string{"cd", ".."}},
		{"history | grep jj", nil},
		{"cd /tmp && pwd", nil},
		{"cd /tmp; pwd", nil},
		{"history > out.txt", nil},
		{"cd $DIR", nil},
		{`cd "$(pwd)"`, nil},
		{"X=1 cd /tmp", nil},
		{"history &", nil},
		{"! history", nil},
		{"", nil},
	}
	for _, tt := range tests {
		got, ok := ex.SimpleCommandWords(tt.line)
		if tt.want == nil {
			if ok {
				t.Errorf("SimpleCommandWords(%q) = %q, true; want not simple", tt.line, got)
			}
			continue
		}
		if !ok || fmt.Sprint(got) != fmt.Sprint(tt.want) {
			t.Errorf("SimpleCommandWords(%q) = %q, %v; want %q, true", tt.line, got, ok, tt.want)
		}
	}
}
