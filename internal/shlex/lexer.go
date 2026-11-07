// Package shlex says what each byte of shell input is: code, a quote, quoted
// text, an escape, a comment or heredoc body. It is the one place that knows
// the quoting rules; the ?? marker search, the rc file filter and completion
// each keep only their own policy on top of it.
//
// mvdan.cc/sh/v3/syntax cannot serve here: it parses complete programs, so it
// rejects the half-typed line completion works on (an open quote, a dangling
// $( ), it has no zsh dialect for rc files it must read anyway, and it reports
// a syntax tree, not the quoting state of every byte.
//
// The lexer reads one byte at a time with no lookahead, so feeding input in
// chunks (the rc filter feeds a line at a time) gives the same result as
// feeding it whole. Sequences of several bytes ($( , ${, $', <(, ((, <<) are
// recognized when their last byte arrives.
package shlex

import (
	"strings"
	"unicode/utf8"
)

// Class is what one byte of shell input is, given the bytes before it.
type Class uint8

const (
	Code    Class = iota // shell code outside quotes: words, blanks and operators
	Quote                // a quote that opens or closes quoted text
	Quoted               // literal text inside quotes
	Escape               // a backslash outside single quotes
	Escaped              // a byte made literal by the backslash before it, which is dropped
	Comment              // a comment, from # up to the end of the line
	Heredoc              // heredoc body text, its delimiter line included (a substitution in it is code)
)

// frameKind is a nesting level that can span bytes and lines.
type frameKind uint8

const (
	frameNone      frameKind = iota // no frame: the top level
	frameSingle                     // '...': no escapes
	frameANSI                       // $'...': backslash escapes
	frameDouble                     // "...": backslash escapes, $( ${ and ` nest
	frameSubst                      // $( ... ): commands inside a word
	frameParam                      // ${ ... }: a parameter expansion; quotes nest, # and ( are text
	frameBacktick                   // ` ... `: commands inside a word
	frameParen                      // ( ... ): a subshell, array or other group of code
	frameArith                      // $(( ... )): arithmetic inside a word
	frameCommand                    // (( ... )): an arithmetic command
	frameGroup                      // ( ... ) inside arithmetic
	frameGlobFlags                  // zsh's (#...) glob flags, part of a word
	// frameBody is the bodies of the heredocs a line started, read in turn
	// after it: text, in which an unquoted delimiter's body opens $( ),
	// backticks and ${ } as in double quotes.
	frameBody
)

// frame is one open nesting level. A substitution's frame (and the lexer's
// top level) holds the heredocs whose << it read and whose bodies a newline
// inside it starts; a heredoc body's frame holds the heredocs it reads, the
// current one first, and the current body line. A body frame is popped with
// its last heredoc, so it always has one.
type frame struct {
	kind     frameKind
	heredocs []heredoc
	// line is a body frame's current line, without the tabs <<- strips,
	// kept up to one byte longer than the longest delimiter: enough to tell
	// whether the line is the delimiter. It holds the code of a substitution
	// in the body too, newlines included: a line that starts or ends inside
	// one is not the delimiter.
	line []byte
}

// mode is what the next byte continues, on top of the innermost frame.
type mode uint8

const (
	modeNormal     mode = iota // the innermost frame decides
	modeEscape                 // the last byte was a backslash: this one is literal
	modeComment                // a comment runs to the end of the line
	modeBodyEscape             // the last body byte was a backslash that may join the next line
)

// pend is the last byte when the next one may combine with it.
type pend uint8

const (
	pendNone    pend = iota
	pendDollar       // $: a ( makes $(, a ' makes $'
	pendSubst        // the ( of $(: another ( makes $((
	pendParen        // ( at the start of a word: another makes ((, a # glob flags
	pendGroup        // ( inside a word: a # makes glob flags
	pendClose        // ) in arithmetic: another closes it
	pendLess         // <: another makes <<, a ( makes <(
	pendGreater      // >: a ( makes >(
	pendHeredoc      // <<: a third < makes <<<, anything else starts the delimiter
)

