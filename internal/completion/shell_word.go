package completion

import (
	"regexp"
	"strings"

	"github.com/tfcace/hash/internal/shlex"
)

// commandWords is the simple command the cursor is in, split into words the
// way the shell would: a new command starts after an unquoted |, ||, &, &&,
// ;, (, ) or newline, and inside $( ), backticks, <( ) and >( ), whose
// closing byte goes back to the command around them with the word that holds
// the substitution still going ("$(pwd)/x" is one word). Words split on
// unquoted whitespace (not inside $(( )) arithmetic or ${ }), and quotes and escapes
// are removed. Redirections (2>&1, >out, "> out", &>log, >|out) are not
// words: their & or | does not end the command, and neither they nor their
// targets are arguments. Leading NAME=value assignments and reserved words
// like then, do, ! and time are not the command name either.
type commandWords struct {
	// words are the complete words before the cursor's word; words[0] is
	// the command name.
	words []string
	// current is the word being completed, "" right after whitespace or an
	// operator.
	current string
	// currentStart is the byte offset in the line where current's raw text
	// starts (the cursor position when current is "").
	currentStart int
	// position is what current is to the command.
	position wordPosition
}

// wordPosition is what the word being completed is to its command.
type wordPosition int

const (
	positionCommand        wordPosition = iota // the command name: words is empty
	positionArgument                           // an argument of words[0]
	positionRedirectTarget                     // a redirection's target ("> out", "2>log"): a path, whatever the command
	positionText                               // a word of a comment or heredoc body: text no command reads as an argument, so only a path
)

// redirectionWord matches the start of a raw word that is a redirection: an
// optional fd number and an unquoted operator, with the target glued on
// ("2>&1", ">out") or, when the match is the whole word, in the next word.
// A here-string's word (<<< word) is its target too.
var redirectionWord = regexp.MustCompile(`^\d*(&>>?|>\||>>?&?|<<<|<<?-?|<>|<&)`)

// commandPrefixWords are the reserved words (and time) that a command name
// follows, when unquoted in command position.
var commandPrefixWords = map[string]bool{
	"if": true, "then": true, "else": true, "elif": true, "while": true, "until": true,
	"do": true, "!": true, "{": true, "time": true,
}

// assignmentWord matches a raw word that assigns a variable (NAME=value,
// NAME+=value, NAME[index]=value), which before the command name is not the
// command.
var assignmentWord = regexp.MustCompile(`^[A-Za-z_]\w*(\[[^]]*\])?\+?=`)

func parseCommandWords(line string, pos int) commandWords {
	pos = clampCursor(line, pos)
	var s commandScanner
	for i := 0; i < pos; i++ {
		s.step(line, i)
	}
	return s.result(line, pos)
}

// command returns the command name, or "" while it is still being typed.
func (cw commandWords) command() string {
	if len(cw.words) == 0 {
		return ""
	}
	return cw.words[0]
}

// args returns the complete arguments before the current word.
func (cw commandWords) args() []string {
	if len(cw.words) == 0 {
		return nil
	}
	return cw.words[1:]
}

// inCommandPosition reports whether the current word is the command name.
func (cw commandWords) inCommandPosition() bool {
	return cw.position == positionCommand
}

// inArgumentPosition reports whether the current word is an argument of the
// command, which a completer for that command may answer.
func (cw commandWords) inArgumentPosition() bool {
	return cw.position == positionArgument
}

func clampCursor(line string, pos int) int {
	if pos < 0 {
		return 0
	}
	if pos > len(line) {
		return len(line)
	}
	return pos
}

// commandScanner splits a line into commands and words, one byte at a time.
// A substitution ($( ), backticks, <( ), >( )) holds commands of its own
// inside one word of the command around it: where it opens, that command is
// suspended on outer, and where it closes (the lexer says where), it resumes
// with its word still in progress, so the word after "$(date)" is an
// argument, not a command name.
type commandScanner struct {
	lex   shlex.Lexer
	cur   command   // the command the next byte is in
	outer []command // the commands cur is nested in, innermost last
}

// command is the simple command being read: its complete words and the
// word in progress.
type command struct {
	words     []string
	word      wordKind
	wordStart int // where the word in progress starts, unless word is wordNone
	// targetNext: the last word was a bare redirection operator ("> out"),
	// so the next word is its target, not an argument.
	targetNext bool
}

// wordKind is what the scanner is in the middle of.
type wordKind int

const (
	wordNone wordKind = iota // between words
	wordText                 // a word, quotes and escapes included
	wordRaw                  // a word of a comment or heredoc body, kept raw (neither has quotes)
)

// step reads line[i], the byte after the last one it was given.
func (s *commandScanner) step(line string, i int) {
	c := line[i]
	substitutions, expr, glued := s.lex.Substitutions(), s.lex.InExpression(), s.lex.GluesRedirection(c)
	class := s.lex.Step(c)
	switch after := s.lex.Substitutions(); {
	case after > substitutions:
		s.open(i)
	case after < substitutions:
		s.resume()
	case class == shlex.Comment || class == shlex.Heredoc:
		s.text(c, i)
	case isBlankByte(c) && class == shlex.Code && !expr:
		s.cur.endWord(line, i)
	case class != shlex.Code || expr:
		// Quoted text, escapes, and arithmetic or ${ }, where blanks and
		// operators are part of the expression.
		s.cur.extendWord(i, wordText)
	default:
		s.code(line, i, glued)
	}
}

