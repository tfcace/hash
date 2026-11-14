package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/term"
)

// launchMode is what this hash does, decided once at startup from its
// arguments and whether stdin is a terminal: an interactiveMode,
// scriptMode, migrateMode, versionMode or helpMode. Each carries only what
// its kind uses.
type launchMode interface{ isLaunchMode() }

// interactiveMode is the REPL: stdin is a terminal, with no -c or script.
type interactiveMode struct {
	login bool // reads the login files, sets HASH_LOGIN
}

// scriptMode is a non-interactive hash: it runs a program with no prompt.
type scriptMode struct {
	source scriptSource
	login  bool // argv[0] starts with "-", or -l: reads the login files, sets HASH_LOGIN
	// rc is -i: read the interactive rc files first, as bash -ic does.
	rc bool
	// options are bash's set options from the command line (-e, -x, -u,
	// -o pipefail, +x, ...), in order, for `set` before the program.
	options []string
	// program is the -c command string, or the script file's path.
	program string
	// args are $0, $1, ....
	args []string
}

// scriptSource is where a scriptMode's program comes from.
type scriptSource int

const (
	sourceCommand scriptSource = iota // -c: the command string
	sourceFile                        // the script file program
	sourceStdin                       // stdin: -s, or it is not a terminal
)

type migrateMode struct{ args []string } // migrate [args]
type versionMode struct{}                // -v, --version, version
type helpMode struct{}                   // -h, --help

func (interactiveMode) isLaunchMode() {}
func (scriptMode) isLaunchMode()      {}
func (migrateMode) isLaunchMode()     {}
func (versionMode) isLaunchMode()     {}
func (helpMode) isLaunchMode()        {}

// launchArgs is the command line as parseArgs reads it, before it settles
// on a mode.
type launchArgs struct {
	command, stdin, version, help bool // -c, -s, -v, -h seen
	login, rc                     bool
	options                       []string
}

var (
	errNeedsCommand    = errors.New("-c: option requires an argument")
	errNeedsOptionName = errors.New("-o: option requires an argument")
)

// parseArgs decides the mode from argv the way bash reads its options: they
// end at the first word that is not one (or at -- or a lone -), short
// options combine (-lc), and -c takes the first word after the options as
// its command, the words after that as $0, $1, .... Without -c, the first
// word after the options is a script to run, with the rest as $1, $2, ...;
// with neither, a stdin that is not a terminal is the script (-s too). A
// stdin that is a terminal is the interactive shell, which takes no set
// options and no arguments. bash's set options (-e, +x, -o name) are kept
// for the program.
func parseArgs(argv []string, stdinTerminal bool) (launchMode, error) {
	var la launchArgs
	if len(argv) > 0 && strings.HasPrefix(filepath.Base(argv[0]), "-") {
		la.login = true
	}
	args := argv[min(1, len(argv)):]
	if sub, ok := subcommand(args); ok {
		return sub, nil
	}

	i := 0
	for ; i < len(args); i++ {
		if args[i] == "--" || args[i] == "-" {
			i++
			break
		}
		if len(args[i]) < 2 || (args[i][0] != '-' && args[i][0] != '+') {
			break
		}
		used, err := la.option(args[i], args[i+1:])
		if err != nil {
			return nil, err
		}
		i += used
		switch {
		case la.version:
			return versionMode{}, nil
		case la.help:
			return helpMode{}, nil
		}
	}
	return la.mode(args[i:], stdinTerminal)
}

// mode is the mode for the words after the options: the -c command and its
// $0, $1, ...; the script file and its arguments; or the script on stdin,
// with -s's $1, $2, ..., unless stdin is a terminal.
func (la launchArgs) mode(rest []string, stdinTerminal bool) (launchMode, error) {
	m := scriptMode{login: la.login, rc: la.rc, options: la.options}
	switch {
	case la.command: // -c wins over -s, as in bash
		if len(rest) == 0 {
			return nil, errNeedsCommand
		}
		m.source, m.program, m.args = sourceCommand, rest[0], positional("hash", rest[1:])
	case len(rest) > 0 && !la.stdin:
		m.source, m.program, m.args = sourceFile, rest[0], rest
	case !stdinTerminal:
		m.source, m.args = sourceStdin, append([]string{"hash"}, rest...)
	default:
		return la.interactive(rest)
	}
	return m, nil
}

// interactive is the interactive shell, or why the command line asks for
// what it does not do: set options and arguments.
func (la launchArgs) interactive(rest []string) (launchMode, error) {
	if len(la.options) > 0 {
		opt := la.options[0]
		if strings.HasSuffix(opt, "o") && len(la.options) > 1 {
			opt += " " + la.options[1]
		}
		return nil, fmt.Errorf("%s: set options apply only to -c, a script or a stdin script", opt)
	}
	if len(rest) > 0 {
		return nil, errors.New("-s: the interactive shell takes no arguments")
	}
	return interactiveMode{login: la.login}, nil
}

// subcommand is the mode for hash migrate and hash version.
func subcommand(args []string) (launchMode, bool) {
	if len(args) == 0 {
		return nil, false
	}
	switch args[0] {
	case "migrate":
		return migrateMode{args: args[1:]}, true
	case "version":
		return versionMode{}, true
	}
	return nil, false
}

// setOptions are bash's single-letter set options hash takes on the
// command line, as -x or +x.
const setOptions = "aefnux"

// option applies one option word (-l, -lc, --login, +x, -o name). -o and
// +o take their option name from next; used is how many words of next it
// took.
func (la *launchArgs) option(arg string, next []string) (used int, err error) {
	if strings.HasPrefix(arg, "--") {
		return 0, la.longOption(arg)
	}
	sign := arg[:1]
	for _, opt := range arg[1:] {
		switch {
		case opt == 'o':
			if used >= len(next) {
				return 0, errNeedsOptionName
			}
			la.options = append(la.options, sign+"o", next[used])
			used++
		case strings.ContainsRune(setOptions, opt):
			la.options = append(la.options, sign+string(opt))
		case sign == "+":
			return 0, fmt.Errorf("+%c: invalid option", opt)
		default:
			if err := la.shortOption(opt); err != nil {
				return 0, err
			}
			if la.version || la.help {
				return used, nil
			}
		}
	}
	return used, nil
}

// shortOption applies one of hash's own single-letter options.
func (la *launchArgs) shortOption(opt rune) error {
	switch opt {
	case 'l':
		la.login = true
	case 'c':
		la.command = true
	case 's':
		la.stdin = true
	case 'i':
		la.rc = true
	case 'v':
		la.version = true
	case 'h':
		la.help = true
	default:
		return fmt.Errorf("-%c: invalid option", opt)
	}
	return nil
}

// longOption applies one --word option.
func (la *launchArgs) longOption(arg string) error {
	switch arg {
	case "--login":
		la.login = true
	case "--version":
		la.version = true
	case "--help":
		la.help = true
	case "--norc", "--noprofile": // accepted for tools that pass them; no effect
	default:
		return fmt.Errorf("%s: invalid option", arg)
	}
	return nil
}

// positional is $0 and the parameters after it: the first of args names $0
// when there is one, as for bash -c, otherwise name does.
func positional(name string, args []string) []string {
	if len(args) == 0 {
		return []string{name}
	}
	return args
}

// stdinIsTerminal reports whether stdin is a terminal: a person types the
// commands.
func stdinIsTerminal() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}