// heredoc is a pending heredoc body and how its delimiter line looks.
type heredoc struct {
	delim     string
	stripTabs bool // <<-
	literal   bool // the delimiter was quoted: no line continuations in the body
}

// delimPhase is how far reading a heredoc delimiter has got.
type delimPhase uint8

const (
	delimNone   delimPhase = iota // not reading a delimiter
	delimBlanks                   // after << or <<-, before the word
	delimText                     // in the word
)

// delimWord is the heredoc delimiter being read after <<. Its bytes are
// lexed as usual, so quotes in it ('EOF', "E O F") are quotes; the word
// ends at a blank or operator back at the nesting depth of the <<.
type delimWord struct {
	phase     delimPhase
	depth     int
	stripTabs bool
	quoted    bool
	text      []byte
}

// Lexer is the quoting state of shell input read so far. The zero value is
// the start of input, read with bash's rules.
type Lexer struct {
	// GlobFlags reads zsh's glob flags: a ( right before a # opens them, as
	// in (#i)xterm* and *(#qN), and the ) that closes them is part of the
	// word. bash (the default) reads (# as a group and a comment. Set it
	// before the first byte.
	GlobFlags bool

	nest []frame
	// top is the top level's frame: the heredocs whose bodies the next
	// newline outside any substitution starts.
	top     frame
	delim   delimWord
	mode    mode
	pending pend
	inWord  bool // the last byte was part of a word, so # is not a comment
	// continued: the last byte was an escaped newline, so the command goes
	// on to the next line whatever else is open.
	continued bool
}

// Scan steps the lexer over s.
func (l *Lexer) Scan(s string) {
	for i := 0; i < len(s); i++ {
		l.Step(s[i])
	}
}

// Step reads one byte and reports what it is.
func (l *Lexer) Step(c byte) Class {
	depth, outer := len(l.nest), l.innermost()
	if body := l.innermostBody(); outer != frameBody && body != nil {
		body.record(c) // code inside a body is on the body line too
	}
	l.continued = false
	class := l.step(c)
	l.inWord = l.wordGoesOn(c, class, depth, outer)
	return class
}

func (l *Lexer) step(c byte) Class {
	p := l.pending
	l.pending = pendNone
	if p == pendHeredoc {
		p = pendNone
		if c == '<' {
			return Code // <<< is a here-string
		}
		l.delim = delimWord{phase: delimBlanks, depth: len(l.nest), stripTabs: c == '-'}
		if c == '-' {
			return Code
		}
	}
	if l.delim.phase != delimNone {
		if class, ok := l.delimiter(c, p); ok {
			return class
		}
	}
	return l.lex(c, p)
}

// wordGoesOn reports whether the byte after c, of class, would be inside a
// word, so that a # there is not a comment. depth and outer are the nesting
// depth and innermost frame before c. This is the only place inWord changes.
func (l *Lexer) wordGoesOn(c byte, class Class, depth int, outer frameKind) bool {
	switch {
	case class == Escape, class == Escaped && c == '\n':
		return l.inWord // a backslash or line continuation leaves the word as it was
	case len(l.nest) > depth: // c opened a quote (in the word) or code (a command starts)
		f := l.innermost()
		return f == frameSingle || f == frameANSI || f == frameDouble || f == frameParam
	case len(l.nest) < depth: // c closed outer: $(cmd)#x and "a"#b are one word
		return outer != frameParen && outer != frameCommand && outer != frameBody
	case class == Code:
		return strings.IndexByte(" \t\n;|&()<>", c) < 0 // blanks and operators end a word
	case class == Comment, class == Heredoc:
		return false
	}
	return true // quotes, quoted and escaped text
}

