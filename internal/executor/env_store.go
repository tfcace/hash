package executor

import (
	"os"
	"runtime"
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
	return &envStore{vars: vars}
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
