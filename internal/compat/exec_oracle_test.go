package compat

// An executing oracle for the rc filter. A generator writes zsh rc files out
// of marker commands (mN) and the constructs and commands the filter skips.
// Each marker is classified as it is written:
//
//	kept:      not inside any skipped statement
//	inside:    inside a skipped statement (a skipped command, a compound
//	           whose test is skipped, the right of && || | after a skipped
//	           left, a test that calls a function whose last command is
//	           skipped, the rest of a function body after a skipped return
//	           guard, everything after a skipped guard nested in a compound
//	           up to the end of its function or the file, and a loop whose
//	           body holds one; a command after ; on a skipped command's line
//	           is a statement of its own, so it is kept)
//	dependent: in a branch after a skipped elif or case arm pattern (later
//	           elifs, else, later arms), which run only by the skipped test
//	after:     after a skipped return guard, up to the end of its function
//	           or the file (and in a loop holding one): the filter returns
//	           where zsh may, so it runs them only where zsh does
//
// Checks per generated file (bash target):
//
//	A  the filtered output, run by the interpreter, never runs an inside marker
//	A2 nor a dependent marker
//	B  the set of kept markers it runs equals the set real zsh (zsh -f,
//	   isolated HOME and ZDOTDIR) runs for the original
//	R  every after marker it runs, zsh runs
//
// and, logged only, L: the after markers zsh runs and it does not, the
// code a return guard costs.
//
// A file filtered whole must pass all three. One the filter stops in (the
// zsh parser rejects it) loads only up to the stop, so it runs fewer kept
// markers than zsh does: it must pass A, A2 and R, and B is only counted.
import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/interp"
)

type mclass int

const (
	mKept mclass = iota
	mInside
	mDependent
	mAfter
)

type gen struct {
	r        *rand.Rand
	lines    []string
	n        int
	class    map[int]mclass
	inside   int
	depend   int
	fnCount  int
	verbatim int
	maxDepth int
	prelude  []string // functions used as tests, defined before the rest
	fz       int
	// stopped: a skipped exit guard nested in a compound came before, so
	// the rest of the function or file may not run
	stopped int
	// returned: a skipped return guard came before: the rest of the
	// function or file runs only where zsh did not return
	returned int
	// exits: the function being written holds a skipped exit guard,
	// itself or in a function it defines (and calls): a call to it may
	// end the shell
	exits bool
}

func (g *gen) skipOK() bool { return g.verbatim == 0 }

func (g *gen) emit(s string) { g.lines = append(g.lines, s) }

func (g *gen) marker() string {
	g.n++
	c := mKept
	switch {
	case g.inside > 0 || g.stopped > 0:
		c = mInside
	case g.depend > 0:
		c = mDependent
	case g.returned > 0:
		c = mAfter
	}
	g.class[g.n] = c
	return "m" + strconv.Itoa(g.n)
}

func (g *gen) pick(n int) int { return g.r.Intn(n) }

var skipSimple = []string{
	"setopt autocd", "bindkey -v", "zstyle ':x' y z", "autoload -Uz compinit",
	"echo ${(%):-x}", "alias -g GG=x", "typeset -U path", "zmodload zsh/datetime",
	"print -l ${(f)x}", "unsetopt beep", "compdef _git g",
}

// simple returns a complete one-line statement; skipped when the line is skipped.
func (g *gen) simple() string {
	switch g.pick(8) {
	case 0, 1, 2:
		return g.marker()
	case 3:
		a := g.marker()
		return a + "; " + g.marker()
	case 4:
		a := g.marker()
		return a + " && " + g.marker()
	case 5:
		a := g.marker()
		return a + " || " + g.marker()
	case 6:
		return []string{"true", "false", ":"}[g.pick(3)]
	}
	if !g.skipOK() {
		return g.marker()
	}
	// a skipped command; after && or || the marker is skipped with it,
	// after ; it is a command of its own
	s := skipSimple[g.pick(len(skipSimple))]
	if g.pick(4) == 0 {
		s = "! " + negSkips[g.pick(len(negSkips))]
	}
	if g.pick(2) == 0 {
		return s
	}
	if g.pick(3) == 0 {
		return s + "; " + g.marker()
	}
	g.inside++
	m := g.marker()
	g.inside--
	return s + []string{" && ", " || "}[g.pick(2)] + m
}

