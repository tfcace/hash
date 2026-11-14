package shlex

import (
	"slices"
	"strings"
)

// Substitutions reports how many command substitutions, $( ) and
// backticks, enclose the next byte. A caller that splits commands sees one
// open when the count goes up after a byte and close when it goes down: the
// word that holds the substitution goes on after it. $(( turns the $( just
// opened into arithmetic, so the count goes back down at its second (.
func (l *Lexer) Substitutions() int {
	n := 0
	for _, f := range l.nest {
		if f.kind == frameSubst || f.kind == frameBacktick {
			n++
		}
	}
	return n
}

// InExpression reports whether the next byte is inside $(( )) or (( ))
// (a parenthesized group inside them included) or directly inside ${ },
// where blanks and operators are part of an expression in one word, not
// word breaks and command separators. A $( ) inside ${ } holds commands
// again.
func (l *Lexer) InExpression() bool {
	switch l.innermost() {
	case frameArith, frameCommand, frameGroup, frameParam:
		return true
	default:
		return false
	}
}

// GluesRedirection reports whether c, read next, would be part of the
// redirection operator the last byte started: the & of 2>&1, <&0 and >&2,
// or the | of >|. Anywhere else & and | end a command.
func (l *Lexer) GluesRedirection(c byte) bool {
	switch c {
	case '&':
		return l.pending == pendLess || l.pending == pendGreater
	case '|':
		return l.pending == pendGreater
	}
	return false
}

// Text is the text, rather than code, the next byte is in: nil in code
// (and quoted text in it), a CommentText, or a heredoc's Body.
type Text interface{ isText() }

// CommentText is a comment, which runs to the end of the line.
type CommentText struct{}

// Body is a heredoc body, its delimiter line included. It ends at a line
// that reads as Delim, after a <<- body (StripTabs) takes the line's
// leading tabs off. Literal: the delimiter is quoted, and nothing in the
// body is special; otherwise \, $ and ` are.
type Body struct {
	Delim     string
	StripTabs bool
	Literal   bool
}

func (CommentText) isText() {}
func (Body) isText()        {}

// QuoteKind is the kind of quoted text the next byte is in.
type QuoteKind uint8

const (
	NoQuote     QuoteKind = iota // not directly inside quotes
	SingleQuote                  // '...': nothing is special but the closing '
	DoubleQuote                  // "...": \ escapes " \ $ and `
	ANSIQuote                    // $'...': \ escapes, ' closes
)

// ParamKind is the kind of ${ } expansion the next byte is directly in.
type ParamKind uint8

const (
	NoParam     ParamKind = iota // not directly inside ${ }
	SplitParam                   // unquoted: what it expands to is split into fields and globbed
	QuotedParam                  // in double quotes or a heredoc body: one string
)

// Context is what the next byte is part of, for a caller that writes text
// there.
type Context struct {
	Quote QuoteKind // the quote it is directly inside: inside a $( ) in double quotes, it is code again
	Text  Text      // the comment or heredoc body it is in, or nil
	// Escaped: a backslash right before it escapes it (in a body, only $ `
	// \ and a newline).
	Escaped bool
	// Backticks is how many backtick substitutions it is in: the shell
	// reads the text of each once more, after taking a backslash off
	// \\, \` and \$.
	Backticks int
	// Param is the ${ } it is directly inside, in the word of an expansion
	// like ${x:-word}.
	Param ParamKind
	// Depth is how many quotes, substitutions, groups and heredoc bodies
	// it is in: a quote open here is closed where the depth goes below it.
	Depth int
}

