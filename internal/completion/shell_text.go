package completion

import (
	"context"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/tfcace/hash/internal/shlex"
	"github.com/tfcace/hash/internal/trace"
	"mvdan.cc/sh/v3/syntax"
)

// writeShellText turns a result's values, literal text the word should
// mean, into the shell text that replaces line[ReplaceFrom:pos] and means
// that: a value from a branch name or a file in the work tree must not
// become two words, or run a command. The Prefix is folded into each value,
// and a value the place cannot hold is left out. A result of variable
// references (env's $NAME) is written as it is. Each item's completed line
// is then read back in hash's dialect lang (insertionCheck), and one that
// does not mean what it should is left out too.
//
// Reading an item back costs a parse of its statement, which a long one (a
// command continued over a thousand lines) makes tens of milliseconds a
// menu: items are checked only until ctx's deadline. The ones left are
// dropped, never offered unchecked; when none was checked, the error is
// ctx's.
func writeShellText(ctx context.Context, result Result, line string, pos int, lang syntax.LangVariant) (Result, error) {
	if result.refs != nil {
		return writeRefs(ctx, result, line, pos, lang)
	}
	in, place := insertionAt(lang, line, result.ReplaceFrom, pos, result.Prefix)
	return writeValues(ctx, result, in, place)
}

// writeRefs keeps the variable references whose completed line reads back
// as one, held to the reference with the result's sigil and a placeholder
// name.
func writeRefs(ctx context.Context, result Result, line string, pos int, lang syntax.LangVariant) (Result, error) {
	check := newRefCheck(lang, line, result.ReplaceFrom, pos, result.refs.sigil+placeholder)
	items := make([]Item, 0, len(result.Items)) // the completer's slice may be cached
	for i, item := range result.Items {
		if ctx.Err() != nil {
			return cutShort(ctx, result, items, i)
		}
		if keep(check, item.Value, item.Display) {
			items = append(items, item)
		}
	}
	return checked(result, items, len(result.Items)), nil
}

// checked is result with the items the check kept, out of the first
// tried of its items: withheld when it kept none of them.
func checked(result Result, items []Item, tried int) Result {
	result.hidden.Withheld += tried - len(items)
	if len(items) == 0 && tried > 0 {
		result.empty = OutcomeWithheld
	}
	result.Items, result.Prefix = items, ""
	return result
}

// cutShort is result with the items checked before ctx's deadline, out of
// the first tried, the rest not shown; or ctx's error when none was tried.
// When every one tried was refused, the menu was withheld.
func cutShort(ctx context.Context, result Result, items []Item, tried int) (Result, error) {
	if tried == 0 {
		return Result{}, ctx.Err()
	}
	result.hidden.More += len(result.Items) - tried
	return checked(result, items, tried), nil
}

// writeValues writes a result's values for insertion in, holding each
// completed line to hash's parser's reading of the place.
func writeValues(ctx context.Context, result Result, in insertion, place reading) (Result, error) {
	items := make([]Item, 0, len(result.Items))
	// The reference a value is held against has the head and tail of the
	// value that change how the place writes it: a first byte a typed
	// backslash escapes, a directory's / that keeps its quote open.
	checks := map[[2]string]checker{}
	for i, item := range result.Items {
		if ctx.Err() != nil {
			return cutShort(ctx, result, items, i)
		}
		value := result.Prefix + item.Value
		text, ok := in.write(value)
		if !ok {
			continue
		}
		head, tail := result.Prefix, ""
		if in.escaped && head == "" {
			head = value[:1]
		}
		if strings.HasSuffix(value, "/") && len(value) > len(head) {
			tail = "/"
		}
		key := [2]string{head, tail}
		if checks[key] == nil {
			checks[key] = refusal("the place cannot hold the placeholder")
			if ref, ok := in.write(head + placeholder + tail); ok {
				checks[key] = place.forValues(ref, in.place.reads())
			}
		}
		if keep(checks[key], text, value[len(head):len(value)-len(tail)]) {
			item.Value = text
			items = append(items, item)
		}
	}
	return checked(result, items, len(result.Items)), nil
}

