package completion

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/tfcace/hash/internal/shlex"
	"mvdan.cc/sh/v3/syntax"
)

// The check holds a completed line to the line with a placeholder written
// for the place: a text that escapes wrongly (or not at all) loses its item
// instead of adding a command, a redirection, a substitution, a word, or
// ending a comment or heredoc body.
func TestInsertionCheck_HoldsOnlyWhatMeansTheValue(t *testing.T) {
	code := codePlace{}.reads()
	tests := []struct {
		before, after string
		reads         []role
		text, meaning string
		want          bool
	}{
		{"probe ", "", code, `a\;id`, "a;id", true},
		{"probe ", "", code, "a;id", "a;id", false},     // a new command
		{"probe ", "", code, "a b", "a b", false},       // a new word
		{"probe ", "", code, "x>y", "x>y", false},       // a redirection
		{"probe ", "", code, "$(id)", "$(id)", false},   // a substitution
		{"probe ", "", code, "<(id)", "<(id)", false},   // a process substitution
		{"probe ", "", code, "`id`", "`id`", false},     // backticks
		{"probe ", "", code, "*", "*", false},           // a glob
		{"probe ", "", code, "{a,b}", "{a,b}", false},   // braces
		{"probe ", "", code, "~root", "~root", false},   // a home directory
		{"probe ", "", code, "$HOME", "$HOME", false},   // an expansion
		{"echo $(( 1 + ", " ))", code, "x", "x", false}, // arithmetic reads no literal
		{"echo ${x:1", "}", code, " ", " ", false},
		{"echo $(( $(probe ", ") ))", code, `a\ b`, "a b", true},
		{"probe $", "", code, "HOME", "HOME", false}, // a parameter with the $ typed before it
		{"probe ${", "}", code, "HOME", "HOME", false},
		{"probe ", "", code, "a #x", "a #x", false},                          // a comment
		{"probe ", " q", code, "'a", "a", false},                             // a quote that takes the rest
		{"probe ", "; q", code, "\"a\"", "a", true},                          // quotes are fine
		{"x=", "", code, `a\ b`, "a b", false},                               // hash keeps the backslash
		{"x=", "", code, "'a b'", "a b", true},                               //
		{"cat <<EOF\n", "\nEOF\n", bodyPlace{}.reads(), "EOF", "EOF", false}, // ends the body
		{"cat <<EOF\n", "\nEOF\n", bodyPlace{}.reads(), `\$x`, "$x", true},
		{"cat <<EOF\n", "\nEOF\n", bodyPlace{}.reads(), "$x", "$x", false},
		{"cat <<'EOF'\n", "\nEOF\n", bodyPlace{body: shlex.Body{Literal: true}}.reads(), "$x", "$x", true},
		{"probe q # ", "", commentPlace{}.reads(), "a", "a", true},
		{"probe q # ", "\n: ok", commentPlace{}.reads(), "a\nid", "a\nid", false}, // ends the comment
		{"cat <<EOF\n$()EOF\n", "\nEOF\n", bodyPlace{}.reads(), "x", "x", false},  // hash's parser ended the body
		{"echo ) ", "", code, "x", "x", false},                                    // the line does not parse
		{"[[ ", " ]]", code, `a\ b`, "a b", false},                                // hash keeps the backslash of a test's word
		{"[[ ", " ]]", code, "'a b'", "a b", true},
		{"[[ ( ", " ) ]]", code, `a\ b`, "a b", false},
		{"eval probe ", "", code, `a\;id`, "a;id", false}, // eval reads it again: a;id
		{"eval probe ", "", code, "src/a.go", "src/a.go", true},
		{`sh -c "probe `, `"`, code, `a\$\(id\)`, "a$(id)", false},
		{"diff <(sort a.txt) ", "", code, "plain.txt", "plain.txt", true}, // a process substitution before the place
		{"diff <(sort a.txt) ", "", code, "a;id", "a;id", false},
		{"diff <(sort ", ") b.txt", code, "plain.txt", "plain.txt", true}, // and around it
		{"tee >(wc -l) ", "", code, `a\ b`, "a b", true},
		{"eval <(id) ", "", code, `a\;id`, "a;id", false}, // eval still reads it again
	}
	for _, tt := range tests {
		line := tt.before + tt.after
		from := len(tt.before)
		check := readPlace(syntax.LangBash, line, from, from, placeholder).forValues(placeholder, tt.reads)
		if got, why := check.holds(tt.text, tt.meaning); got != tt.want {
			t.Errorf("%q + %q + %q: holds = %v (%s), want %v", tt.before, tt.text, tt.after, got, why, tt.want)
		}
	}
}