// TopLevel reports whether the next byte is outside any quote, substitution
// or arithmetic.
func (l *Lexer) TopLevel() bool {
	return len(l.nest) == 0
}

// Continues reports whether the input read so far stops in the middle of a
// command: a quote, substitution or arithmetic is open, a backslash or an
// escaped newline joins the next line, or a heredoc body is still to come.
func (l *Lexer) Continues() bool {
	return len(l.nest) > 0 || len(l.top.heredocs) > 0 ||
		l.delim.phase != delimNone || l.mode == modeEscape || l.continued
}

// Continued reports whether the last byte read was an escaped newline,
// which joins the next line onto the command whatever else is open.
func (l *Lexer) Continued() bool {
	return l.continued
}

// Expands reports whether a $ or ` as the next byte would expand: not
// inside single or ANSI-C quotes, a comment or the body of a heredoc with a
// quoted delimiter, or after a backslash.
func (l *Lexer) Expands() bool {
	if l.mode != modeNormal {
		return false
	}
	switch l.innermost() {
	case frameSingle, frameANSI:
		return false
	case frameBody:
		return !l.body().heredocs[0].literal
	}
	return true
}

// innermost is the kind of the innermost open frame.
func (l *Lexer) innermost() frameKind {
	if len(l.nest) == 0 {
		return frameNone
	}
	return l.nest[len(l.nest)-1].kind
}

func (l *Lexer) push(kind frameKind) { l.nest = append(l.nest, frame{kind: kind}) }
func (l *Lexer) pop()                { l.nest = l.nest[:len(l.nest)-1] }

// become turns the innermost frame, just opened by the byte before, into
// kind: $( into $(( and ( into ((.
func (l *Lexer) become(kind frameKind) { l.nest[len(l.nest)-1].kind = kind }

// body is the innermost frame, a heredoc body's.
func (l *Lexer) body() *frame { return &l.nest[len(l.nest)-1] }

// innermostBody is the innermost heredoc body's frame, maybe with a
// substitution in the body inside it, or nil.
func (l *Lexer) innermostBody() *frame {
	for i := len(l.nest) - 1; i >= 0; i-- {
		if l.nest[i].kind == frameBody {
			return &l.nest[i]
		}
	}
	return nil
}

// scope is the frame whose heredocs a << read here joins: the innermost
// substitution or heredoc body (in which only a substitution reads code),
// or the top level.
func (l *Lexer) scope() *frame {
	for i := len(l.nest) - 1; i >= 0; i-- {
		switch l.nest[i].kind {
		case frameSubst, frameBacktick, frameBody:
			return &l.nest[i]
		}
	}
	return &l.top
}

func (l *Lexer) inBacktick() bool {
	for _, f := range l.nest {
		if f.kind == frameBacktick {
			return true
		}
	}
	return false
}

// delimiter reads c as part of a heredoc delimiter. It reports false when
// the delimiter is over (or never started) and c is to be lexed as usual.
func (l *Lexer) delimiter(c byte, p pend) (Class, bool) {
	d := &l.delim
	switch {
	case d.phase == delimBlanks && isBlank(c):
		return l.lex(c, p), true
	case d.phase == delimBlanks && !startsDelimiter(c):
		*d = delimWord{} // 1<<2 and $x<<y are not heredocs
		return Code, false
	case d.phase == delimBlanks:
		d.phase = delimText
	case l.mode == modeNormal && len(l.nest) == d.depth && endsDelimiter(c):
		scope := l.scope()
		scope.heredocs = append(scope.heredocs, heredoc{delim: string(d.text), stripTabs: d.stripTabs, literal: d.quoted})
		*d = delimWord{}
		return Code, false
	}
	class := l.lex(c, p)
	switch {
	case inText(class, c):
		d.text = append(d.text, c)
		d.quoted = d.quoted || class == Escaped
	case class == Quote:
		d.quoted = true
	case class == Escaped && len(d.text) == 0 && !d.quoted:
		d.phase = delimBlanks // a line continuation: the word has not started yet
	}
	return class, true
}