// keep reports whether check holds text, written for meaning, tracing an
// item it leaves out.
func keep(check checker, text, meaning string) bool {
	ok, why := check.holds(text, meaning)
	if !ok && trace.Enabled("completion") {
		trace.Emit("completion", "insertion_refused", trace.LevelDetailed, map[string]any{
			"meaning": meaning, "text": text, "reason": why,
		})
	}
	return ok
}

// insertion is the place a replacement goes, which decides how a value is
// written to mean itself there. The lexer reads it where the replacement
// starts (what the replaced text opens and the rest of the line closes add
// to it only when the replaced text opens a quote), and hash's parser says
// how the word there is read (insertionAt).
type insertion struct {
	place place
	// escaped: a backslash the user typed right before the replacement
	// escapes the value's first byte.
	escaped bool
	layers  layers
}

// layers are the backtick substitutions around a place: the shell reads
// their text once more for each, so the text is escaped once more for each.
type layers struct {
	backticks int
	// nested: an escaped backtick (\`) before the replacement may have
	// opened a backtick inside the backticks, which the lexer does not tell.
	// hash's parser reads the text there with one level of escaping, as one
	// backtick deep, but backslashes its own way (neither bash's nor one
	// level's), so a value written with a backslash (a\ b, $'\'') is not
	// offered. Telling the depth would mean reading every escape once per
	// level in the lexer; the textual guess only ever refuses.
	nested bool
}

// place is where in the shell's grammar a replacement goes: a word of
// code, a comment or a heredoc body.
type place interface {
	// write returns value written as shell text that means value there, or
	// false when the place cannot hold it.
	write(value string) (string, bool)
	// escapes reports whether a backslash typed right before the place
	// simply escapes the byte after it.
	escapes() bool
	// after returns the place for text that follows typed, the first byte
	// of a value that goes as it is.
	after(typed string) place
	// readAs returns the place as hash's parser reads what is written
	// there: in role r.
	readAs(r role) place
	// reads are the ways hash's parser may read what write wrote
	// (insertionCheck's roles).
	reads() []role
}

// codePlace is a word of code: inside the quote the replacement starts in,
// in a quote the replaced text opens, or outside quotes.
type codePlace struct {
	quote shlex.QuoteKind // the quote the replacement is inside (NoQuote: outside quotes)
	opens openedQuote     // the quote the replaced text opens, if any
	word  codeWord
	// tilde is a ~/ or ~name/ the user typed in front of the items: it
	// stays outside any quote, where it expands.
	tilde string
}

// openedQuote is a quote the replaced text opens ("e) outside quotes,
// which the value goes inside, and what closes it. Its kind is NoQuote
// when the replaced text opens none.
type openedQuote struct {
	kind shlex.QuoteKind
	end  quoteEnd
}

// quoteEnd is what closes an opened quote.
type quoteEnd uint8

const (
	closedByLine  quoteEnd = iota // the line closes it after the cursor
	closedByValue                 // the line does not, so the value does
	// closedAtEnd: nothing follows the cursor, so the value closes it, but
	// a directory's stays open for the user to go on into.
	closedAtEnd
)

// commentPlace is a comment, which no shell reads: a value is written as
// code would be anyway, so that where the lexer takes code for a comment
// (bash's extglob !(#x), zsh's glob flags (#i)) it still means itself.
type commentPlace struct{}

// bodyPlace is a heredoc body's line around a replacement: a value must
// not make it the delimiter, which would end the body there.
type bodyPlace struct {
	body   shlex.Body
	before string // the line's text up to the replacement
	rest   string // the line's text after the cursor
}

// codeWord is how a word of code reads a value outside quotes.
type codeWord uint8

const (
	escapingWord codeWord = iota // a backslash escapes the next byte: a\ b
	// keepingWord is a word hash's parser expands to one string (a
	// redirection's target, a variable's value, a [[ test's operand, a case
	// subject, a quoted ${ } expansion's word), keeping the backslash of an
	// escape (x=a\ b is "a\ b"): the value is quoted.
	keepingWord
	// splittingWord is an unquoted ${ } expansion's word, which hash's
	// parser also splits at blanks and globs, quoted or not: a value with
	// a blank or a glob character is not offered.
	splittingWord
)