// Shell text (env's $NAME) is held to a placeholder name in the same place:
// it may expand, as one variable and nothing else.
func TestInsertionCheck_HoldsShellTextAsOneExpansion(t *testing.T) {
	tests := []struct {
		before, after, sigil, text string
		want                       bool
	}{
		{`echo "`, `"`, "$", "$HOME", true},
		{"echo ", "", "$", "$HOME", true},
		{"echo ${", "}", "", "HOME", true},
		{"echo ", "", "$", "$HOME;id", false},
		{"echo ", "", "$", "$(id)", false},
	}
	for _, tt := range tests {
		line := tt.before + tt.after
		from := len(tt.before)
		check := newRefCheck(syntax.LangBash, line, from, from, tt.sigil+placeholder)
		if got, why := check.holds(tt.text, ""); got != tt.want {
			t.Errorf("%q + %q + %q: holds = %v (%s), want %v", tt.before, tt.text, tt.after, got, why, tt.want)
		}
	}
}

// mvdan's expand panics on a pattern that is not UTF-8 (${0#$'\\x80'}); the check, which
// runs outside any completer's guard, reads that as a failed expansion.
func TestInsertionCheck_SurvivesAnExpansionThatPanics(t *testing.T) {
	line := "probe ${0#$'\\x80'}"
	check := readPlace(syntax.LangBash, line, len(line), len(line), placeholder).forValues(placeholder, codePlace{}.reads())
	if got, _ := check.holds("x", "x"); got {
		t.Errorf("holds = true for a word whose expansion fails")
	}
}

// placeholderless is a comment that cannot hold the placeholder: no line
// to hold a value against can be written there.
type placeholderless struct{ commentPlace }

func (placeholderless) write(value string) (string, bool) {
	return value, !strings.Contains(value, placeholder)
}

// Where the placeholder cannot be written, no value is offered: there is
// nothing to read a completed line back against. A stand-in line read in
// its place (a NUL) used to parse as a comment and let every value through.
func TestWriteValues_RefusesAllWhereThePlaceholderCannotBeWritten(t *testing.T) {
	line := "probe q # "
	result := Result{Items: []Item{{Value: "a"}}, ReplaceFrom: len(line)}
	in := insertion{place: placeholderless{}}
	_, place := insertionAt(syntax.LangBash, line, len(line), len(line), "")
	got, _ := writeValues(context.Background(), result, in, place)
	if len(got.Items) != 0 {
		t.Errorf("offered %+v, want nothing", got.Items)
	}
}

// A [[ test goes on after a line that ends in || or &&, where its words
// are read as one string each: a value is quoted there.
func TestRouter_ReadsATestThatGoesOnAfterAList(t *testing.T) {
	line := "[[ -n x ||\n"
	router := NewRouter()
	router.Register(oneValue{"a b", false}, PriorityPlugin)
	got, _ := router.Complete(context.Background(), line, len(line))
	if len(got.Items) != 1 || got.Items[0].Value != "'a b'" {
		t.Errorf("%q: offered %+v, want 'a b'", line, got.Items)
	}
}

// A compound command holding the insertion is read whole, whatever comes
// before it.
func TestRouter_ReadsTheCompoundCommandOfTheInsertionWhole(t *testing.T) {
	for _, before := range []string{
		"for f in a b; do\n  echo \"$f\"\ndone\nprobe ",
		"case $x in\n  a) probe ",
		"while true; do\n  probe ",
		"ls; if true; then probe ",
		"ls\nls; probe ",
	} {
		router := NewRouter()
		router.Register(oneValue{"a b", false}, PriorityPlugin)
		got, _ := router.Complete(context.Background(), before, len(before))
		if len(got.Items) != 1 || got.Items[0].Value != `a\ b` {
			t.Errorf("%q: offered %+v, want a\\ b", before, got.Items)
		}
	}
}

// Where hash's parser does not read the line before the insertion, the
// statement the insertion is in is not known: nothing is offered.
func TestRouter_OffersNothingAfterASyntaxError(t *testing.T) {
	for _, before := range []string{"ls )\nprobe ", "ls )\nif true; then probe "} {
		router := NewRouter()
		router.Register(oneValue{"a b", false}, PriorityPlugin)
		if got, _ := router.Complete(context.Background(), before, len(before)); len(got.Items) != 0 {
			t.Errorf("%q: offered %+v, want nothing", before, got.Items)
		}
	}
}