// inText reports whether byte c of class is part of the text a quoted or
// escaped word stands for: not a quote, an escaping backslash or a line
// continuation.
func inText(class Class, c byte) bool {
	return class != Quote && class != Escape && (class != Escaped || c != '\n')
}

// startsDelimiter reports whether c can start a heredoc delimiter: a letter,
// underscore, quote or backslash, which rules out arithmetic like 1<<2.
func startsDelimiter(c byte) bool {
	return c == '_' || c == '\'' || c == '"' || c == '\\' || ('a' <= c|0x20 && c|0x20 <= 'z')
}

func endsDelimiter(c byte) bool {
	return isBlank(c) || strings.IndexByte("\n;|&<>()`", c) >= 0
}

// lex reads c in the current mode and innermost frame.
func (l *Lexer) lex(c byte, p pend) Class {
	switch l.mode {
	case modeEscape:
		return l.escaped(c)
	case modeBodyEscape:
		return l.bodyEscaped(c)
	case modeComment:
		if c != '\n' && (c != '`' || !l.inBacktick()) {
			return Comment
		}
		l.mode = modeNormal // the newline or backtick ends the comment
	}
	return l.readFrame(c, p)
}

// readFrame reads c in the innermost frame.
func (l *Lexer) readFrame(c byte, p pend) Class {
	switch f := l.innermost(); f {
	case frameBody:
		return l.readBody(c, p)
	case frameNone, frameSubst, frameBacktick, frameParen:
		return l.code(c, p, f)
	case frameDouble:
		return l.double(c, p)
	case frameParam:
		return l.param(c, p)
	case frameArith, frameCommand, frameGroup:
		return l.arith(c, p, f)
	case frameGlobFlags:
		if c == ')' {
			l.pop()
		}
		return Code
	case frameANSI:
		if c == '\\' {
			l.mode = modeEscape
			return Escape
		}
	}
	if c == '\'' { // frameSingle, frameANSI
		l.pop()
		return Quote
	}
	return Quoted
}

// escaped reads the byte after a backslash. Inside double quotes a
// backslash only escapes $ ` " \ and newline; before anything else both
// bytes are literal.
func (l *Lexer) escaped(c byte) Class {
	l.mode = modeNormal
	if c == '\n' {
		l.continued = true
	}
	if l.innermost() == frameDouble && strings.IndexByte("$`\"\\\n", c) < 0 {
		return Quoted
	}
	return Escaped
}

// body reads a heredoc body byte, ending the first heredoc of the current
// scope at the end of its delimiter line, and the frame with the last one.
// In the body of an unquoted delimiter $( ), backticks and ${ } open code,
// and a backslash-newline joins the next line, so that line is not the
// delimiter on its own. Inside backticks the closing backtick ends the
// substitution's text, and with it the bodies still open in it.
func (l *Lexer) readBody(c byte, p pend) Class {
	f := l.body()
	h := f.heredocs[0]
	switch {
	case c == '`' && l.inBacktick():
		l.pop()
		return l.lex(c, pendNone)
	case c == '\n':
		line := string(f.line)
		f.line = f.line[:0]
		if line == h.delim {
			f.heredocs = f.heredocs[1:]
			if len(f.heredocs) == 0 {
				l.pop()
			}
		}
		return Heredoc
	case h.literal:
	case c == '\\':
		l.mode = modeBodyEscape
		return Heredoc
	default:
		f.bodyByte(c)
		l.expand(c, p) // may push: f is not used after it
		return Heredoc
	}
	f.bodyByte(c)
	return Heredoc
}

// expand reads c in the body of an unquoted heredoc, where $( ), backticks
// and ${ } open code as in double quotes.
func (l *Lexer) expand(c byte, p pend) {
	switch c {
	case '$':
		l.dollar(p)
	case '(':
		if p == pendDollar {
			l.openSubst()
		}
	case '{':
		if p == pendDollar {
			l.push(frameParam)
		}
	case '`':
		l.push(frameBacktick)
	}
}