var skipConds = []string{
	"[[ -n ${(%):-x} ]]", "[[ -z ${(%):-x} ]]", "[[ $TERM == (#i)XTERM* ]]", "[[ $TERM == (#i)dumb ]]",
	"setopt autocd", "bindkey -v",
}

// cond returns a condition and whether its line will be skipped.
func (g *gen) cond(allowSkip bool) (string, bool) {
	if allowSkip && g.skipOK() && g.pick(3) == 0 {
		return skipConds[g.pick(len(skipConds))], true
	}
	switch g.pick(4) {
	case 0:
		return "true", false
	case 1:
		return "false", false
	}
	return g.marker(), false
}

// condInHeader: some skip conds only skip the line as the first word (setopt);
// inside "if setopt x" the line is not skipped, so only use ${( or (# ones.
func (g *gen) headerCond() (string, bool) {
	if g.skipOK() && g.pick(3) == 0 {
		return skipConds[g.pick(4)], true
	}
	c, _ := g.cond(false)
	return c, false
}

// tstatus is what the filter makes of a test: kept, skipped (":"), or
// unknown (it runs, but a skip in it decides its status).
type tstatus int

const (
	tKept tstatus = iota
	tSkipped
	tUnknown
)

// within returns the status of a compound whose last command has status s.
func (s tstatus) within() tstatus {
	if s == tSkipped {
		return tUnknown
	}
	return s
}

// testSkips are skips that hold wherever the command is.
var testSkips = []string{"[[ -n ${(%):-x} ]]", "[[ -z ${(%):-x} ]]", "[[ $TERM == (#i)XTERM* ]]", "setopt autocd"}

// negSkips are commands the text rules skip, wherever they are.
var negSkips = []string{"setopt autocd", "zmodload zsh/datetime 2>/dev/null", "autoload -Uz compinit", "bindkey -v", "compdef _git g"}

// test returns a command whose status decides what runs next, and what the
// filter makes of it. Its markers are classified as they are written, left
// to right: one after a test that is not kept, which decides whether it
// runs, is inside.
func (g *gen) test(depth int) (string, tstatus) {
	if !g.skipOK() || depth > 2 {
		return g.marker(), tKept
	}
	switch g.pick(13) {
	case 9: // negated skip
		return "! " + []string{"[[ -n ${(%):-x} ]]", "[[ $TERM == (#i)XTERM* ]]", "{ true; [[ -z ${(%):-x} ]]; }"}[g.pick(3)], tSkipped
	case 11: // a negated command a text rule skips
		return "! " + negSkips[g.pick(len(negSkips))], tSkipped
	case 10: // a function whose last command is skipped, used as a test: its status is unknown
		g.fz++
		name := fmt.Sprintf("fz_%d", g.fz)
		body := testSkips[g.pick(len(testSkips))]
		if g.pick(2) == 0 {
			body = "true; " + body
		}
		g.prelude = append(g.prelude, name+"() { "+body+"; }")
		if g.pick(2) == 0 {
			return "! " + name, tUnknown
		}
		return name, tUnknown
	case 12: // negated compound
		x, xs := g.test(depth + 1)
		return "! { " + x + "; }", xs.within()
	case 0, 1:
		return g.marker(), tKept
	case 2:
		return []string{"true", "false"}[g.pick(2)], tKept
	case 3:
		return testSkips[g.pick(len(testSkips))], tSkipped
	case 4, 5: // a && b, a || b, a | b: b runs as a says
		x, xs := g.test(depth + 1)
		op := []string{" && ", " || ", " | "}[g.pick(3)]
		if xs != tKept {
			g.inside++
			defer func() { g.inside-- }()
		}
		y, ys := g.test(depth + 1)
		if xs != tKept {
			return x + op + y, xs
		}
		return x + op + y, ys.within()
	case 6: // { a; b } and ( a; b ): b's status
		var parts []string
		var last tstatus
		for k := 1 + g.pick(2); k > 0; k-- {
			var p string
			p, last = g.test(depth + 1)
			parts = append(parts, p)
		}
		switch g.pick(3) {
		case 0:
			return "{ " + strings.Join(parts, "; ") + " }", last.within()
		case 1:
			return "{ " + strings.Join(parts, "; ") + "; }", last.within()
		}
		return "( " + strings.Join(parts, "; ") + " )", last.within()
	case 7:
		return `[[ -n "$(echo ${(%):-x})" ]]`, tUnknown
	}
	return "! " + g.marker(), tKept
}

