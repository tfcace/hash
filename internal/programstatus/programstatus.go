// Package programstatus is the program side of the Program Status Protocol
// (OSC 7501): https://www.superlogical.com/rex/docs/build/program-status
//
// A program writes OSC 7501 ; key=value:key=value ST to tell its terminal
// what it is doing: idle, working, done, blocked on the user, or failed,
// and why. Hash only writes reports; it never parses them. The encoder and
// the text fitting follow the progstatus package of TUIOS (MIT).
package programstatus

import (
	"encoding/base64"
	"io"
	"strings"
	"unicode/utf8"
)

// Limits from the specification. A terminal discards a report that breaks
// one, so a producer fits its fields before encoding.
const (
	MaxMsgDecoded   = 2048
	MaxTitleDecoded = 192
	MaxApp          = 32
	MaxSegment      = 32
)

// State is the state key of a report.
type State string

// The states of the specification. Clear is not a state a record can hold:
// it removes records.
const (
	Idle    State = "idle"
	Working State = "working"
	Done    State = "done"
	Blocked State = "blocked"
	Error   State = "error"
	Clear   State = "clear"
)

// Kind is what a blocked record waits for.
type Kind string

// The kinds of the specification.
const (
	Permission Kind = "permission"
	Question   Kind = "question"
	Auth       Kind = "auth"
)

// Report is one OSC 7501 report. Hash has no percentages to report, so
// there is no progress key: absent means indeterminate.
type Report struct {
	State State
	// ID is the record's id, empty for the root record.
	ID string
	// Kind is sent only with Blocked.
	Kind Kind
	// App is the program's own name, [A-Za-z0-9_.+-]{1,32}.
	App string
	// Title and Msg are free text; the encoder base64s them.
	Title string
	Msg   string
}

// Encode builds the body of a report, without OSC and ST: the pairs joined
// with ":", title and msg in standard base64. It does not fit the fields;
// Reporter does.
func Encode(r Report) string {
	pairs := []string{"state=" + string(r.State)}
	if r.ID != "" {
		pairs = append(pairs, "id="+r.ID)
	}
	if r.State == Clear {
		return strings.Join(pairs, ":")
	}
	if r.Kind != "" && r.State == Blocked {
		pairs = append(pairs, "kind="+string(r.Kind))
	}
	if r.App != "" {
		pairs = append(pairs, "app="+r.App)
	}
	if r.Title != "" {
		pairs = append(pairs, "title="+base64.StdEncoding.EncodeToString([]byte(r.Title)))
	}
	if r.Msg != "" {
		pairs = append(pairs, "msg="+base64.StdEncoding.EncodeToString([]byte(r.Msg)))
	}
	return strings.Join(pairs, ":")
}

// Sequence is Encode wrapped in OSC 7501 and ESC \.
func Sequence(r Report) string {
	return "\x1b]7501;" + Encode(r) + "\x1b\\"
}

// IsControl reports whether r is a control character as the specification
// defines one: U+0000 to U+001F, U+007F and U+0080 to U+009F.
func IsControl(r rune) bool {
	return r <= 0x1f || (r >= 0x7f && r <= 0x9f)
}

// FitText makes s fit a free-text value: control characters become spaces,
// invalid UTF-8 is dropped, and the text is cut on a rune boundary to limit
// bytes.
func FitText(s string, limit int) string {
	var b strings.Builder
	for _, r := range s {
		if r == utf8.RuneError {
			continue
		}
		if IsControl(r) {
			r = ' '
		}
		if b.Len()+utf8.RuneLen(r) > limit {
			break
		}
		b.WriteRune(r)
	}
	return b.String()
}

// FitLine makes s one line of at most limit bytes: runs of whitespace,
// newlines included, become one space.
func FitLine(s string, limit int) string {
	return FitText(strings.Join(strings.Fields(s), " "), limit)
}

// FitSegment makes s a valid id segment or app value: bytes outside
// [A-Za-z0-9_.+-] become "-", and the result is cut to limit bytes. An
// empty result becomes "-".
func FitSegment(s string, limit int) string {
	b := []byte(s)
	for i := range b {
		if !segmentByte(b[i]) {
			b[i] = '-'
		}
	}
	if len(b) > limit {
		b = b[:limit]
	}
	if len(b) == 0 {
		return "-"
	}
	return string(b)
}

func segmentByte(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	}
	return c == '_' || c == '.' || c == '+' || c == '-'
}

// Reporter writes reports to a terminal. A nil Reporter writes nothing, so
// a caller never has to check for one.
type Reporter struct {
	w       io.Writer
	enabled bool
}

// NewReporter returns a Reporter writing to w, enabled.
func NewReporter(w io.Writer) *Reporter {
	return &Reporter{w: w, enabled: true}
}

// SetEnabled turns reporting on or off.
func (r *Reporter) SetEnabled(enabled bool) {
	if r != nil {
		r.enabled = enabled
	}
}

// Enabled reports whether Report writes anything.
func (r *Reporter) Enabled() bool {
	return r != nil && r.enabled && r.w != nil
}

// Report fits the free-text and app fields of rep to the specification's
// limits and writes the sequence.
func (r *Reporter) Report(rep Report) {
	if !r.Enabled() {
		return
	}
	rep.Msg = FitLine(rep.Msg, MaxMsgDecoded)
	rep.Title = FitLine(rep.Title, MaxTitleDecoded)
	if rep.App != "" {
		rep.App = FitSegment(rep.App, MaxApp)
	}
	io.WriteString(r.w, Sequence(rep)) //nolint:errcheck // terminal escapes are best effort
}
