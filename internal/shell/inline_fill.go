package shell

import "strings"

// fillState is where an inline ?? fill is in the markdown a model may wrap
// it in, despite being asked not to: a one-backtick span (`internal/*.go`)
// or a code fence. Enter runs the accepted line as shell code, where a
// backtick pair is command substitution, so the wrapper must not reach the
// ghost text.
type fillState int

const (
	fillLead      fillState = iota // only whitespace so far
	fillTicks                      // a run of backticks opened the fill
	fillSpan                       // inside a one-backtick span: a wrapper if it closes at the end
	fillClosed                     // the span closed; anything but whitespace after it makes it a substitution
	fillFenceHead                  // a fence's opening line (its language tag), dropped
	fillFenceBody                  // inside a fence; trailing backticks and whitespace are held, the closing fence
	fillBody                       // plain text, passed through as it arrives
)

// inlineFill strips the markdown wrapper from an inline fill as it streams.
// Plain text passes through chunk by chunk. A fill that opens with a
// backtick is held until it is known to be a wrapper (its span closes at
// the end) or a substitution (more follows the span); a code fence's marks
// are dropped and its body streams.
type inlineFill struct {
	state fillState
	lead  string          // whitespace before the first mark
	ticks int             // backticks in the opening run
	held  strings.Builder // what the state holds back: a span, a fence's tag or trailing marks
	trail string          // whitespace after a closed span
}

// unwrapInlineFill is the fill a whole reply makes.
func unwrapInlineFill(reply string) string {
	var f inlineFill
	return f.write(reply) + f.end()
}

// write takes the next chunk and returns what can be shown of it now.
func (f *inlineFill) write(chunk string) string {
	var out strings.Builder
	for i := 0; i < len(chunk); i++ {
		f.step(chunk[i], &out)
	}
	return out.String()
}

func isFillSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n'
}

func (f *inlineFill) step(c byte, out *strings.Builder) {
	switch f.state {
	case fillLead:
		f.stepLead(c, out)
	case fillTicks:
		f.stepTicks(c, out)
	case fillSpan:
		if c == '`' {
			f.state = fillClosed
			return
		}
		f.held.WriteByte(c)
	case fillClosed:
		f.stepClosed(c, out)
	case fillFenceHead:
		if c == '\n' {
			f.held.Reset()
			f.state = fillFenceBody
			return
		}
		f.held.WriteByte(c)
	case fillFenceBody:
		if c == '`' || isFillSpace(c) {
			f.held.WriteByte(c)
			return
		}
		out.WriteString(f.held.String())
		out.WriteByte(c)
		f.held.Reset()
	case fillBody:
		out.WriteByte(c)
	}
}

func (f *inlineFill) stepLead(c byte, out *strings.Builder) {
	switch {
	case isFillSpace(c):
		f.lead += string(c)
	case c == '`':
		f.ticks, f.state = 1, fillTicks
	default:
		out.WriteString(f.lead)
		out.WriteByte(c)
		f.state = fillBody
	}
}

func (f *inlineFill) stepTicks(c byte, out *strings.Builder) {
	switch {
	case c == '`':
		f.ticks++
		if f.ticks == 3 {
			f.state = fillFenceHead
		}
	case f.ticks == 1:
		f.held.WriteByte(c)
		f.state = fillSpan
	default: // two backticks then text: no wrapper shape, keep it as is
		out.WriteString(f.lead + "``")
		out.WriteByte(c)
		f.state = fillBody
	}
}

// stepClosed is the byte after a closed span: whitespace may still end the
// fill, anything else makes the span a command substitution, kept whole.
func (f *inlineFill) stepClosed(c byte, out *strings.Builder) {
	if isFillSpace(c) {
		f.trail += string(c)
		return
	}
	out.WriteString(f.lead + "`" + f.held.String() + "`" + f.trail)
	out.WriteByte(c)
	f.held.Reset()
	f.state = fillBody
}

// end is called once the reply is complete and returns what is left to
// show: a wrapped fill's inside, without its marks.
func (f *inlineFill) end() string {
	switch f.state {
	case fillTicks:
		return f.lead + strings.Repeat("`", f.ticks)
	case fillSpan: // never closed: not a wrapper
		return f.lead + "`" + f.held.String()
	case fillClosed:
		return f.lead + f.held.String()
	case fillFenceHead: // a fence on one line: ```ls -a```
		return strings.TrimSpace(strings.TrimRight(f.held.String(), "`"))
	case fillFenceBody:
		tail := strings.TrimRight(f.held.String(), " \t\r\n")
		return strings.TrimRight(strings.TrimSuffix(tail, "```"), " \t\r\n")
	default:
		return ""
	}
}
