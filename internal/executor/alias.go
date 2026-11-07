package executor

import (
	"context"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/tfcace/hash/internal/shparse"
	"github.com/tfcace/hash/internal/trace"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

// hash keeps an alias as a function, `name() { value "$@"; }`: the
// interpreter stores aliases but expands none in the lines it parses
// later, each parse being on its own, and the function works in the zsh
// dialect and with bash-only syntax (&&) too. The functions are the
// truth; aliasTable only remembers each one's value, for alias to list.
//
// bash expands no alias again inside its own expansion, so aliases that
// name each other end (alias ll='ls -d'; alias ls=ll runs ls -d), where
// functions that call each other never do. Each alias's function says, in
// expandingVar, that it is being expanded, for as long as its call runs
// and to everything it calls; a call of a name in there runs the command
// of that name (expandingAlias).

// expandingVar is the local variable an alias's function adds its name to:
// the aliases being expanded where a call is made, each after a space.
const expandingVar = "__hash_expanding"

// expandingAlias reports whether the call of name in ctx is made inside
// the expansion of the alias name, where it means the command.
func (e *Executor) expandingAlias(ctx context.Context, name string) bool {
	expanding := interp.HandlerCtx(ctx).Env.Get(expandingVar)
	if !expanding.IsSet() || !slices.Contains(strings.Fields(expanding.String()), name) {
		return false
	}
	_, isFunc := e.callerRunner(ctx).Funcs[name]
	return isFunc
}

// aliasTable is the aliases the shells defined: the value each was given,
// by its name and the body of the function it became (aliasDef). A
// shell's function is its alias while its body is that one: a function of
// the name defined since, or unset, is no alias. A subshell's alias of a
// name is another body, kept beside its shell's.
type aliasTable struct {
	mu   sync.Mutex
	defs map[aliasDef]string
}

// aliasDef is an alias's name and its function's body, as printed.
type aliasDef struct{ name, body string }

// define records name, given value, its function's body body.
func (t *aliasTable) define(name, value, body string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.defs == nil {
		t.defs = make(map[aliasDef]string)
	}
	t.defs[aliasDef{name: name, body: body}] = value
}

// of is the aliases of the shell r, by name: the ones whose function r
// has, with its body.
func (t *aliasTable) of(r *interp.Runner) map[string]string {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make(map[string]string)
	for def, value := range t.defs {
		if fn := r.Funcs[def.name]; fn != nil && printed(fn) == def.body {
			out[def.name] = value
		}
	}
	return out
}

// printed is node as the printer prints it.
func printed(node syntax.Node) string {
	var b strings.Builder
	_ = syntax.NewPrinter().Print(&b, node)
	return b.String()
}

// aliasCall is bash's alias: it lists the calling shell's aliases (all
// of them, by name, for none or -p) as `alias NAME='value'`, and defines
// the ones its NAME=value arguments give as functions (aliasFunction).
// A name that is no alias says so, and the call fails with 1. It returns
// the call that defines them and carries the status.
func (e *Executor) aliasCall(ctx context.Context, args []string) []string {
	trace.Emit("compat", "alias_intercept", trace.LevelVerbose, map[string]any{"args": args})
	hc := interp.HandlerCtx(ctx)
	caller := e.callerRunner(ctx)
	aliases := e.aliases.of(caller)
	if len(args) > 0 && args[0] == "-p" {
		args = args[1:]
	}
	if len(args) == 0 {
		listAliases(hc.Stdout, aliases, nil)
	}
	var defs []string
	failed := false
	for _, arg := range args {
		name, value, isDef := strings.Cut(arg, "=")
		if !isDef {
			if _, ok := aliases[name]; !ok {
				fmt.Fprintf(hc.Stderr, "hash: alias: %s: not found\n", name)
				failed = true
				continue
			}
			listAliases(hc.Stdout, aliases, []string{name})
			continue
		}
		if text, ok := e.aliasFunction(ctx, caller, name, value); ok {
			defs = append(defs, text)
		}
	}
	if failed {
		defs = append(defs, "false")
	}
	if len(defs) == 0 {
		return []string{":"}
	}
	return []string{"eval", strings.Join(defs, "\n")}
}

// listAliases writes the aliases names name (all of them, by name, for
// none) to w as bash's alias lists them.
func listAliases(w io.Writer, aliases map[string]string, names []string) {
	if names == nil {
		names = slices.Sorted(maps.Keys(aliases))
	}
	for _, name := range names {
		fmt.Fprintf(w, "alias %s=%s\n", name, shellQuote(aliases[name]))
	}
}

// aliasFunction defines the alias name, given value, as the function
// `name() { value "$@"; }`, its name among the aliases being expanded
// while it runs (expandingVar), in the calling shell caller, and records it
// (aliasTable): the text that defines it, for the call to eval, or "" with
// ok false when it ran here (a program bash cannot express) or value does
// not parse, which defines nothing.
func (e *Executor) aliasFunction(ctx context.Context, caller *interp.Runner, name, value string) (text string, ok bool) {
	// Strip matching outer quotes from value if present.
	if len(value) >= 2 && (value[0] == '\'' || value[0] == '"') && value[len(value)-1] == value[0] {
		value = value[1 : len(value)-1]
	}
	trace.Emit("compat", "alias_to_function", trace.LevelVerbose, map[string]any{
		"name": name, "value": truncateForTrace(value, 100),
	})
	text = fmt.Sprintf("%s() { local %s=\"${%s-} \"%s; %s \"$@\"; }", name, expandingVar, expandingVar, shellQuote(name), value)
	prog, err := shparse.Parse(e.newParser(), text, "alias-func")
	if err != nil {
		trace.Emit("compat", "alias_func_fail", trace.LevelVerbose, map[string]any{
			"name": name, "value": truncateForTrace(value, 100), "error": err.Error(),
		})
		return "", false
	}
	callsAsCommand(prog, name)
	e.prepare(prog)
	if len(prog.Stmts) == 1 {
		if fn, isFunc := prog.Stmts[0].Cmd.(*syntax.FuncDecl); isFunc {
			e.aliases.define(name, value, printed(fn.Body))
		}
	}
	if text, ok := e.bashText(prog); ok {
		return text, true
	}
	runNested(ctx, caller, prog, "alias-func")
	return "", false
}

// callsAsCommand makes each call in prog, an alias's function, of name,
// the alias's own name (bare, escaped as \name or quoted), run the command
// of that name: `command name ...`. bash does not expand an alias again
// in its own text (alias ls='ls -G' runs ls), where the function would
// call itself forever.
func callsAsCommand(prog *syntax.File, name string) {
	syntax.Walk(prog, func(node syntax.Node) bool {
		if call, ok := node.(*syntax.CallExpr); ok && len(call.Args) > 0 && unescapedWord(call.Args[0]) == name {
			command := &syntax.Word{Parts: []syntax.WordPart{&syntax.Lit{Value: "command"}}}
			call.Args = append([]*syntax.Word{command}, call.Args...)
		}
		return true
	})
}

// unescapedWord is the text of w, a word of literals and quoted literals,
// its quotes and escaping backslashes taken out; "" for any other word.
func unescapedWord(w *syntax.Word) string {
	var b strings.Builder
	for _, part := range w.Parts {
		switch p := part.(type) {
		case *syntax.Lit:
			for i := 0; i < len(p.Value); i++ {
				if p.Value[i] == '\\' && i+1 < len(p.Value) {
					i++
				}
				b.WriteByte(p.Value[i])
			}
		case *syntax.SglQuoted:
			b.WriteString(p.Value)
		case *syntax.DblQuoted:
			for _, inner := range p.Parts {
				lit, ok := inner.(*syntax.Lit)
				if !ok {
					return ""
				}
				b.WriteString(lit.Value)
			}
		default:
			return ""
		}
	}
	return b.String()
}

// unaliasCall is bash's unalias: it takes the aliases it names (every one
// for -a) out of the calling shell, their functions with them. A name
// that is no alias says so, and the call fails with 1.
func (e *Executor) unaliasCall(ctx context.Context, args []string) []string {
	hc := interp.HandlerCtx(ctx)
	aliases := e.aliases.of(e.callerRunner(ctx))
	names := args
	if len(args) > 0 && args[0] == "-a" {
		names = slices.Sorted(maps.Keys(aliases))
	}
	text, failed := "", false
	for _, name := range names {
		if _, ok := aliases[name]; !ok {
			fmt.Fprintf(hc.Stderr, "hash: unalias: %s: not found\n", name)
			failed = true
			continue
		}
		text += "unset -f " + shellQuote(name) + "\n"
	}
	if failed {
		text += "false"
	}
	if text == "" {
		return []string{":"}
	}
	return []string{"eval", text}
}
