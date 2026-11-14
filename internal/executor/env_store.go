package executor

import (
	"os"
	"runtime"
	"slices"
	"strings"

	"mvdan.cc/sh/v3/expand"
)

type envStore struct {
	vars map[string]expand.Variable
}

func newEnvStoreFromOS() *envStore {
	vars := make(map[string]expand.Variable)
	for _, pair := range os.Environ() {
		name, value, ok := strings.Cut(pair, "=")
		if !ok || name == "" {
			continue
		}
		name = normalizeEnvName(name)
		vars[name] = expand.Variable{
			Set:      true,
			Exported: true,
			Kind:     expand.String,
			Str:      value,
		}
	}
	vars[flagsVar] = interactiveFlags()
	return &envStore{vars: vars}
}

// flagsVar is what the interactive shell's $- expands, ${__hash_flags[$-]}
// (markInteractive): a read-only associative array from each value the
// interpreter's $- can have, its set flags (aefnux), to the same flags with
// the interactive shell's i among them, as bash's $- has it. It is in
// every shell's variables, as hash's own, so that every runner and every
// copy of one (a subshell, a pipeline's command, a job) has it.
const flagsVar = "__hash_flags"

// posixFlags are the flags the interpreter's $- can hold, sorted, as it
// prints them.
const posixFlags = "aefnux"

// interactiveFlags is flagsVar's value.
func interactiveFlags() expand.Variable {
	m := make(map[string]string, 1<<len(posixFlags))
	for set := range 1 << len(posixFlags) {
		var flags []byte
		for i := range len(posixFlags) {
			if set&(1<<i) != 0 {
				flags = append(flags, posixFlags[i])
			}
		}
		withI := append(slices.Clone(flags), 'i')
		slices.Sort(withI)
		m[string(flags)] = string(withI)
	}
	return expand.Variable{Set: true, ReadOnly: true, Kind: expand.Associative, Map: m}
}

func normalizeEnvName(name string) string {
	if runtime.GOOS == "windows" {
		return strings.ToUpper(name)
	}
	return name
}

func (e *envStore) Get(name string) expand.Variable {
	if e == nil {
		return expand.Variable{}
	}
	vr, ok := e.vars[normalizeEnvName(name)]
	if !ok {
		return expand.Variable{}
	}
	return vr
}

func (e *envStore) Each(fn func(name string, vr expand.Variable) bool) {
	if e == nil {
		return
	}
	for name, vr := range e.vars {
		if !fn(name, vr) {
			return
		}
	}
}

func (e *envStore) set(name string, vr expand.Variable) {
	if e == nil {
		return
	}
	if e.vars == nil {
		e.vars = make(map[string]expand.Variable)
	}
	e.vars[normalizeEnvName(name)] = vr
}

func (e *envStore) setExportedString(name, value string) {
	e.set(name, expand.Variable{
		Set:      true,
		Exported: true,
		Kind:     expand.String,
		Str:      value,
	})
}

func (e *envStore) replace(vars map[string]expand.Variable) {
	if e == nil {
		return
	}
	next := make(map[string]expand.Variable, len(vars))
	for name, vr := range vars {
		next[normalizeEnvName(name)] = vr
	}
	e.vars = next
}
