package programstatus

import (
	"bytes"
	"strings"
	"testing"
)

// Bodies from the specification's examples; the comments give the decoded
// base64.
func TestEncode_SpecExamples(t *testing.T) {
	tests := []struct {
		name string
		r    Report
		want string
	}{
		{"working", Report{State: Working, App: "brew", Msg: "Installing updates"},
			"state=working:app=brew:msg=SW5zdGFsbGluZyB1cGRhdGVz"},
		{"blocked auth", Report{State: Blocked, Kind: Auth, App: "brew", Msg: "Password required to install updates"},
			"state=blocked:kind=auth:app=brew:msg=UGFzc3dvcmQgcmVxdWlyZWQgdG8gaW5zdGFsbCB1cGRhdGVz"},
		{"child with title", Report{State: Working, ID: "us-east", Title: "US East", Msg: "Pushing image"},
			"state=working:id=us-east:title=VVMgRWFzdA==:msg=UHVzaGluZyBpbWFnZQ=="},
		{"clear carries only its id", Report{State: Clear, ID: "us-east", App: "deploy", Msg: "ignored"},
			"state=clear:id=us-east"},
		{"kind dropped outside blocked", Report{State: Done, Kind: Permission, Msg: "ok"},
			"state=done:msg=b2s="},
		{"bare state", Report{State: Idle}, "state=idle"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Encode(tt.r); got != tt.want {
				t.Errorf("Encode() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSequence_WrapsInOSC7501(t *testing.T) {
	got := Sequence(Report{State: Idle})
	if got != "\x1b]7501;state=idle\x1b\\" {
		t.Errorf("Sequence() = %q", got)
	}
}

func TestFitText(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		limit int
		want  string
	}{
		{"control characters become spaces", "a\tb\x1b[31mc\x7f", 100, "a b [31mc "},
		{"invalid utf-8 dropped", "ok\xffok", 100, "okok"},
		{"cut on a rune boundary", "héllo", 2, "h"},
		{"fits exactly", "héllo", 6, "héllo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FitText(tt.in, tt.limit); got != tt.want {
				t.Errorf("FitText(%q, %d) = %q, want %q", tt.in, tt.limit, got, tt.want)
			}
		})
	}
}

func TestFitLine_CollapsesWhitespaceToOneLine(t *testing.T) {
	got := FitLine("  make \n\n  -j8\tall  ", MaxMsgDecoded)
	if got != "make -j8 all" {
		t.Errorf("FitLine() = %q", got)
	}
}

func TestFitSegment(t *testing.T) {
	if got := FitSegment("my tool/v2", MaxApp); got != "my-tool-v2" {
		t.Errorf("FitSegment() = %q", got)
	}
	if got := FitSegment("", MaxApp); got != "-" {
		t.Errorf("FitSegment(\"\") = %q, want -", got)
	}
	if got := FitSegment(strings.Repeat("a", 40), MaxApp); len(got) != MaxApp {
		t.Errorf("FitSegment() len = %d, want %d", len(got), MaxApp)
	}
}

func TestReporter_FitsFieldsBeforeWriting(t *testing.T) {
	var buf bytes.Buffer
	r := NewReporter(&buf)
	r.Report(Report{State: Working, App: "my tool", Msg: "line one\nline two"})
	want := "\x1b]7501;state=working:app=my-tool:msg=bGluZSBvbmUgbGluZSB0d28=\x1b\\"
	if buf.String() != want {
		t.Errorf("wrote %q, want %q", buf.String(), want)
	}
}

func TestReporter_NilAndDisabledWriteNothing(t *testing.T) {
	var nilReporter *Reporter
	nilReporter.Report(Report{State: Working}) // must not panic
	if nilReporter.Enabled() {
		t.Error("a nil Reporter should not report itself enabled")
	}

	var buf bytes.Buffer
	r := NewReporter(&buf)
	r.SetEnabled(false)
	r.Report(Report{State: Working})
	if buf.Len() != 0 {
		t.Errorf("disabled reporter wrote %q", buf.String())
	}
	if r.Enabled() {
		t.Error("Enabled() should be false after SetEnabled(false)")
	}
}