// A comment inside backticks ends at the closing backtick: the rest of
// the line after it is code, where an item is offered.
func TestRouter_EndsACommentInBackticksAtTheClosingBacktick(t *testing.T) {
	for _, before := range []string{"echo `echo a # c` ", "echo `echo a # c`; cat ", "echo \"`echo a # c`\" "} {
		router := NewRouter()
		router.Register(oneValue{"a b", false}, PriorityPlugin)
		got, _ := router.Complete(context.Background(), before, len(before))
		if len(got.Items) != 1 || got.Items[0].Value != `a\ b` {
			t.Errorf("%q: offered %+v (%s), want a\\ b", before, got.Items, got.Outcome())
		}
	}
	// inside the backticks after the #, or after backticks closed before
	// the #, the place is the comment (which eval does not read)
	for _, line := range []string{"echo `echo a # c `", "eval `x` # c "} {
		before := strings.TrimSuffix(line, "`")
		router := NewRouter()
		router.Register(oneValue{"a b", false}, PriorityPlugin)
		if got, _ := router.Complete(context.Background(), line, len(before)); len(got.Items) != 1 {
			t.Errorf("%q: offered %+v (%s), want one item in the comment", line, got.Items, got.Outcome())
		}
	}
}

// hash's parser recovers `case (` with a case item that has no pattern,
// whose Pos panics: the check refuses the line instead of taking the shell
// down (the router writes items on the caller's goroutine).
func TestRouter_SurvivesARecoveredNodeWithoutAPosition(t *testing.T) {
	line := "case ("
	router := NewRouter()
	router.Register(oneValue{"0", true}, PriorityPlugin)
	got, err := router.Complete(context.Background(), line, len("case "))
	if err != nil || len(got.Items) > 1 {
		t.Errorf("Complete(%q) = %+v, %v", line, got.Items, err)
	}
}

// FuzzScopedReadingAgreesWithTheWholeLine: a completed line read in the
// statements of its insertion (readReference) is held, or not, as when it
// is read whole: what comes before and after them cannot change it. The
// text held is the one the place writes for the value, and the value as it
// is (as a writer that does not escape would write it).
func FuzzScopedReadingAgreesWithTheWholeLine(f *testing.F) {
	for i, s := range spots {
		f.Add(s.before, s.after+s.closes, readBackValues[i%len(readBackValues)], s.wordStart, s.zsh)
	}
	for _, seed := range [][2]string{
		{"ls\nls; probe ", "\n: x"},
		{"probe ", "\n: x; y"},
		{"cat <<EOF; probe ", "\nbody\nEOF\nls"},
		{"cat <<EOF\nx\nEOF\nls ", ""},
		{"ls )\nprobe ", ""},
		{"echo a &\n eval ", ""},
		{"ls &&\n ; ls\nprobe ", ""},
		{"{ ls; }\nprobe ", "\n{ ls"},
		{"x=(a\n", ")\nls"},
		{"ls # c\nprobe ", " # d\nls"},
		{"f() {\n  ls ", "\n}\nls\nls"},
		{"probe ", " \\\nls\nls"},
		{"time\nls ", "\nls"},
		{"case\n", " in a) ;; esac\nls"},
	} {
		f.Add(seed[0], seed[1], "a\\", false, false)
		f.Add(seed[0], seed[1], "a b;c", false, true)
	}
	f.Add("0&\x00", "0", "!", false, true) // zsh's &! across a NUL, when the scope started after the &
	f.Fuzz(func(t *testing.T, before, after, value string, wordStart, zsh bool) {
		if len(before)+len(after) > 300 || value == "" {
			return
		}
		lang := syntax.LangBash
		if zsh {
			lang = syntax.LangZsh
		}
		line, from := before+after, len(before)
		in := lexedInsertion(line, from, from, "")
		if wordStart {
			from = parseCommandWords(line, from).currentStart
			in = lexedInsertion(line, from, len(before), "")
		}
		pos := len(before)
		ref, ok := in.write(placeholder)
		if !ok {
			return
		}
		scoped, err := readReference(lang, line, from, pos, ref)
		if err != nil {
			return
		}
		whole, _, _ := readWhole(lang, line, from, pos, ref)
		if whole.ref, err = whole.read(ref); err != nil {
			t.Fatalf("%q|%q: reads in its scope (%q), not whole: %v", before, after, scoped.line, err)
		}
		if a, b := describeRead(scoped.ref, from-scoped.from), describeRead(whole.ref, 0); a != b {
			t.Fatalf("%q|%q: the reference reads %s in its scope (%q), %s whole", before, after, a, scoped.line, b)
		}
		reads := in.place.readAs(scoped.ref.target.role).reads()
		for _, text := range []string{value, writtenOr(in, value)} {
			a, whyA := scoped.forValues(ref, reads).holds(text, value)
			b, whyB := whole.forValues(ref, reads).holds(text, value)
			if a != b {
				t.Fatalf("%q|%q, %q: held %v (%s) in its scope (%q), %v (%s) whole", before, after, text, a, whyA, scoped.line, b, whyB)
			}
		}
	})
}

