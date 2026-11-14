// Package shparse runs mvdan.cc/sh's parser on input hash did not write: rc
// files, command lines, scripts, eval and source text. The parser panics on
// some malformed input (zsh's `cat <<$a[`), which must be a syntax error, not
// a crash of the shell.
package shparse

import (
	"fmt"
	"io"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// Parse parses src with p, as p.Parse does, turning a parser panic into an
// error. A comment's trailing backslash is the comment's, as in bash
// (commentBackslashes).
func Parse(p *syntax.Parser, src, name string) (f *syntax.File, err error) {
	defer func() {
		if r := recover(); r != nil {
			f, err = nil, fmt.Errorf("shell parser failed: %v", r)
		}
	}()
	return p.Parse(strings.NewReader(dropCommentBackslashes(p, src)), name)
}

// dropCommentBackslashes blanks the backslash that ends a comment's line in
// src (a space in its place, so every offset into src stays the same: hash
// reads positions of the parse against src, completion among them). In bash a comment runs to the end of its line, a backslash in it
// included; mvdan's lexer takes one at the end for a line continuation and
// joins the next line to the command before the comment (`echo a # note \`
// then `echo b` runs `echo a echo b`). The comments are found by parsing
// src with a copy of p that keeps them, one backslash blanked a round, the
// first first: the lines after it may lex differently once it is gone.
func dropCommentBackslashes(p *syntax.Parser, src string) string {
	if !strings.Contains(src, "#") || !strings.Contains(src, "\\\n") {
		return src
	}
	q := *p // p's dialect; a parser is reset as each Parse starts
	syntax.KeepComments(true)(&q)
	for {
		f, err := q.Parse(strings.NewReader(src), "")
		if err != nil {
			return src
		}
		cut := -1
		syntax.Walk(f, func(n syntax.Node) bool {
			c, ok := n.(*syntax.Comment)
			if !ok || !strings.HasSuffix(c.Text, "\\\n") {
				return true
			}
			end := int(c.End().Offset()) //nolint:gosec // G115: an offset into src
			if end >= 2 && end <= len(src) && src[end-2:end] == "\\\n" && (cut < 0 || end-2 < cut) {
				cut = end - 2
			}
			return true
		})
		if cut < 0 {
			return src
		}
		src = src[:cut] + " " + src[cut+1:]
	}
}

// Stmts parses the statements r holds one at a time with p, calling fn with
// each top-level one until it returns false, and turning a parser panic (not
// one in fn) into an error. The parser reads r only as far as it needs.
func Stmts(p *syntax.Parser, r io.Reader, fn func(*syntax.Stmt) bool) (err error) {
	inFn := false // a panic in fn is the caller's: it goes through
	defer func() {
		if inFn {
			return
		}
		if r := recover(); r != nil {
			err = fmt.Errorf("shell parser failed: %v", r)
		}
	}()
	for stmt, err := range p.StmtsSeq(r) {
		if err != nil {
			return err
		}
		inFn = true
		goOn := fn(stmt)
		inFn = false
		if !goOn {
			break
		}
	}
	return nil
}