// Context reports what the next byte is part of.
func (l *Lexer) Context() Context {
	c := Context{
		Escaped: l.mode == modeEscape || l.mode == modeBodyEscape,
		Param:   l.paramKind(),
		Depth:   len(l.nest),
	}
	for _, f := range l.nest {
		if f.kind == frameBacktick {
			c.Backticks++
		}
	}
	switch f := l.innermost(); {
	case l.mode == modeComment:
		c.Text = CommentText{}
	case f == frameBody:
		h := l.body().heredocs[0]
		c.Text = Body{Delim: h.delim, StripTabs: h.stripTabs, Literal: h.literal}
	case f == frameSingle:
		c.Quote = SingleQuote
	case f == frameDouble:
		c.Quote = DoubleQuote
	case f == frameANSI:
		c.Quote = ANSIQuote
	}
	return c
}

// paramKind reports the kind of ${ } the next byte is directly in.
func (l *Lexer) paramKind() ParamKind {
	switch {
	case l.innermost() != frameParam:
		return NoParam
	case l.paramQuoted():
		return QuotedParam
	}
	return SplitParam
}

// Closer returns text that, read next, closes everything open: it ends a
// comment, closes each quote, substitution, expansion and group from the
// innermost out, and ends each heredoc body still to read with its
// delimiter, those a closed substitution hands out included. A line that a
// parser rejects as unfinished parses with it, the way the line would once
// typed to its end. A pending escape would take the closer's first byte,
// so an escaped blank, which means itself wherever a backslash escapes,
// goes first.
func (l *Lexer) Closer() string {
	if l.mode == modeEscape {
		c := l.clone()
		c.Step(' ')
		return " " + c.Closer()
	}
	if l.delim.phase == delimText { // the delimiter word ends with the line
		c := l.clone()
		scope := c.scope()
		scope.heredocs = append(scope.heredocs, heredoc{delim: string(l.delim.text)})
		c.delim = delimWord{}
		return c.Closer()
	}
	var b strings.Builder
	atLineStart := func() bool { return b.Len() > 0 && b.String()[b.Len()-1] == '\n' }
	switch l.mode {
	case modeComment:
		b.WriteString(" \n") // mvdan joins a comment's trailing backslash with the next line
	case modeBodyEscape:
		b.WriteByte('\n') // the backslash would join the delimiter line
	}
	var handed []heredoc // bodies a closed substitution hands out
	endBodies := func(heredocs []heredoc) {
		for _, h := range heredocs {
			b.WriteString(h.delim + "\n")
		}
	}
	for i := len(l.nest) - 1; i >= 0; i-- {
		switch f := l.nest[i]; f.kind {
		case frameBody:
			if !atLineStart() && (b.Len() > 0 || len(f.line) > 0) {
				b.WriteByte('\n')
			}
			endBodies(append(slices.Clone(f.heredocs), handed...))
			handed = nil
		case frameSubst, frameBacktick:
			b.WriteString(frameClosers[f.kind])
			handed = append(slices.Clone(f.heredocs), handed...)
		default:
			b.WriteString(frameClosers[f.kind])
		}
	}
	if pending := append(slices.Clone(l.top.heredocs), handed...); len(pending) > 0 {
		if !atLineStart() {
			b.WriteByte('\n')
		}
		endBodies(pending)
	}
	return b.String()
}

// frameClosers is the text that closes each kind of frame but a body.
var frameClosers = map[frameKind]string{
	frameSingle: "'", frameANSI: "'", frameDouble: `"`, frameSubst: ")", frameParam: "}",
	frameBacktick: "`", frameParen: ")", frameArith: "))", frameCommand: "))", frameGroup: ")",
	frameGlobFlags: ")",
}

// clone is a copy of l that reads on without changing l.
func (l *Lexer) clone() *Lexer {
	c := *l
	c.nest = slices.Clone(l.nest)
	for i := range c.nest {
		c.nest[i].heredocs = slices.Clone(c.nest[i].heredocs)
		c.nest[i].line = slices.Clone(c.nest[i].line)
	}
	c.top.heredocs = slices.Clone(c.top.heredocs)
	c.delim.text = slices.Clone(c.delim.text)
	return &c
}