// bodyEscaped reads the body byte after a backslash: a newline joins the
// next line, anything else is text.
func (l *Lexer) bodyEscaped(c byte) Class {
	l.mode = modeNormal
	if c != '\n' {
		f := l.body()
		f.bodyByte('\\')
		f.bodyByte(c)
	}
	return Heredoc
}

// closeSubstitution pops the substitution that ends here and hands the
// bodies still to read in it to the scope around it, as mvdan reads
// $(cat <<EOF) with the body after the line.
func (l *Lexer) closeSubstitution() {
	pending := l.nest[len(l.nest)-1].heredocs
	l.pop()
	scope := l.scope()
	scope.heredocs = append(scope.heredocs, pending...)
}

// bodyByte adds c, a byte of the current body's text, to the body line
// matched against its delimiter, unless it is a leading tab <<- strips.
func (f *frame) bodyByte(c byte) {
	if len(f.line) > 0 || c != '\t' || !f.heredocs[0].stripTabs {
		f.record(c)
	}
}

// record adds c to the body line, up to one byte past the longest
// delimiter.
func (f *frame) record(c byte) {
	longest := 0
	for _, h := range f.heredocs {
		longest = max(longest, len(h.delim))
	}
	if len(f.line) <= longest {
		f.line = append(f.line, c)
	}
}

// code reads c where commands are read: the top level, $( ), backticks and
// ( ) (frame f).
func (l *Lexer) code(c byte, p pend, f frameKind) Class {
	wordStart := !l.inWord
	switch c {
	case '\n':
		if scope := l.scope(); len(scope.heredocs) > 0 {
			// bodies start after the line that holds <<
			heredocs := scope.heredocs
			scope.heredocs = nil
			l.nest = append(l.nest, frame{kind: frameBody, heredocs: heredocs})
		}
	case '\\':
		l.mode = modeEscape
		return Escape
	case '\'', '"', '`':
		return l.quote(c, p, f)
	case '$':
		l.dollar(p)
	case '{':
		if p == pendDollar {
			l.push(frameParam)
		}
	case '(':
		l.openParen(p, wordStart)
	case ')':
		l.closeParen(f)
	case '#':
		return l.hash(p, wordStart)
	case '<':
		if p == pendLess {
			l.pending = pendHeredoc
		} else {
			l.pending = pendLess
		}
	case '>':
		l.pending = pendGreater
	}
	return Code
}

// hash reads a # where commands are read: right after a ( with GlobFlags it
// opens zsh glob flags, at the start of a word a comment, and anywhere else
// it is part of the word.
func (l *Lexer) hash(p pend, wordStart bool) Class {
	switch {
	case l.GlobFlags && (p == pendParen || p == pendGroup):
		l.become(frameGlobFlags)
	case wordStart:
		l.mode = modeComment
		return Comment
	}
	return Code
}

// quote reads a quote or backtick where commands are read.
func (l *Lexer) quote(c byte, p pend, f frameKind) Class {
	switch {
	case c == '"':
		l.push(frameDouble)
	case c == '\'' && p == pendDollar:
		l.push(frameANSI)
	case c == '\'':
		l.push(frameSingle)
	case f == frameBacktick:
		l.closeSubstitution() // the word goes on after the closing backtick
		return Code
	default:
		l.push(frameBacktick) // a command starts inside
		return Code
	}
	return Quote
}

// dollar notes a $ that may start $( or $'; $$ is a parameter of its own.
func (l *Lexer) dollar(p pend) {
	if p != pendDollar {
		l.pending = pendDollar
	}
}

