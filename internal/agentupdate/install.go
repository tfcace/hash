package agentupdate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// ErrNoNPM means npm is not on PATH, so hash cannot run the install.
var ErrNoNPM = errors.New("npm not found on PATH")

// ErrPermission means npm could not write to its global prefix.
var ErrPermission = errors.New("npm could not write to its global prefix")

// Runner runs a program with args, streaming combined output to out.
type Runner func(ctx context.Context, name string, args []string, out io.Writer) error

// Installer runs `npm install -g <Package>@latest` and reports what landed.
// Installer{Command: cmd} is the production value.
type Installer struct {
	Command  string                       // the configured agent command, re-detected after install
	LookPath func(string) (string, error) // exec.LookPath when nil
	Run      Runner                       // ExecRunner when nil
}

// Result is the install before and after npm ran. When After equals Before
// although a newer version exists, npm wrote to a prefix that is not the one
// the command on PATH comes from; the caller judges that with the known
// latest version and GlobalPrefix.
type Result struct {
	Before Install
	After  Install
}

// InstallCommand is the exact command Install runs, for display.
func InstallCommand() string {
	return "npm install -g " + Package + "@latest"
}

// Install runs the npm install, streaming its output to out.
func (i Installer) Install(ctx context.Context, out io.Writer) (Result, error) {
	run, lookPath := i.runner(), i.lookPath()
	var res Result
	res.Before, _ = Detect(i.Command, lookPath)

	npm, err := lookPath("npm")
	if err != nil {
		return res, ErrNoNPM
	}
	var captured bytes.Buffer
	if err := run(ctx, npm, []string{"install", "-g", Package + "@latest"}, io.MultiWriter(out, &captured)); err != nil {
		if strings.Contains(captured.String(), "EACCES") {
			return res, ErrPermission
		}
		return res, fmt.Errorf("npm install: %w", err)
	}
	res.After, _ = Detect(i.Command, lookPath)
	return res, nil
}

// GlobalPrefix is where this npm installs global packages (`npm prefix -g`),
// or "" when that cannot be determined.
func (i Installer) GlobalPrefix(ctx context.Context) string {
	npm, err := i.lookPath()("npm")
	if err != nil {
		return ""
	}
	var out bytes.Buffer
	if err := i.runner()(ctx, npm, []string{"prefix", "-g"}, &out); err != nil {
		return ""
	}
	return strings.TrimSpace(out.String())
}

func (i Installer) runner() Runner {
	if i.Run != nil {
		return i.Run
	}
	return ExecRunner
}

func (i Installer) lookPath() func(string) (string, error) {
	if i.LookPath != nil {
		return i.LookPath
	}
	return exec.LookPath
}

// ExecRunner runs the program with os/exec, stdout and stderr both to out.
// Pass a writer that is not an *os.File so the child sees a pipe and npm
// prints plain text instead of colors and a progress bar. When ctx is
// canceled the child gets SIGINT and up to five seconds to finish, so an
// interrupted install is not left half-replaced by a SIGKILL.
func ExecRunner(ctx context.Context, name string, args []string, out io.Writer) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 5 * time.Second
	cmd.Stdout = out
	cmd.Stderr = out
	return cmd.Run()
}
