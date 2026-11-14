package executor

import (
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// quoteEscapedChars makes each backslash outside quotes in a word the
// shell takes as one string (oneStringWords) quote the character after it
// with quotes instead: `a\ b` becomes `a' 'b`. The interpreter drops such a
// backslash only from a command's arguments; everywhere else it kept it
// (`dir=My\ Documents` held My\ Documents, `> a\ b` wrote a file named
// a\ b). A word in double quotes, or a heredoc, keeps its backslashes, as
// in bash.
func quoteEscapedChars(prog *syntax.File) {
	inQuotes := map[*syntax.Word]bool{} // the words of ${x:-word} in double quotes
	syntax.Walk(prog, func(node syntax.Node) bool {
		switch n := node.(type) {
		case *syntax.DblQuoted:
			markQuotedExpansions(n.Parts, inQuotes)
		case *syntax.Redirect:
			if n.Hdoc != nil { // a heredoc's body is as in double quotes
				markQuotedExpansions(n.Hdoc.Parts, inQuotes)
			}
		}
		for _, w := range oneStringWords(node) {
			if !inQuotes[w] {
				w.Parts = quoteEscapes(w.Parts)
			}
		}
		return true
	})
}

// markQuotedExpansions marks the words of the parameter expansions in
// parts, which double quotes hold, and of those in them, as in quotes.
func markQuotedExpansions(parts []syntax.WordPart, inQuotes map[*syntax.Word]bool) {
	for _, part := range parts {
		pe, ok := part.(*syntax.ParamExp)
		if !ok {
			continue
		}
		for _, w := range expansionWords(pe) {
			inQuotes[w] = true
			markQuotedExpansions(w.Parts, inQuotes)
		}
	}
}

// oneStringWords are the words node holds that the shell expands to one
// string, with no splitting or globbing, and that are no pattern.
func oneStringWords(node syntax.Node) []*syntax.Word {
	var words []*syntax.Word
	add := func(w *syntax.Word) {
		if w != nil {
			words = append(words, w)
		}
	}
	switch n := node.(type) {
	case *syntax.Assign:
		add(n.Value)
		if n.Array != nil {
			for _, elem := range n.Array.Elems {
				if elem.Index != nil {
					add(elem.Value)
				}
			}
		}
	case *syntax.Redirect:
		if n.Op != syntax.Hdoc && n.Op != syntax.DashHdoc {
			add(n.Word) // a heredoc's delimiter says whether its body expands
		}
	case *syntax.CaseClause:
		add(n.Word)
	case *syntax.ParamExp:
		words = expansionWords(n)
	default:
		for _, x := range testOperands(node) {
			if w, ok := x.(*syntax.Word); ok {
				words = append(words, w)
			}
		}
	}
	return words
}

// testOperands are the operands of the [[ test node that are no pattern
// or regexp, which keep their backslashes.
func testOperands(node syntax.Node) []syntax.TestExpr {
	switch n := node.(type) {
	case *syntax.TestClause:
		return []syntax.TestExpr{n.X}
	case *syntax.ParenTest:
		return []syntax.TestExpr{n.X}
	case *syntax.UnaryTest:
		return []syntax.TestExpr{n.X}
	case *syntax.BinaryTest:
		switch n.Op {
		case syntax.TsMatch, syntax.TsMatchShort, syntax.TsNoMatch, syntax.TsReMatch:
			return []syntax.TestExpr{n.X}
		}
		return []syntax.TestExpr{n.X, n.Y}
	}
	return nil
}

// expansionWords are the words of the parameter expansion pe that expand
// to a string, not a pattern: the word of ${x:-word} and its kind, the
// replacement of ${x/p/word}.
func expansionWords(pe *syntax.ParamExp) []*syntax.Word {
	var words []*syntax.Word
	if pe.Exp != nil && pe.Exp.Word != nil {
		switch pe.Exp.Op {
		case syntax.RemSmallPrefix, syntax.RemLargePrefix, syntax.RemSmallSuffix, syntax.RemLargeSuffix:
		default:
			words = append(words, pe.Exp.Word)
		}
	}
	if pe.Repl != nil && pe.Repl.With != nil {
		words = append(words, pe.Repl.With)
	}
	return words
}

// quoteEscapes is parts with each backslash in their unquoted literals
// replaced by quotes around the character it quotes; a backslash and a
// newline go, a backslash at the end stays.
func quoteEscapes(parts []syntax.WordPart) []syntax.WordPart {
	var out []syntax.WordPart
	for _, part := range parts {
		lit, ok := part.(*syntax.Lit)
		if !ok || !strings.Contains(lit.Value, `\`) {
			out = append(out, part)
			continue
		}
		s, plain := lit.Value, strings.Builder{}
		flush := func() {
			if plain.Len() > 0 {
				out = append(out, &syntax.Lit{ValuePos: lit.ValuePos, ValueEnd: lit.ValueEnd, Value: plain.String()})
				plain.Reset()
			}
		}
		for i := 0; i < len(s); i++ {
			if s[i] != '\\' || i+1 == len(s) {
				plain.WriteByte(s[i])
				continue
			}
			i++
			if s[i] == '\n' {
				continue
			}
			flush()
			out = append(out, quotedByte(lit, s[i]))
		}
		flush()
	}
	return out
}

// quotedByte is c, a character a backslash quoted in lit, in quotes.
func quotedByte(lit *syntax.Lit, c byte) syntax.WordPart {
	if c == '\'' {
		return &syntax.DblQuoted{Left: lit.ValuePos, Right: lit.ValueEnd, Parts: []syntax.WordPart{
			&syntax.Lit{ValuePos: lit.ValuePos, ValueEnd: lit.ValueEnd, Value: "'"},
		}}
	}
	return &syntax.SglQuoted{Left: lit.ValuePos, Right: lit.ValueEnd, Value: string(c)}
}