// openParen reads a ( where commands are read. $(, <( and >( open a
// substitution, (( at the start of a word arithmetic, and any other ( a group
// of code (a subshell, an array, a function's ()) that the matching ) closes.
func (l *Lexer) openParen(p pend, wordStart bool) {
	switch p {
	case pendDollar:
		l.openSubst()
	case pendLess, pendGreater:
		l.push(frameSubst) // <(( is <( and a subshell, never arithmetic
	case pendSubst:
		l.become(frameArith) // $((
	case pendParen:
		l.become(frameCommand) // ((
	default:
		l.push(frameParen)
		l.pending = pendGroup
		if wordStart {
			l.pending = pendParen
		}
	}
}

func (l *Lexer) openSubst() {
	l.push(frameSubst)
	l.pending = pendSubst
}

// closeParen reads a ) where commands are read: it closes a $( ) or ( ),
// and is a case pattern's ) anywhere else.
func (l *Lexer) closeParen(f frameKind) {
	switch f {
	case frameSubst:
		l.closeSubstitution()
	case frameParen:
		l.pop()
	}
}

// param reads c inside ${ }, where the expansion's words may hold quotes
// and substitutions of their own, inside double quotes too (as mvdan reads
// "${x:-'a'}": the single quotes are quotes). The braces are code, like the
// parens of $( ); the text is code outside double quotes and quoted text
// inside them.
func (l *Lexer) param(c byte, p pend) Class {
	switch c {
	case '}':
		l.pop()
		return Code
	case '\'', '"':
		l.quote(c, p, frameParam)
		return Quote
	case '`':
		l.push(frameBacktick)
		return Code
	case '\\':
		l.mode = modeEscape
		return Escape
	case '$':
		l.dollar(p)
	case '(':
		if p == pendDollar {
			l.openSubst()
			return Code
		}
	case '{':
		if p == pendDollar {
			l.push(frameParam)
			return Code
		}
	}
	if l.paramQuoted() {
		return Quoted
	}
	return Code
}

// paramQuoted reports whether the innermost ${ } is inside double quotes
// or a heredoc body, with only other ${ } between: "${x:-${y:-a b}}".
func (l *Lexer) paramQuoted() bool {
	for i := len(l.nest) - 2; i >= 0; i-- {
		if l.nest[i].kind != frameParam {
			return l.nest[i].kind == frameDouble || l.nest[i].kind == frameBody
		}
	}
	return false
}

// double reads c inside double quotes.
func (l *Lexer) double(c byte, p pend) Class {
	switch c {
	case '"':
		l.pop()
		return Quote
	case '\\':
		l.mode = modeEscape
		return Escape
	case '`':
		l.push(frameBacktick)
		return Code
	case '$':
		l.dollar(p)
	case '(':
		if p == pendDollar {
			l.openSubst()
			return Code
		}
	case '{':
		if p == pendDollar {
			l.push(frameParam)
			return Code
		}
	}
	return Quoted
}

// arith reads c inside $(( )), (( )) or a ( ) group within them (frame f).
// The shift << and base notation 16#ff are plain arithmetic here, and no
// comment starts at the end of $(( )), which is part of a word; (( )) is a
// command of its own.
func (l *Lexer) arith(c byte, p pend, f frameKind) Class {
	switch c {
	case '(':
		if p == pendDollar {
			l.openSubst()
		} else {
			l.push(frameGroup)
		}
	case ')':
		switch {
		case f == frameGroup:
			l.pop()
		case p == pendClose:
			l.pop()
		default:
			l.pending = pendClose
		}
	case '"':
		l.push(frameDouble)
		return Quote
	case '\'':
		if p == pendDollar {
			l.push(frameANSI)
		} else {
			l.push(frameSingle)
		}
		return Quote
	case '`':
		l.push(frameBacktick)
	case '\\':
		l.mode = modeEscape
		return Escape
	case '$':
		l.dollar(p)
	}
	return Code
}