// header returns an if or while test; when it is not kept, the compound is
// skipped whole, its test's markers too.
func (g *gen) header() (string, bool) {
	if g.pick(2) == 0 {
		return g.headerCond()
	}
	n := g.n
	c, st := g.test(0)
	if st == tKept {
		return c, false
	}
	for i := n + 1; i <= g.n; i++ {
		g.class[i] = mInside
	}
	return c, true
}

// chain writes a test and a marker after it, which runs as the test says.
func (g *gen) chain() {
	c, st := g.test(0)
	if st != tKept {
		g.inside++
		defer func() { g.inside-- }()
	}
	g.emit(c + []string{" && ", " || "}[g.pick(2)] + g.marker())
}

func (g *gen) block(depth int) {
	for k := 1 + g.pick(2); k > 0; k-- {
		g.stmt(depth)
		if depth > 0 && g.skipOK() && g.pick(6) == 0 {
			// a skipped guard nested in a compound: what follows it, in
			// the compound and after it, may not run
			end := []string{" && return", " || return", " && exit"}[g.pick(3)]
			g.emit("  " + testSkips[g.pick(3)] + end)
			if end == " && exit" {
				g.stopped++
				g.exits = true
			} else {
				g.returned++
			}
		}
	}
}

// guardedLoop marks the markers of a loop from marker n on inside when its
// body holds a nested skipped exit guard: the filter skips such a loop
// whole, as it would run its body again where zsh left it. With a return
// guard in it, the loop's markers that are kept are after ones: the filter
// returns there.
func (g *gen) guardedLoop(n, stopped, returned int) {
	for i := n + 1; i <= g.n; i++ {
		switch {
		case g.stopped > stopped:
			g.class[i] = mInside
		case g.returned > returned && g.class[i] == mKept:
			g.class[i] = mAfter
		}
	}
}

func (g *gen) stmt(depth int) {
	if depth > g.maxDepth {
		g.emit(g.simple())
		return
	}
	switch g.pick(18) {
	case 16:
		g.chain()
	case 17:
		g.fallthroughCase(depth)
	case 0, 1, 2:
		g.emit(g.simple())
	case 3:
		g.ifStmt(depth)
	case 4:
		g.loop(depth)
	case 5:
		g.forStmt(depth)
	case 6:
		g.caseStmt(depth)
	case 7:
		g.braceGuard(depth)
	case 8:
		g.continuation()
	case 9:
		g.function(depth)
	case 10:
		g.emit("(")
		g.verbatim++
		g.block(depth + 1)
		g.verbatim--
		g.emit(")")
	case 11:
		g.emit("x=$(")
		g.verbatim++
		g.block(depth + 1)
		g.verbatim--
		g.emit(")")
	case 12:
		g.heredoc(depth)
	case 13:
		g.multiQuote()
	case 14:
		g.backslash()
	case 15:
		g.emit("")
		g.emit("# comment " + []string{"it's", "(#i)", "$(", "\"", "fi"}[g.pick(5)])
		g.emit(g.simple())
	}
}

