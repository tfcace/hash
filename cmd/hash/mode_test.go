package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseArgs(t *testing.T) {
	tests := []struct {
		name string
		argv []string
		want launchMode
	}{
		{"no arguments", []string{"hash"}, interactiveMode{}},
		{"login argv0", []string{"-hash"}, interactiveMode{login: true}},
		{"login flag", []string{"hash", "-l"}, interactiveMode{login: true}},
		{"long login", []string{"hash", "--login"}, interactiveMode{login: true}},
		{"command", []string{"hash", "-c", "echo hi"}, scriptMode{source: sourceCommand, program: "echo hi", args: []string{"hash"}}},
		{"command with $0 and $1", []string{"hash", "-c", "echo", "zero", "one"}, scriptMode{source: sourceCommand, program: "echo", args: []string{"zero", "one"}}},
		{"combined login command", []string{"hash", "-lc", "echo"}, scriptMode{source: sourceCommand, login: true, program: "echo", args: []string{"hash"}}},
		{"options before the command", []string{"hash", "-c", "-l", "echo"}, scriptMode{source: sourceCommand, login: true, program: "echo", args: []string{"hash"}}},
		{"-i is accepted", []string{"hash", "-i", "-l", "-c", "echo"}, scriptMode{source: sourceCommand, login: true, rc: true, program: "echo", args: []string{"hash"}}},
		{"options end at the command", []string{"hash", "-c", "echo", "-l", "-v"}, scriptMode{source: sourceCommand, program: "echo", args: []string{"-l", "-v"}}},
		{"double dash ends options", []string{"hash", "-c", "--", "-l"}, scriptMode{source: sourceCommand, program: "-l", args: []string{"hash"}}},
		{"version flag", []string{"hash", "-v"}, versionMode{}},
		{"long version", []string{"hash", "--version"}, versionMode{}},
		{"version subcommand", []string{"hash", "version"}, versionMode{}},
		{"help", []string{"hash", "-h"}, helpMode{}},
		{"long help", []string{"hash", "--help"}, helpMode{}},
		{"migrate", []string{"hash", "migrate", "status"}, migrateMode{args: []string{"status"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseArgs(tt.argv, true)
			if err != nil {
				t.Fatalf("parseArgs(%q): %v", tt.argv, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseArgs(%q) = %+v, want %+v", tt.argv, got, tt.want)
			}
		})
	}
}

func TestParseArgs_Errors(t *testing.T) {
	tests := []struct {
		argv []string
		want string
	}{
		{[]string{"hash", "-c"}, "-c: option requires an argument"},
		{[]string{"hash", "-l", "-c"}, "-c: option requires an argument"},
		{[]string{"hash", "--bogus"}, "--bogus: invalid option"},
		{[]string{"hash", "-lq"}, "-q: invalid option"},
		{[]string{"hash", "+l"}, "+l: invalid option"},
	}
	for _, tt := range tests {
		_, err := parseArgs(tt.argv, true)
		if err == nil || err.Error() != tt.want {
			t.Errorf("parseArgs(%q) error = %v, want %q", tt.argv, err, tt.want)
		}
	}
}

// Without -c, a stdin that is not a terminal is read as a script.
func TestParseArgs_StdinNotATerminal(t *testing.T) {
	tests := []struct {
		argv []string
		want launchMode
	}{
		{[]string{"hash"}, scriptMode{source: sourceStdin, args: []string{"hash"}}},
		{[]string{"-hash"}, scriptMode{source: sourceStdin, login: true, args: []string{"hash"}}},
		{[]string{"hash", "-c", "echo"}, scriptMode{source: sourceCommand, program: "echo", args: []string{"hash"}}},
		{[]string{"hash", "version"}, versionMode{}},
		{[]string{"hash", "-s"}, scriptMode{source: sourceStdin, args: []string{"hash"}}},
		{[]string{"hash", "-s", "a", "b"}, scriptMode{source: sourceStdin, args: []string{"hash", "a", "b"}}},
		{[]string{"hash", "-x"}, scriptMode{source: sourceStdin, options: []string{"-x"}, args: []string{"hash"}}},
	}
	for _, tt := range tests {
		got, err := parseArgs(tt.argv, false)
		if err != nil || !reflect.DeepEqual(got, tt.want) {
			t.Errorf("parseArgs(%q, false) = %+v, %v; want %+v", tt.argv, got, err, tt.want)
		}
	}
}

func TestParseArgs_Scripts(t *testing.T) {
	tests := []struct {
		argv []string
		want launchMode
	}{
		{[]string{"hash", "s.sh"}, scriptMode{source: sourceFile, program: "s.sh", args: []string{"s.sh"}}},
		{[]string{"hash", "-l", "s.sh", "a", "-c"}, scriptMode{source: sourceFile, login: true, program: "s.sh", args: []string{"s.sh", "a", "-c"}}},
		{[]string{"hash", "--", "-s.sh"}, scriptMode{source: sourceFile, program: "-s.sh", args: []string{"-s.sh"}}},
		{[]string{"hash", "-sc", "echo", "x"}, scriptMode{source: sourceCommand, program: "echo", args: []string{"x"}}},
		{[]string{"hash", "-cs", "echo"}, scriptMode{source: sourceCommand, program: "echo", args: []string{"hash"}}},
	}
	for _, tt := range tests {
		got, err := parseArgs(tt.argv, true)
		if err != nil || !reflect.DeepEqual(got, tt.want) {
			t.Errorf("parseArgs(%q) = %+v, %v; want %+v", tt.argv, got, err, tt.want)
		}
	}
}

// bash's own options pass through to `set` before the program, so tools
// that run `$SHELL -e -o pipefail -c ...` get them; a lone - ends the
// options; -i asks for an interactive shell's startup files.
func TestParseArgs_BashOptions(t *testing.T) {
	tests := []struct {
		argv []string
		want launchMode
	}{
		{[]string{"hash", "-e", "-x", "-c", "echo"}, scriptMode{source: sourceCommand, options: []string{"-e", "-x"}, program: "echo", args: []string{"hash"}}},
		{[]string{"hash", "-euxc", "echo"}, scriptMode{source: sourceCommand, options: []string{"-e", "-u", "-x"}, program: "echo", args: []string{"hash"}}},
		{[]string{"hash", "-o", "pipefail", "+x", "-c", "echo"}, scriptMode{source: sourceCommand, options: []string{"-o", "pipefail", "+x"}, program: "echo", args: []string{"hash"}}},
		{[]string{"hash", "-eo", "pipefail", "s.sh"}, scriptMode{source: sourceFile, options: []string{"-e", "-o", "pipefail"}, program: "s.sh", args: []string{"s.sh"}}},
		{[]string{"hash", "+o", "errexit", "-c", "echo"}, scriptMode{source: sourceCommand, options: []string{"+o", "errexit"}, program: "echo", args: []string{"hash"}}},
		{[]string{"hash", "--norc", "--noprofile", "-c", "echo"}, scriptMode{source: sourceCommand, program: "echo", args: []string{"hash"}}},
		{[]string{"hash", "-ic", "echo"}, scriptMode{source: sourceCommand, rc: true, program: "echo", args: []string{"hash"}}},
		{[]string{"hash", "-", "s.sh", "-x"}, scriptMode{source: sourceFile, program: "s.sh", args: []string{"s.sh", "-x"}}},
		{[]string{"hash", "-l", "-"}, interactiveMode{login: true}},
	}
	for _, tt := range tests {
		got, err := parseArgs(tt.argv, true)
		if err != nil || !reflect.DeepEqual(got, tt.want) {
			t.Errorf("parseArgs(%q) = %+v, %v; want %+v", tt.argv, got, err, tt.want)
		}
	}
	if _, err := parseArgs([]string{"hash", "-o"}, true); err == nil || err.Error() != "-o: option requires an argument" {
		t.Errorf("hash -o: error %v", err)
	}
}

// The interactive shell takes no set options and no arguments, so a launch
// that gives it some is refused, not run without them; -s at a terminal is
// the interactive shell, as in bash.
func TestParseArgs_InteractiveLaunch(t *testing.T) {
	for argv, want := range map[string]string{
		"hash -x":      "-x: set options apply only to -c, a script or a stdin script",
		"hash -o vi":   "-o vi: set options apply only to -c, a script or a stdin script",
		"hash -e -l":   "-e: set options apply only to -c, a script or a stdin script",
		"hash -s a b":  "-s: the interactive shell takes no arguments",
		"hash -l -s a": "-s: the interactive shell takes no arguments",
	} {
		_, err := parseArgs(strings.Fields(argv), true)
		if err == nil || err.Error() != want {
			t.Errorf("parseArgs(%q) error = %v, want %q", argv, err, want)
		}
	}
	for _, argv := range []string{"hash -s", "hash -i", "hash -l -s"} {
		got, err := parseArgs(strings.Fields(argv), true)
		if _, ok := got.(interactiveMode); err != nil || !ok {
			t.Errorf("parseArgs(%q) = %+v, %v; want the interactive shell", argv, got, err)
		}
	}
}
