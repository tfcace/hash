package agentupdate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

func lookPathWithNpm(bin string) func(string) (string, error) {
	return func(name string) (string, error) {
		switch name {
		case "npm":
			return "/fake/npm", nil
		case "claude-agent-acp":
			return bin, nil
		}
		return "", errors.New("not found")
	}
}

func TestInstaller_RunsNpmAndRedetects(t *testing.T) {
	pkgDir, bin := fakeNpmTree(t, "0.42.0")
	var ran [][]string
	run := func(_ context.Context, name string, args []string, out io.Writer) error {
		ran = append(ran, append([]string{name}, args...))
		writePackageJSON(t, pkgDir, "0.88.0") // what npm does: replace the package
		fmt.Fprintln(out, "added 3 packages in 12s")
		return nil
	}
	inst := Installer{Command: "claude-agent-acp", LookPath: lookPathWithNpm(bin), Run: run}

	var out bytes.Buffer
	res, err := inst.Install(context.Background(), &out)
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	want := "/fake/npm install -g @agentclientprotocol/claude-agent-acp@latest"
	if len(ran) != 1 || strings.Join(ran[0], " ") != want {
		t.Errorf("ran %v, want [%s]", ran, want)
	}
	if res.Before.Version != "0.42.0" || res.After.Version != "0.88.0" {
		t.Errorf("Result = before %q after %q", res.Before.Version, res.After.Version)
	}
	if !strings.Contains(out.String(), "added 3 packages") {
		t.Errorf("npm output not streamed: %q", out.String())
	}
}

func TestInstaller_NoNpm(t *testing.T) {
	_, bin := fakeNpmTree(t, "0.42.0")
	inst := Installer{Command: "claude-agent-acp", LookPath: lookPathFor(bin),
		Run: func(context.Context, string, []string, io.Writer) error { t.Fatal("npm must not run"); return nil }}
	if _, err := inst.Install(context.Background(), io.Discard); !errors.Is(err, ErrNoNPM) {
		t.Errorf("Install() error = %v, want ErrNoNPM", err)
	}
}

func TestInstaller_PermissionDenied(t *testing.T) {
	_, bin := fakeNpmTree(t, "0.42.0")
	run := func(_ context.Context, _ string, _ []string, out io.Writer) error {
		fmt.Fprintln(out, "npm error code EACCES")
		fmt.Fprintln(out, "npm error syscall mkdir")
		return errors.New("exit status 243")
	}
	inst := Installer{Command: "claude-agent-acp", LookPath: lookPathWithNpm(bin), Run: run}
	var out bytes.Buffer
	if _, err := inst.Install(context.Background(), &out); !errors.Is(err, ErrPermission) {
		t.Errorf("Install() error = %v, want ErrPermission", err)
	}
	if !strings.Contains(out.String(), "EACCES") {
		t.Error("npm's own error output should still reach the user")
	}
}

func TestInstaller_GlobalPrefix(t *testing.T) {
	_, bin := fakeNpmTree(t, "0.42.0")
	run := func(_ context.Context, _ string, args []string, out io.Writer) error {
		if strings.Join(args, " ") != "prefix -g" {
			t.Errorf("args = %v, want [prefix -g]", args)
		}
		fmt.Fprintln(out, "/opt/homebrew")
		return nil
	}
	inst := Installer{Command: "claude-agent-acp", LookPath: lookPathWithNpm(bin), Run: run}
	if got := inst.GlobalPrefix(context.Background()); got != "/opt/homebrew" {
		t.Errorf("GlobalPrefix() = %q", got)
	}
}

func TestExecRunner_CombinesOutputAndReportsExit(t *testing.T) {
	var buf bytes.Buffer
	err := ExecRunner(context.Background(), "sh", []string{"-c", "echo OUT; echo npm error code EACCES 1>&2; exit 243"}, &buf)
	if err == nil {
		t.Fatal("ExecRunner() error = nil, want the non-zero exit reported")
	}
	out := buf.String()
	if !strings.Contains(out, "OUT") || !strings.Contains(out, "npm error code EACCES") {
		t.Errorf("combined output = %q, want both the stdout and stderr lines", out)
	}
}