// Unquote returns word with its quotes and escaping backslashes removed, the
// way the shell would pass it to a command: $'...' and $"..." lose their $
// too, and the ANSI-C escapes in $'...' are decoded. A trailing backslash
// with nothing to escape is kept.
func Unquote(word string) string {
	b := make([]byte, 0, len(word))
	var l Lexer
	escape := false
	for i := 0; i < len(word); i++ {
		c := word[i]
		dollar, depth, ansi := l.pending == pendDollar, len(l.nest), l.innermost() == frameANSI
		class := l.Step(c)
		switch {
		case class == Escape && ansi:
			text, n := decodeANSI(word[i+1:])
			b = append(b, text...)
			l.Scan(word[i+1 : i+1+n])
			i += n
			continue
		case class == Quote && dollar && len(l.nest) > depth:
			b = b[:len(b)-1] // the $ that opened $'...' or $"..."
		case !inText(class, c):
		case class == Escaped:
			b = append(b, c)
		case escape:
			b = append(b, '\\', c) // inside "...", \a is two literal bytes
		default:
			b = append(b, c)
		}
		escape = class == Escape
	}
	if escape {
		b = append(b, '\\')
	}
	return string(b)
}

// ansiEscapes are the one-letter escapes of $'...'.
var ansiEscapes = map[byte]byte{
	'a': '\a', 'b': '\b', 'e': 0x1b, 'E': 0x1b, 'f': '\f', 'n': '\n', 'r': '\r',
	't': '\t', 'v': '\v', '\\': '\\', '\'': '\'', '"': '"', '?': '?',
}

// decodeANSI decodes the escape at the start of s, which follows a
// backslash in $'...', and reports how many bytes of s it took. An unknown
// escape keeps its backslash, as the shell does; \ooo above 255 wraps.
func decodeANSI(s string) (text string, n int) {
	if s == "" {
		return "", 0
	}
	if c, ok := ansiEscapes[s[0]]; ok {
		return string(c), 1
	}
	switch s[0] {
	case 'x':
		if v, n := digits(s[1:], 16, 2); n > 0 {
			return string([]byte{byte(v & 0xff)}), 1 + n
		}
	case 'u', 'U':
		width := 4
		if s[0] == 'U' {
			width = 8
		}
		if v, n := digits(s[1:], 16, width); n > 0 {
			r := utf8.RuneError
			if v <= utf8.MaxRune {
				r = rune(v)
			}
			return string(r), 1 + n
		}
	case 'c':
		if len(s) > 1 && s[1] != '\'' {
			return string([]byte{s[1] & 0x1f}), 2
		}
	default:
		if v, n := digits(s, 8, 3); n > 0 {
			return string([]byte{byte(v & 0xff)}), n
		}
	}
	return "\\" + s[:1], 1
}

// digits reads up to width digits in base (8 or 16) from the start of s.
func digits(s string, base uint32, width int) (value uint32, n int) {
	for ; n < width && n < len(s); n++ {
		var d uint32
		switch c := s[n]; {
		case '0' <= c && c <= '9':
			d = uint32(c - '0')
		case 'a' <= c|0x20 && c|0x20 <= 'f':
			d = uint32(c|0x20-'a') + 10
		default:
			return value, n
		}
		if d >= base {
			return value, n
		}
		value = value*base + d
	}
	return value, n
}

// WordStart returns the offset in line where the word that ends at pos
// starts. Words split at blanks and newlines only, so an operator glued to a
// word (the "ls|" of "ls|gr") is part of it, and a quoted or escaped blank
// or newline does not split it.
func WordStart(line string, pos int) int {
	pos = max(0, min(pos, len(line)))
	start := 0
	var l Lexer
	for i := 0; i < pos; i++ {
		if class := l.Step(line[i]); (class == Code || class == Comment) && (isBlank(line[i]) || line[i] == '\n') {
			start = i + 1
		}
	}
	return start
}

func isBlank(c byte) bool {
	return c == ' ' || c == '\t'
}