func (g *gen) ifStmt(depth int) {
	c, skip := g.header()
	if skip {
		g.inside++
	}
	if g.pick(2) == 0 {
		g.emit("if " + c + "; then")
	} else {
		g.emit("if " + c)
		g.emit("then")
	}
	g.block(depth + 1)
	dep := 0
	for k := g.pick(3); k > 0; k-- {
		c2, skip2 := g.header()
		if skip2 && !skip {
			// the elif line is skipped: its body and every later branch depend on it
			g.depend++
			dep++
			g.inside++
			g.emit("elif " + c2 + "; then")
			g.block(depth + 1)
			g.inside--
			continue
		}
		g.emit("elif " + c2 + "; then")
		g.block(depth + 1)
	}
	if g.pick(2) == 0 {
		g.emit("else")
		g.block(depth + 1)
	}
	g.emit("fi")
	g.depend -= dep
	if skip {
		g.inside--
	}
}

func (g *gen) loop(depth int) {
	kw := []string{"while", "until"}[g.pick(2)]
	n, stopped, returned := g.n, g.stopped, g.returned
	defer func() { g.guardedLoop(n, stopped, returned) }()
	c, skip := g.header()
	// a skipped break skips its loop whole
	breakSkip := !skip && g.skipOK() && g.pick(3) == 0
	if skip || breakSkip {
		if kw == "until" {
			// until [[ -n x ]] would never run in zsh; keep it a valid loop
			kw = "while"
		}
		g.inside++
	}
	if breakSkip {
		for i := n + 1; i <= g.n; i++ {
			g.class[i] = mInside // the markers of the test, written before
		}
	}
	g.emit(kw + " " + c + "; do")
	g.block(depth + 1)
	if breakSkip {
		g.emit("  " + testSkips[g.pick(len(testSkips))] + " && break")
	}
	g.emit("  break")
	g.emit("done")
	if skip || breakSkip {
		g.inside--
	}
}

// fallthroughCase writes a case whose first arm falls through (;&) into an
// arm with a skipped pattern: its body runs after the first arm's, and the
// arms after it depend on the skipped pattern, unless it ends in ;|, which
// tests them either way.
func (g *gen) fallthroughCase(depth int) {
	if !g.skipOK() {
		g.emit(g.simple())
		return
	}
	g.emit("case " + []string{"a", "b"}[g.pick(2)] + " in")
	g.emit("  a)")
	g.block(depth + 1)
	g.emit("    ;&")
	g.emit("  ${(%):-a})")
	g.block(depth + 1)
	op := []string{";;", ";|"}[g.pick(2)]
	g.emit("    " + op)
	if op == ";;" {
		g.depend++
		defer func() { g.depend-- }()
	}
	g.emit("  *) " + g.marker() + " ;;")
	g.emit("esac")
}

func (g *gen) forStmt(depth int) {
	n, stopped, returned := g.n, g.stopped, g.returned
	defer func() { g.guardedLoop(n, stopped, returned) }()
	skip := g.skipOK() && g.pick(3) == 0
	if skip {
		g.inside++
		switch g.pick(3) {
		case 0:
			g.emit("for v in ${(%):-a}; do")
		case 1:
			g.emit("for v in *(N); do")
		default:
			g.emit("for v (a b); do")
		}
	} else {
		g.emit("for v in a; do")
	}
	g.block(depth + 1)
	g.emit("done")
	if skip {
		g.inside--
	}
}

func (g *gen) caseStmt(depth int) {
	skip := g.skipOK() && g.pick(3) == 0
	if skip {
		g.inside++
		g.emit([]string{"case ${(%):-a} in", "case $(echo ${(%):-a}) in"}[g.pick(2)])
	} else {
		g.emit("case a in")
	}
	dep := 0
	for k := 1 + g.pick(3); k > 0; k-- {
		var pat string
		patSkip := false
		switch g.pick(5) {
		case 0:
			pat = "a)"
		case 1:
			pat = "*)"
		case 2:
			pat = "b|c)"
		case 3:
			pat = "(a)"
		default:
			pat, patSkip = "(#i)A)", true
		}
		if patSkip && !g.skipOK() {
			pat, patSkip = "a)", false
		}
		if patSkip && !skip {
			g.inside++
			g.depend++
			dep++
		}
		op := ";;"
		if g.pick(4) == 0 {
			op = ";|" // zsh: go on testing the patterns after it
		}
		if g.pick(2) == 0 {
			g.emit("  " + pat + " " + g.simple() + " " + op)
		} else {
			g.emit("  " + pat)
			g.block(depth + 1)
			g.emit("    " + op)
		}
		if patSkip && !skip {
			g.inside--
		}
	}
	g.emit("esac")
	g.depend -= dep
	if skip {
		g.inside--
	}
}