// code reads an unquoted byte where commands are read; glued says the
// lexer read it as part of the redirection operator before it.
func (s *commandScanner) code(line string, i int, glued bool) {
	switch c := line[i]; {
	case partOfRedirection(line, i, glued):
		if s.cur.word == wordText && redirectionEndsWord(line[s.cur.wordStart:i]) {
			s.cur.endWord(line, i) // "hi>out": hi is an argument, >out a redirection
		}
		s.cur.extendWord(i, wordText)
	case strings.IndexByte("()|&;\n", c) >= 0:
		// A subshell's ( or ), or a case pattern's ), starts a new command
		// like any other separator.
		s.cur = command{}
	default:
		s.cur.extendWord(i, wordText)
	}
}

// partOfRedirection reports whether line[i] belongs to a redirection
// operator: < and >, the & of 2>&1, >&2, <&0 and &>file, and the | of >|
// (glued: the lexer read it as part of the < or > before it).
func partOfRedirection(line string, i int, glued bool) bool {
	switch line[i] {
	case '<', '>':
		return true
	case '&':
		return glued || (i+1 < len(line) && line[i+1] == '>')
	case '|':
		return glued // >| writes the file even under noclobber
	}
	return false
}

// text reads c, the byte at line[i] of a comment or heredoc body: its words
// belong to no command. Both end at a newline (a comment in backticks at
// the closing one), where a new command starts anyway.
func (s *commandScanner) text(c byte, i int) {
	if isBlankByte(c) || c == '\n' {
		s.cur = command{}
		return
	}
	s.cur.extendWord(i, wordRaw)
}

// open suspends the current command at line[i], the byte that opens a
// substitution inside its word in progress, and starts the substitution's
// own command.
func (s *commandScanner) open(i int) {
	s.cur.extendWord(i, wordText) // a backtick starts the word
	s.outer = append(s.outer, s.cur)
	s.cur = command{}
}

// resume ends the innermost substitution and goes back to the command
// around it, whose word goes on. The lexer opens and closes one
// substitution per byte, so outer has one command per open substitution.
func (s *commandScanner) resume() {
	s.cur, s.outer = s.outer[len(s.outer)-1], s.outer[:len(s.outer)-1]
}

// extendWord adds line[i] to the word in progress, starting one of kind
// there if none is.
func (c *command) extendWord(i int, kind wordKind) {
	if c.word == wordNone {
		c.word, c.wordStart = kind, i
	}
}

// endWord ends the word in progress, if any, at line[i].
func (c *command) endWord(line string, i int) {
	if c.word == wordNone {
		return
	}
	raw := line[c.wordStart:i]
	switch op := redirection(raw); {
	case c.targetNext:
		c.targetNext = false
	case op != "":
		// A redirection, its target glued on or in the next word.
		c.targetNext = op == raw
	case len(c.words) == 0 && (assignmentWord.MatchString(raw) || commandPrefixWords[raw]):
		// FOO=1 git ..., then git ..., time git ...: the command comes
		// after it.
	default:
		c.words = append(c.words, shlex.Unquote(raw))
	}
	c.word = wordNone
}

// result is the commandWords for a cursor at pos, the end of what was read.
// A redirection's target glued to its operator (">out", "2>log") is the
// word being completed without the operator, which stays in front of it.
func (s *commandScanner) result(line string, pos int) commandWords {
	c := s.cur
	cw := commandWords{words: c.words, currentStart: pos, position: positionArgument}
	raw := ""
	if c.word != wordNone {
		raw, cw.currentStart = line[c.wordStart:pos], c.wordStart
	}
	switch {
	case s.lex.Context().Text != nil:
		cw.position = positionText
	case c.targetNext:
		cw.position = positionRedirectTarget
	case c.word == wordText && redirection(raw) != "":
		cw.position = positionRedirectTarget
		op := redirection(raw)
		raw, cw.currentStart = raw[len(op):], cw.currentStart+len(op)
	case len(c.words) == 0:
		cw.position = positionCommand
	}
	cw.current = raw
	if c.word == wordText {
		cw.current = shlex.Unquote(raw)
	}
	return cw
}

// redirectionEndsWord reports whether a redirection operator right after
// raw, the word in progress, ends it: unless raw is the operator's fd
// ("2>") or its own start (">>", "&>").
func redirectionEndsWord(raw string) bool {
	return strings.Trim(raw, "0123456789") != "" && raw != "&" && redirection(raw) == ""
}

// redirection returns the operator (with its fd) that raw, an unquoted
// word, starts with when it is a redirection, or "". <(ls) and >(cat) are
// process substitutions: arguments, not redirections.
func redirection(raw string) string {
	op := redirectionWord.FindString(raw)
	if strings.HasPrefix(raw[len(op):], "(") {
		return ""
	}
	return op
}

func isBlankByte(c byte) bool {
	return c == ' ' || c == '\t'
}
