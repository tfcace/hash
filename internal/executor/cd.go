package executor

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/interp"
)

// cdUsage is the usage line bash prints after a cd option it does not
// take. -@ (extended attributes) is not offered.
const cdUsage = "cd: usage: cd [-L|[-P [-e]]] [dir]\n"

// cdCall makes a call of cd bash 5.2's cd (`cd`, `builtin cd`, `command
// cd`; ok is false for any other call). The interpreter's cd takes one
// directory and goes there logically, with messages of its own; hash
// works out the directory as bash does (the options -L, -P and -e, `--`,
// HOME, `-` for OLDPWD, CDPATH) and checks it, with bash's messages and
// statuses, and hands the interpreter's cd the absolute directory to go
// to, which sets PWD and OLDPWD. The shell's own cd (not a subshell's)
// moves hash's process there at once (enterDir). A plain cd call when a function named cd
// is defined (zoxide's `--cmd cd`) goes to the function as it is.
func (e *Executor) cdCall(ctx context.Context, args []string) (call []string, ok bool) {
	var prefix, ops []string
	switch {
	case args[0] == "cd":
		if _, isFunc := e.callerRunner(ctx).Funcs["cd"]; isFunc {
			return args, true
		}
		ops = args[1:]
	case (args[0] == "builtin" || args[0] == "command") && len(args) > 1 && args[1] == "cd":
		prefix, ops = []string{"builtin"}, args[2:] // past any function, as both are in bash
	default:
		return nil, false
	}
	hc := interp.HandlerCtx(ctx)
	dir, status := cdTarget(hc.Env, hc.Dir, ops, hc.Stdout, hc.Stderr)
	if dir == "" {
		return statusCall(status), true
	}
	if e.callerRunner(ctx) == e.runner {
		if err := e.enterDir(dir); err != nil {
			fmt.Fprintf(hc.Stderr, "hash: cd: %s: %v\n", dir, err)
			return statusCall(1), true
		}
	}
	return append(prefix, "cd", dir), true
}

// cdTarget is the absolute directory a cd with arguments args goes to from
// cwd, or "" with the status cd ends with when it goes nowhere (an error,
// said on stderr, or an empty directory, which bash takes as staying). It
// prints the directory on stdout when bash does: for `-` and a CDPATH hit.
func cdTarget(env expand.Environ, cwd string, args []string, stdout, stderr io.Writer) (dir string, status int) {
	ops, physical, bad := cdOptions(args)
	if bad != "" {
		fmt.Fprintf(stderr, "hash: cd: %s: invalid option\n%s", bad, cdUsage)
		return "", 2
	}
	var operand string
	switch len(ops) {
	case 0:
		home := env.Get("HOME")
		if !home.IsSet() {
			fmt.Fprintln(stderr, "hash: cd: HOME not set")
			return "", 1
		}
		operand = home.String()
	case 1:
		operand = ops[0]
	default:
		fmt.Fprintln(stderr, "hash: cd: too many arguments")
		return "", 1
	}
	show := false
	if operand == "-" {
		old := env.Get("OLDPWD")
		if old.String() == "" {
			fmt.Fprintln(stderr, "hash: cd: OLDPWD not set")
			return "", 1
		}
		operand, show = old.String(), true
	}
	if operand == "" {
		return "", 0
	}
	raw := cdJoin(cwd, operand)
	if hit, ok := cdPathHit(env.Get("CDPATH").String(), cwd, operand, physical); ok {
		raw, show = hit, true
	}
	target, err := cdResolve(raw, physical)
	if err != nil {
		fmt.Fprintf(stderr, "hash: cd: %s: %s\n", operand, err)
		return "", 1
	}
	if show {
		fmt.Fprintln(stdout, target)
	}
	return target, 0
}

