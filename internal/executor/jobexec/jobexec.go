// Package jobexec starts a background job's command with the signals its
// place calls for ignored (Group):
//
//   - in a process group of its own (the interactive shell's job, as a job
//     of bash with job control), SIGTTOU and SIGTTIN: the terminal's
//     Ctrl+C, Ctrl+\ and hangup reach only the shell's foreground, but
//     hash has no job control to continue a job the terminal stopped, so
//     the job must not stop: with SIGTTOU ignored it may set the terminal
//     up (stty), and with SIGTTIN ignored a read of the terminal fails
//     (EIO) instead of stopping it;
//   - in the shell's process group (a script's job, as a job of bash with
//     no job control), SIGINT and SIGQUIT: the terminal's Ctrl+C and
//     Ctrl+\ reach it, and it must not die of them.
//
// Go cannot start a child with a signal ignored that the parent handles
// (SysProcAttr has no such field, and an ignore in hash would be hash's
// own), so hash starts itself (Args) and this package's init, in that
// process, sets the ignore and execs the command: exec keeps an ignored
// signal ignored. The command gets its argv[0] as typed, its environment
// and every other signal.
//
// The init runs in any program that links the package (hash, and the test
// binaries of the packages that run jobs), before main and before the
// inits of the packages that import it. It does nothing unless the program
// was started by Args.
package jobexec

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"unicode"
	"unicode/utf8"
)

// token is the first argument that makes a hash the start of a job's
// command; no one types it. The second is the job's Group.
const token = "__hash_job_exec__"

// Group is the process group a job's command runs in, which decides the
// signals it ignores.
type Group string

const (
	OwnGroup   Group = "own"   // a group of its own: SIGTTOU and SIGTTIN ignored
	ShellGroup Group = "shell" // the shell's: SIGINT and SIGQUIT ignored
)

// ignored are the signals a command of group g starts with ignored.
func (g Group) ignored() []os.Signal {
	if g == OwnGroup {
		return []os.Signal{syscall.SIGTTOU, syscall.SIGTTIN}
	}
	return []os.Signal{syscall.SIGINT, syscall.SIGQUIT}
}

// NoShebangShell runs a file with no shebang that the system would not
// exec (ENOEXEC), as bash runs it with itself and execvp with /bin/sh.
const NoShebangShell = "/bin/sh"

// Args is the argv for the program self (hash) that starts the program at
// path with argv (argv[0] its name as typed), in group g.
func Args(self string, g Group, path string, argv []string) []string {
	return append([]string{self, token, string(g), path}, argv...)
}

func init() {
	if len(os.Args) < 5 || os.Args[1] != token {
		return
	}
	os.Exit(start(Group(os.Args[2]), os.Args[3], os.Args[4:]))
}

// start ignores the signals of group g and execs path with argv. It
// returns only when the exec failed, with the status to exit with, having
// said why as bash does; a file with no shebang that is not a binary runs
// as a sh script instead, as it would in the foreground.
func start(g Group, path string, argv []string) int {
	signal.Ignore(g.ignored()...)
	err := syscall.Exec(path, argv, os.Environ()) //nolint:gosec // G702: the command the shell's user ran
	reason, script := Failure(path, err)
	if script {
		err = syscall.Exec(NoShebangShell, append([]string{"sh", ScriptName(path, argv[0])}, argv[1:]...), os.Environ()) //nolint:gosec // G702: as above
		reason, _ = Failure(NoShebangShell, err)
	}
	fmt.Fprintf(os.Stderr, "hash: %s: %s\n", argv[0], reason)
	return 126
}

// ScriptName is how a file with no shebang that runs as a sh script names
// itself ($0): as typed when the name is a path (./tool), else by its path.
func ScriptName(path, name string) string {
	if strings.Contains(name, "/") {
		return name
	}
	return path
}

// Failure is what a failed exec of path means: script is true for a file
// with no shebang that is not a binary (ENOEXEC), which runs as a sh script
// instead; else reason says why in bash's words ("cannot execute binary
// file: Exec format error", "Argument list too long"), "" when err is no
// exec failure.
func Failure(path string, err error) (reason string, script bool) {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return "", false
	}
	head := fileHead(path)
	switch {
	case errno == syscall.ENOEXEC && !binaryHead(head):
		return "", true
	case errno == syscall.ENOEXEC:
		return "cannot execute binary file: " + Strerror(errno), false
	case errno == syscall.ENOENT && bytes.HasPrefix(head, []byte("#!")):
		interp, _, _ := bytes.Cut(head[2:], []byte("\n"))
		if fields := strings.Fields(string(interp)); len(fields) > 0 {
			return fields[0] + ": bad interpreter: " + Strerror(errno), false
		}
	}
	return Strerror(errno), false
}

// fileHead is the first 80 bytes of the file at path, what bash looks at
// to tell a binary from a script; nil when it cannot be read.
func fileHead(path string) []byte {
	f, err := os.Open(path) //nolint:gosec // G703: the command's own file, which the shell was to run
	if err != nil {
		return nil
	}
	defer f.Close()
	head := make([]byte, 80)
	n, _ := io.ReadFull(f, head)
	return head[:n]
}

// binaryHead reports whether a file that begins with head is a binary, as
// bash's check_binary_file tells: an ELF header, or a NUL in its first line
// (in its first two lines after a #! line).
func binaryHead(head []byte) bool {
	if bytes.HasPrefix(head, []byte("\x7fELF")) {
		return true
	}
	lines := 1
	if bytes.HasPrefix(head, []byte("#!")) {
		lines = 2
	}
	for _, c := range head {
		if c == '\n' {
			lines--
			if lines == 0 {
				return false
			}
		}
		if c == 0 {
			return true
		}
	}
	return false
}

// Strerror is errno's message as the C library words it ("Argument list
// too long"): Go's, capitalized.
func Strerror(errno syscall.Errno) string {
	msg := errno.Error()
	r, size := utf8.DecodeRuneInString(msg)
	return string(unicode.ToUpper(r)) + msg[size:]
}