// insertionAt reads the place of a replacement of line[from:pos], whose
// values all start with prefix, as the lexer and then hash's parser (in
// dialect lang) read it, with the placeholder written there: where the
// parser takes the word for one string, a value is quoted. That one
// reading is also the check the values written there are held to (where
// a command parses the word again, it holds only plain words).
func insertionAt(lang syntax.LangVariant, line string, from, pos int, prefix string) (insertion, reading) {
	in := lexedInsertion(line, from, pos, prefix)
	text, ok := in.write(prefix + placeholder)
	if !ok {
		return in, refusal("the place cannot hold the placeholder")
	}
	c, err := readReference(lang, line, from, pos, text)
	if err != nil {
		return in, refusal("the line does not parse: " + err.Error())
	}
	in.place = in.place.readAs(c.ref.target.role)
	return in, c
}

// lexedInsertion reads the place of a replacement of line[from:pos] as the
// lexer reads it.
func lexedInsertion(line string, from, pos int, prefix string) insertion {
	var lex shlex.Lexer
	lex.Scan(line[:from])
	start := lex.Context()
	in := insertion{
		escaped: start.Escaped,
		layers:  layers{backticks: start.Backticks, nested: start.Backticks > 0 && strings.Contains(line[:from], "\\`")},
	}
	switch text := start.Text.(type) {
	case shlex.CommentText:
		in.place = commentPlace{}
		return in
	case shlex.Body:
		in.place = bodyPlaceAt(text, line, from, pos)
		return in
	}
	code := codePlace{quote: start.Quote, tilde: tildePrefix(prefix)}
	if start.Param == shlex.SplitParam {
		code.word = splittingWord
	}
	lex.Scan(line[from:pos])
	if end := lex.Context(); start.Quote == shlex.NoQuote && end.Quote != shlex.NoQuote &&
		end.Text == nil && end.Depth == start.Depth+1 {
		code.opens = openedQuote{kind: end.Quote, end: closedAtEnd}
		if pos < len(line) {
			code.opens.end = closedByValue
		}
		for i := pos; i < len(line) && code.opens.end == closedByValue; i++ {
			lex.Step(line[i])
			if lex.Context().Depth < end.Depth {
				code.opens.end = closedByLine
			}
		}
	}
	in.place = code
	return in
}

// bodyPlaceAt is the place of a replacement of line[from:pos] in heredoc
// body b: the line of the body it is in, around the replacement.
func bodyPlaceAt(b shlex.Body, line string, from, pos int) bodyPlace {
	end := strings.IndexByte(line[pos:], '\n')
	if end < 0 {
		end = len(line) - pos
	}
	return bodyPlace{body: b, before: line[strings.LastIndexByte(line[:from], '\n')+1 : from], rest: line[pos : pos+end]}
}

// holds reports whether the line with text in the replacement keeps text
// as it is: it does not end the body, and no leading tab of it is stripped.
func (p bodyPlace) holds(text string) bool {
	line := p.before + text + p.rest
	if p.body.StripTabs {
		if strings.TrimLeft(p.before, "\t") == "" && strings.HasPrefix(text, "\t") {
			return false
		}
		line = strings.TrimLeft(line, "\t")
	}
	return line != p.body.Delim
}

// write returns value written as shell text for the place, or false when
// the place cannot hold it: after the escaped byte, if any, written by the
// place, then through the backticks around.
func (in insertion) write(value string) (string, bool) {
	if strings.IndexByte(value, 0) >= 0 {
		return "", false // no shell word holds a NUL
	}
	text, ok := "", false
	if in.escaped {
		text, ok = in.afterEscape(value)
	} else {
		text, ok = in.place.write(value)
	}
	if !ok || in.layers.nested && strings.Contains(text, `\`) {
		return "", false
	}
	for range in.layers.backticks {
		text = backslashBefore(text, "\\`$")
	}
	return text, true
}