// describeRead describes r, read from start in a line.
func describeRead(r *readLine, start int) string {
	target := r.target
	for _, p := range []*mappedPos{&target.from, &target.to} {
		if p.zone == zoneBefore {
			p.off += start
		}
	}
	words := r.expansion()
	return fmt.Sprintf("%s reread=%v foreign=%v words=%d%q", target, r.reread, r.foreign(), words.state, words.fields)
}

// writtenOr is value written for in, or value where in cannot write it.
func writtenOr(in insertion, value string) string {
	if text, ok := in.write(value); ok {
		return text
	}
	return value
}

// countdown is a context whose deadline passes after n calls to Err.
type countdown struct {
	context.Context
	n int
}

func (c *countdown) Err() error {
	if c.n <= 0 {
		return context.DeadlineExceeded
	}
	c.n--
	return nil
}

// values completes any line with its values, replacing from the cursor.
type values []string

func (values) Name() string { return "values" }

func (v values) Complete(_ context.Context, _ string, pos int) (Result, error) {
	items := make([]Item, len(v))
	for i, value := range v {
		items[i] = Item{Value: value}
	}
	return Result{Items: items, ReplaceFrom: pos}, nil
}

// Each item of a menu is read back in its statement, which may be long (a
// command continued over a thousand lines takes a menu tens of
// milliseconds): items are checked only until the completion deadline. The
// rest are dropped, never offered unchecked, and a menu the deadline cut
// before any item was checked is cut short, not empty.
func TestRouter_ChecksItemsOnlyUntilTheDeadline(t *testing.T) {
	line := "docker run \\\n" + strings.Repeat("  --env FOO=bar \\\n", 50) + "  -v "
	router := NewRouter()
	router.Register(values{"a b", "c d", "e f", "g h"}, PriorityPlugin)
	// one Err call is the router's, before it asks the completer
	got, err := router.Complete(&countdown{Context: context.Background(), n: 3}, line, len(line))
	if err != nil || len(got.Items) != 2 || got.Items[0].Value != `a\ b` || got.Items[1].Value != `c\ d` {
		t.Errorf("with time for two items: Complete = %+v, %v, want a\\ b and c\\ d", got.Items, err)
	}
	got, err = router.Complete(&countdown{Context: context.Background(), n: 1}, line, len(line))
	if !errors.Is(err, context.DeadlineExceeded) || len(got.Items) != 0 {
		t.Errorf("with no time for any item: Complete = %+v, %v, want the deadline", got.Items, err)
	}
}

// A menu says what it left out: the items the read-back check refused, and
// that the deadline cut the rest. A lone survivor is not the only match.
func TestRouter_SaysWhatAMenuLeftOut(t *testing.T) {
	router := NewRouter()
	router.Register(values{"My Docs/", "README.md", "a;b"}, PriorityPlugin)
	got, err := router.Complete(context.Background(), "eval cat ", len("eval cat "))
	if err != nil || len(got.Items) != 1 || got.Hidden() != (Hidden{Withheld: 2}) {
		t.Errorf("eval cat: items %+v hidden %+v (err %v), want README.md with 2 withheld", got.Items, got.Hidden(), err)
	}

	line := "docker run \\\n" + strings.Repeat("  --env FOO=bar \\\n", 50) + "  -v "
	router = NewRouter()
	router.Register(values{"a b", "c d", "e f"}, PriorityPlugin)
	got, err = router.Complete(&countdown{Context: context.Background(), n: 2}, line, len(line))
	if err != nil || len(got.Items) != 1 || got.Hidden() != (Hidden{More: 2}) {
		t.Errorf("deadline after one item: items %+v hidden %+v (err %v), want one with 2 more not shown", got.Items, got.Hidden(), err)
	}

	got, _ = routerWith(values{"a", "b"}).Complete(context.Background(), "cat ", len("cat "))
	if got.Hidden().Any() {
		t.Errorf("cat: hidden %+v, want nothing left out", got.Hidden())
	}
}

// routerWith is a router with one plugin-priority completer.
func routerWith(c Completer) *Router {
	r := NewRouter()
	r.Register(c, PriorityPlugin)
	return r
}

// When the deadline passes after the check refused every item it got to,
// the menu was withheld (no safe matches), not cut short by the deadline.
func TestRouter_SaysWithheldWhenTheDeadlinePassesAfterOnlyRefusals(t *testing.T) {
	router := routerWith(values{"a;pwn", "b c", "d e"})
	got, err := router.Complete(&countdown{Context: context.Background(), n: 3}, "eval probe ", len("eval probe "))
	if err != nil || len(got.Items) != 0 || got.Outcome() != OutcomeWithheld || got.Hidden() != (Hidden{Withheld: 2, More: 1}) {
		t.Errorf("Complete = %+v, %v, outcome %s, hidden %+v, want withheld (2, 1 more)", got.Items, err, got.Outcome(), got.Hidden())
	}
}