// cdResolve is the directory cd goes to for raw, the operand joined to
// the directory cd starts in, .. and symlinks left in it: physically
// (-P), its symlinks resolved before each .. that follows them; else
// logically, raw cleaned, falling back to the physical directory when
// the logical one cannot be entered, as bash does (`cd link/../x` where
// only the link's target's parent holds x). err is why cd cannot go
// there (cdCheck).
func cdResolve(raw string, physical bool) (string, error) {
	if !physical {
		logical := filepath.Clean(raw)
		err := cdCheck(logical)
		if err == nil {
			return logical, nil
		}
		if resolved, rerr := filepath.EvalSymlinks(raw); rerr != nil || cdCheck(resolved) != nil {
			return "", err
		}
	}
	resolved, err := filepath.EvalSymlinks(raw)
	if err != nil {
		return "", cmp.Or(cdCheck(raw), err)
	}
	return resolved, cdCheck(resolved)
}

// cdOptions splits cd's arguments into its options and the rest: -P goes
// physically, -L (the default) logically, the last one given wins; -e is
// taken and does nothing more. bad is the first option cd does not take.
func cdOptions(args []string) (rest []string, physical bool, bad string) {
	for i, arg := range args {
		if arg == "--" {
			return args[i+1:], physical, ""
		}
		if len(arg) < 2 || arg[0] != '-' {
			return args[i:], physical, ""
		}
		for _, c := range arg[1:] {
			switch c {
			case 'L':
				physical = false
			case 'P':
				physical = true
			case 'e':
			default:
				return nil, false, "-" + string(c)
			}
		}
	}
	return nil, physical, ""
}

// cdPathHit is the directory CDPATH finds for operand, cwd being the
// directory cd starts in: in the first entry holding it, an empty entry
// meaning cwd, as cd would go there (cdResolve, physically for -P), the
// entry and operand joined with their .. left for it. ok is false when
// there is none, or the hit is in cwd (an empty entry), which cd goes to
// as it would without CDPATH and bash does not print. CDPATH is not
// searched for an absolute operand or one starting with . or .. .
func cdPathHit(cdpath, cwd, operand string, physical bool) (dir string, ok bool) {
	first, _, _ := strings.Cut(operand, "/")
	if cdpath == "" || filepath.IsAbs(operand) || first == "." || first == ".." {
		return "", false
	}
	for _, entry := range strings.Split(cdpath, ":") {
		candidate := cdJoin(cwd, cmp.Or(entry, ".")+"/"+operand)
		if _, err := cdResolve(candidate, physical); err == nil {
			return candidate, entry != ""
		}
	}
	return "", false
}

// cdCheck is why cd cannot go to dir, nil when it can, in the words bash
// uses (strerror's): No such file or directory, Not a directory,
// Permission denied.
func cdCheck(dir string) error {
	info, err := os.Stat(dir)
	if err == nil && !info.IsDir() {
		err = syscall.ENOTDIR
	}
	if err == nil {
		err = unix.Access(dir, unix.X_OK)
	}
	if err == nil {
		return nil
	}
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		errno = syscall.ENOENT
	}
	msg := errno.Error()
	return errors.New(strings.ToUpper(msg[:1]) + msg[1:])
}

// cdJoin is path made absolute from cwd, not cleaned: its .. still follow
// the names they follow, for the physical directory (cdResolve).
func cdJoin(cwd, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return cwd + "/" + path
}

// dirVars are the variables cd sets, which bash always exports: the
// interpreter's cd sets them unexported, so hash exports them itself.
var dirVars = []string{"PWD", "OLDPWD"}

// dirEnv is what a command's environment adds for dirVars: each one env
// holds, exported or not.
func dirEnv(env expand.Environ) []string {
	var add []string
	for _, name := range dirVars {
		if vr := env.Get(name); vr.IsSet() && !vr.Exported {
			add = append(add, name+"="+vr.String())
		}
	}
	return add
}

// exportDirVars marks dirVars exported in hash's copy of the shell's
// variables, the environment hash itself and what it starts (the prompt)
// see.
func exportDirVars(env *envStore) {
	for _, name := range dirVars {
		if vr := env.Get(name); vr.IsSet() && !vr.Exported {
			env.setExportedString(name, vr.String())
		}
	}
}