func (g *gen) braceGuard(depth int) {
	c, skip := g.cond(true)
	if skip {
		g.inside++
	}
	g.emit(c + []string{" && {", " || {"}[g.pick(2)])
	g.block(depth + 1)
	g.emit("}")
	if skip {
		g.inside--
	}
}

func (g *gen) continuation() {
	c, skip := g.cond(true)
	if skip {
		g.inside++
	}
	g.emit(c + []string{" &&", " ||", " |"}[g.pick(3)])
	g.emit("  " + g.marker())
	if skip {
		g.inside--
	}
}

func (g *gen) function(depth int) {
	// a return guard in the body stops the body only; an exit guard (here
	// or in a function it defines) makes the rest of the file stop after
	// the definition, which the filter takes for a call when the
	// definition is skipped
	stopped, returned, exits := g.stopped, g.returned, g.exits
	g.exits = false
	defer func() {
		g.returned = returned
		if g.stopped = stopped; g.exits {
			g.stopped++
		}
		g.exits = g.exits || exits
	}()
	g.fnCount++
	name := fmt.Sprintf("f_%d", g.fnCount)
	switch g.pick(4) {
	case 0:
		if !g.skipOK() {
			g.emit("function " + name + " {")
			break
		}
		g.inside++
		g.emit("function zle-a" + strconv.Itoa(g.fnCount) + " zle-b" + strconv.Itoa(g.fnCount) + " {")
		g.block(depth + 1)
		g.emit("}")
		g.inside--
		return
	case 1:
		g.emit("function " + name + " {")
	default:
		g.emit(name + "() {")
	}
	// body markers run only if called; call it right after, unconditionally
	g.block(depth + 1)
	guard := g.skipOK() && g.pick(3) == 0
	if guard {
		// a skipped guard that may return: the rest of the body may not run
		end := []string{" && return", " && exit"}[g.pick(2)]
		g.emit("  " + testSkips[g.pick(3)] + end)
		if end == " && exit" {
			g.exits = true
			g.inside++
			g.block(depth + 1)
			g.inside--
		} else {
			g.returned++
			g.block(depth + 1)
		}
	}
	g.emit("}")
	g.emit(name)
}

func (g *gen) heredoc(depth int) {
	switch g.pick(4) {
	case 0:
		g.emit("cat <<EOF")
		g.emit("text m999 $(echo hi) it's")
		g.emit("EOF")
	case 1:
		g.emit("cat <<'EOF' && " + g.marker())
		g.emit("fi done }")
		g.emit("EOF")
	case 2:
		g.emit("y=$(cat <<EOF")
		g.emit("body \"")
		g.emit("EOF")
		g.emit(")")
		g.emit(g.marker())
	case 3:
		if !g.skipOK() {
			return
		}
		g.inside++
		g.emit("bindkey -s '^x' <<EOF && " + g.marker())
		g.emit(g.marker())
		g.emit("EOF")
		g.inside--
	}
}

func (g *gen) multiQuote() {
	if !g.skipOK() || g.pick(2) == 0 {
		g.emit("echo \"a")
		g.emit("m998 b\" && " + g.marker())
		return
	}
	g.inside++
	g.emit("zstyle ':x' \"a")
	g.emit("b\" && " + g.marker())
	g.inside--
}

func (g *gen) backslash() {
	if !g.skipOK() || g.pick(2) == 0 {
		a := g.marker()
		g.emit(a + " \\")
		g.emit("  && " + g.marker())
		return
	}
	g.inside++
	g.emit("setopt autocd \\")
	g.emit("  && " + g.marker())
	g.inside--
}