// afterEscape writes value right after a backslash the user typed: the
// backslash escapes the value's first byte, which goes as it is, and the
// rest is written as anywhere else in the place. Where that backslash is not
// simply an escape of the next byte, nothing is offered: in $'...' it starts
// an escape sequence, inside backticks the shell takes it off before reading
// the text, in a redirection target or assignment hash keeps it, and before
// a newline it joins the next line (a carriage return may come before one,
// and a byte of a longer rune would split it).
func (in insertion) afterEscape(value string) (string, bool) {
	c := value[0]
	if in.layers.backticks > 0 || !in.place.escapes() || c == '\n' || c == '\r' || c >= utf8.RuneSelf {
		return "", false
	}
	text, ok := in.place.after(value[:1]).write(value[1:])
	return value[:1] + text, ok
}

// write writes value as (part of) a word of code: escaped for the quote
// the replacement is inside, in the quote it opens, or else
// backslash-escaped, a ~ that would start the word and expand included.
func (p codePlace) write(value string) (string, bool) {
	if p.word == splittingWord && strings.ContainsAny(value, " \t\n*?[") {
		return "", false
	}
	text := strings.TrimPrefix(value, p.tilde)
	switch {
	case p.opens.kind != shlex.NoQuote:
		q := p.opens.kind
		quoted := quoteOpen[q] + escapeInQuote(text, q)
		if p.opens.end == closedByValue || p.opens.end == closedAtEnd && !strings.HasSuffix(text, "/") {
			quoted += quoteClose[q]
		}
		text = quoted
	case p.quote != shlex.NoQuote:
		text = escapeInQuote(text, p.quote)
	case p.word != escapingWord:
		text = quoteWord(text)
	case p.tilde == "" && strings.HasPrefix(text, "~"):
		text = `\` + EscapeShellWord(text)
	default:
		text = EscapeShellWord(text)
	}
	return p.tilde + text, true
}

func (p codePlace) escapes() bool {
	return p.quote != shlex.ANSIQuote && p.opens.kind != shlex.ANSIQuote && p.word == escapingWord
}

func (codePlace) reads() []role { return []role{roleFields, roleLiteral, rolePattern} }

// readAs: hash's parser keeps an escape's backslash in a word it expands
// to one string.
func (p codePlace) readAs(r role) place {
	if r == roleLiteral && p.word == escapingWord {
		p.word = keepingWord
	}
	return p
}

// after: the replaced text that opened a quote is gone, and the escape
// the value goes on after is outside quotes.
func (p codePlace) after(string) place {
	p.opens, p.tilde = openedQuote{}, ""
	return p
}

func (commentPlace) write(value string) (string, bool) { return codePlace{}.write(value) }
func (commentPlace) escapes() bool                     { return false } // a backslash in a comment is text
func (p commentPlace) after(string) place              { return p }

// readAs: where hash's parser reads code the lexer took for a comment, the
// place is that code's.
func (p commentPlace) readAs(r role) place {
	if r == roleComment {
		return p
	}
	return codePlace{}.readAs(r)
}

// reads: a comment, or code where the lexer takes code for one.
func (commentPlace) reads() []role {
	return []role{roleComment, roleFields, roleLiteral, rolePattern}
}

// write writes value into the body line as it is, with \, $ and ` escaped
// in an expanding body. A body line cannot hold a newline, which ends it,
// nor bytes that are not UTF-8 (hash's parser rejects or replaces them),
// nor a carriage return at its end (the line may end there, and hash's
// parser reads \r\n as the end of a line), nor make the line the
// delimiter.
func (p bodyPlace) write(value string) (string, bool) {
	if strings.Contains(value, "\n") || strings.HasSuffix(value, "\r") || !utf8.ValidString(value) {
		return "", false
	}
	text := value
	if !p.body.Literal {
		text = backslashBefore(value, "\\$`")
	}
	return text, p.holds(text)
}

// escapes: the lexer reports an escape in a body only where it is one, in
// an expanding body before \, $ and `.
func (bodyPlace) escapes() bool { return true }

func (p bodyPlace) reads() []role {
	if p.body.Literal {
		return []role{roleRaw}
	}
	return []role{roleDocument}
}

func (p bodyPlace) readAs(role) place { return p }

func (p bodyPlace) after(typed string) place {
	p.before += typed
	return p
}

// quoteWord writes text as a word without a backslash escape: as it is
// when nothing in it is special, else in single quotes, or in $'...' when
// it holds a single quote. A ~ in the quotes does not expand.
func quoteWord(text string) string {
	switch {
	case EscapeShellWord(text) == text && !strings.HasPrefix(text, "~"):
		return text
	case strings.Contains(text, "'"):
		return "$'" + escapeInQuote(text, shlex.ANSIQuote) + "'"
	}
	return "'" + escapeInQuote(text, shlex.SingleQuote) + "'"
}

var (
	quoteOpen  = map[shlex.QuoteKind]string{shlex.SingleQuote: "'", shlex.DoubleQuote: `"`, shlex.ANSIQuote: "$'"}
	quoteClose = map[shlex.QuoteKind]string{shlex.SingleQuote: "'", shlex.DoubleQuote: `"`, shlex.ANSIQuote: "'"}
	// quoteEscapes are the bytes a backslash escapes in a quote.
	quoteEscapes = map[shlex.QuoteKind]string{shlex.DoubleQuote: "\\\"$`", shlex.ANSIQuote: `\'`}
)

// escapeInQuote returns text written inside the quote q so that it means
// text. A carriage return, and a byte that is not UTF-8, go outside the
// quote as $'\r' and $'\xNN': hash's parser reads a quoted \r\n as \n,
// and rejects or replaces bytes that are not UTF-8.
func escapeInQuote(text string, q shlex.QuoteKind) string {
	var b strings.Builder
	for i := 0; i < len(text); {
		r, size := utf8.DecodeRuneInString(text[i:])
		c := text[i : i+size]
		i += size
		if escape := ansiEscape(r, c); escape != "" {
			if q != shlex.ANSIQuote { // close the quote around $'...'
				escape = quoteClose[q] + "$'" + escape + "'" + quoteOpen[q]
			}
			b.WriteString(escape)
			continue
		}
		switch {
		case q == shlex.SingleQuote && c == "'":
			c = `'\''`
		case len(c) == 1 && strings.IndexByte(quoteEscapes[q], c[0]) >= 0:
			c = `\` + c
		}
		b.WriteString(c)
	}
	return b.String()
}

// ansiEscape returns the $'...' escape for c, the bytes of r, when it goes
// in no other quote: a carriage return, or a byte that is not UTF-8.
func ansiEscape(r rune, c string) string {
	switch {
	case r == '\r':
		return `\r`
	case r == utf8.RuneError && len(c) == 1:
		return fmt.Sprintf(`\x%02x`, c[0])
	}
	return ""
}

// backslashBefore puts a backslash before each byte of text in special,
// which holds ASCII bytes only.
func backslashBefore(text, special string) string {
	var b strings.Builder
	for i := 0; i < len(text); i++ {
		if strings.IndexByte(special, text[i]) >= 0 {
			b.WriteByte('\\')
		}
		b.WriteByte(text[i])
	}
	return b.String()
}

// EscapeShellWord returns text that can be inserted as one shell word.
// It favors backslash escaping because it composes well with interactive path
// completion and directory drilling. A newline, which a backslash would turn
// into a line continuation, is written $'\n', a carriage return (a backslash
// before \r\n joins the lines too) $'\r', and a byte that is not UTF-8
// $'\xNN'.
func EscapeShellWord(word string) string {
	var b strings.Builder
	b.Grow(len(word))
	for i := 0; i < len(word); {
		r, size := utf8.DecodeRuneInString(word[i:])
		c := word[i : i+size]
		i += size
		switch {
		case r == '\n':
			b.WriteString(`$'\n'`)
		case r == '\r', r == utf8.RuneError && size == 1:
			b.WriteString(`$'` + ansiEscape(r, c) + `'`)
		case isShellWordSafeRune(r):
			b.WriteString(c)
		default:
			b.WriteString(`\` + c)
		}
	}
	return b.String()
}

func isShellWordSafeRune(r rune) bool {
	if unicode.IsLetter(r) || unicode.IsDigit(r) {
		return true
	}
	return strings.ContainsRune("@%_+=:,./-~", r)
}

// plainWordRune reports whether r means itself in a word wherever the word
// is, parsed once or again: no quoting, expansion (a ~, zsh's =cmd) or
// assignment comes of it.
func plainWordRune(r rune) bool {
	return r != '~' && r != '=' && isShellWordSafeRune(r)
}