func generate(seed int64) (src string, class map[int]mclass, markers int) {
	g := &gen{r: rand.New(rand.NewSource(seed)), class: map[int]mclass{}, maxDepth: 3}
	for k := 2 + g.pick(5); k > 0; k-- {
		g.stmt(0)
		if g.inside == 0 && g.pick(12) == 0 {
			// a skipped guard that may return: nothing after it runs
			end := []string{" && return", " && exit"}[g.pick(2)]
			g.emit(testSkips[g.pick(3)] + end)
			if end == " && exit" {
				g.inside++
			} else {
				g.returned++
			}
		}
	}
	return strings.Join(append(g.prelude, g.lines...), "\n") + "\n", g.class, g.n
}

var markerRE = regexp.MustCompile(`^m([0-9]+)$`)

// markerStatus is the exit status of marker n, so that && and || go both ways.
func markerStatus(n int) uint8 { return uint8(n % 2) }

// runMarkers runs out, the filtered bash, in the interpreter, sourced as
// hash runs a startup file, and returns the markers it ran. Every other
// external command succeeds and does nothing.
func runMarkers(out string) ([]int, error) {
	if _, err := parseRC(out, "bash"); err != nil {
		return nil, err
	}
	var mu sync.Mutex
	var ran []int
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	r, err := interp.New(
		interp.StdIO(nil, io.Discard, io.Discard),
		sourcedText(out),
		interp.Env(expand.ListEnviron("TERM=xterm-256color", "HOME=/nonexistent", "PATH=/nonexistent")),
		interp.ExecHandlers(func(next interp.ExecHandlerFunc) interp.ExecHandlerFunc {
			return func(ctx context.Context, args []string) error {
				m := markerRE.FindStringSubmatch(args[0])
				if m == nil {
					return nil
				}
				n, _ := strconv.Atoi(m[1])
				mu.Lock()
				ran = append(ran, n)
				mu.Unlock()
				if s := markerStatus(n); s != 0 {
					return interp.ExitStatus(s)
				}
				return nil
			}
		}),
	)
	if err != nil {
		return nil, err
	}
	_ = r.Run(ctx, sourceCall())
	return ran, ctx.Err()
}

// runZsh runs content in real zsh, with each marker a function that logs
// it, and returns the markers run. dir is its HOME, ZDOTDIR and cwd.
func runZsh(dir, content string, markers int) ([]int, error) {
	logf := filepath.Join(dir, "log")
	os.Remove(logf)
	var pre strings.Builder
	pre.WriteString("setopt extendedglob\nunsetopt nomatch\n")
	for i := 1; i <= markers; i++ {
		fmt.Fprintf(&pre, "m%d() { print -r m%d >> %q; return %d }\n", i, i, logf, markerStatus(i))
	}
	script := filepath.Join(dir, "rc.zsh")
	if err := os.WriteFile(script, []byte(pre.String()+content), 0o644); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, zshPath, "-f", script)
	cmd.Dir = dir
	cmd.Env = []string{"HOME=" + dir, "ZDOTDIR=" + dir, "TERM=xterm-256color", "PATH=/usr/bin:/bin"}
	_ = cmd.Run()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	b, _ := os.ReadFile(logf)
	var ran []int
	for _, l := range strings.Fields(string(b)) {
		n, _ := strconv.Atoi(strings.TrimPrefix(l, "m"))
		ran = append(ran, n)
	}
	return ran, nil
}

const zshPath = "/bin/zsh"

// oracleRun is one generated file checked: the path the filter took and the
// checks it failed (A, A2, B), or "run" when the filtered file did not run.
type oracleRun struct {
	stopped bool // the filter stopped in it: only the lines before load
	failed  []string
	lost    int // L: after markers zsh ran that the filter did not
	log     string
}

// wrong reports whether the file failed a check it must pass: any, or any
// but B for a file the filter stopped in.
func (r oracleRun) wrong() bool {
	return slices.ContainsFunc(r.failed, func(f string) bool { return f != "B" || !r.stopped })
}

// checkOracle generates the file for seed, filters it and runs it; zshDir is
// real zsh's isolated home, or "" to skip check B.
func checkOracle(seed int64, zshDir string) oracleRun {
	content, class, markers := generate(seed)
	out, report := filterRC(content, "zsh", "bash", func() *Report { return NewReport("", "") })
	res := oracleRun{stopped: len(report.NotLoaded) > 0}
	ran, err := runMarkers(out)
	if err != nil {
		res.failed = []string{"run"}
		res.log = fmt.Sprintf("seed %d: filtered file did not run: %v\n--in--\n%s--out--\n%s", seed, err, content, out)
		return res
	}
	var wrong []string
	for _, n := range ran {
		switch class[n] {
		case mInside:
			res.failed = append(res.failed, "A")
			wrong = append(wrong, fmt.Sprintf("m%d", n))
		case mDependent:
			res.failed = append(res.failed, "A2")
			wrong = append(wrong, fmt.Sprintf("m%d", n))
		}
	}
	var zran []int
	if zshDir != "" {
		if zran, err = runZsh(zshDir, content, markers); err == nil {
			if !slices.Equal(kept(zran, class), kept(ran, class)) {
				res.failed = append(res.failed, "B")
			}
			after, zafter := ofClass(ran, class, mAfter), ofClass(zran, class, mAfter)
			for _, n := range after {
				if !slices.Contains(zafter, n) {
					res.failed = append(res.failed, "R")
					wrong = append(wrong, fmt.Sprintf("m%d", n))
				}
			}
			for _, n := range zafter {
				if !slices.Contains(after, n) {
					res.lost++
				}
			}
		}
	}
	slices.Sort(res.failed)
	res.failed = slices.Compact(res.failed)
	if len(res.failed) > 0 {
		res.log = fmt.Sprintf("seed %d: %v (lines %v): ran %v, skipped markers run %v, zsh kept %v\n--in--\n%s--out--\n%s",
			seed, res.failed, res.stopped, ran, wrong, kept(zran, class), content, out)
	}
	return res
}

// kept returns the kept markers of ran, sorted.
func kept(ran []int, class map[int]mclass) []int { return ofClass(ran, class, mKept) }

// ofClass returns the markers of ran of class c, sorted.
func ofClass(ran []int, class map[int]mclass, c mclass) []int {
	var out []int
	for _, n := range ran {
		if class[n] == c {
			out = append(out, n)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// zshDir returns an empty home for real zsh, or "" when there is no zsh.
func zshDir(t testing.TB) string {
	if _, err := os.Stat(zshPath); err != nil {
		t.Logf("no %s: check B skipped", zshPath)
		return ""
	}
	return t.TempDir()
}

// TestFilterRC_ExecutingOracle checks generated rc files: the tree filter
// must never run a skipped statement or a branch that depends on one, and
// must run every other marker real zsh runs. HASH_ORACLE_SEEDS runs more
// seeds (the default is a fixed few hundred).
func TestFilterRC_ExecutingOracle(t *testing.T) {
	seeds := 300
	if s := os.Getenv("HASH_ORACLE_SEEDS"); s != "" {
		seeds, _ = strconv.Atoi(s)
	}
	dir := zshDir(t)
	counts := map[string]map[string]int{"whole": {}, "stopped": {}}
	for seed := int64(1); seed <= int64(seeds); seed++ {
		res := checkOracle(seed, dir)
		path := "whole"
		if res.stopped {
			path = "stopped"
		}
		counts[path]["files"]++
		counts[path]["L"] += res.lost
		for _, f := range res.failed {
			counts[path][f]++
		}
		if res.wrong() {
			t.Error(res.log)
		}
	}
	t.Logf("filtered whole: %v; stopped: %v", counts["whole"], counts["stopped"])
	if counts["whole"]["files"] < seeds/2 {
		t.Errorf("only %d of %d files were filtered whole", counts["whole"]["files"], seeds)
	}
}

// FuzzFilterRC_ExecutingOracle is the oracle over any seed.
func FuzzFilterRC_ExecutingOracle(f *testing.F) {
	f.Add(int64(1))
	dir := zshDir(f)
	f.Fuzz(func(t *testing.T, seed int64) {
		if res := checkOracle(seed, dir); res.wrong() {
			t.Error(res.log)
		}
	})
}
