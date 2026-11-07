package main

import (
	"bufio"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tfcace/hash/internal/executor"
	"github.com/tfcace/hash/internal/shlex"
)

// A script line goes on to the next where the lexer says the command does:
// after an escaping backslash, not one in a comment or single quotes.
func TestScriptLineContinues(t *testing.T) {
	tests := []struct {
		src  string
		want bool
	}{
		{"echo a\n", false},
		{"echo a \\\n", true},
		{"echo a \\\\\n", false},
		{"echo a \\\\\\\n", true},
		{"\\\n", true},
		{"echo a # note \\\n", false},
		{"echo 'a \\\n", true}, // the open quote goes on
	}
	for _, tt := range tests {
		var lex shlex.Lexer
		lex.Scan(tt.src)
		if got := lex.Continues(); got != tt.want {
			t.Errorf("Continues after %q = %v, want %v", tt.src, got, tt.want)
		}
	}
}

// countingFile counts the reads on a file.
type countingFile struct {
	*os.File
	reads int
}

func (c *countingFile) Read(p []byte) (int, error) {
	c.reads++
	return c.File.Read(p)
}

// A script file on stdin is read in blocks, and each line hands the rest of
// the file back: the offset after a line is right past it, for the commands
// that read stdin.
func TestStdinLines_FileReadsInBlocksAndGivesBackTheRest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.sh")
	body := "echo one two three four\nread x\nfrom-read\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cf := &countingFile{File: f}
	r, ok := newStdinLines(context.Background(), f).(*fileLines)
	if !ok {
		t.Fatal("a file on stdin is not read as a file")
	}
	r.f = cf
	var offset int64
	for _, want := range strings.SplitAfter(body, "\n")[:3] {
		got, err := r.ReadString('\n')
		if err != nil || got != want {
			t.Fatalf("ReadString = %q, %v; want %q", got, err, want)
		}
		offset += int64(len(want))
		if at, _ := f.Seek(0, io.SeekCurrent); at != offset {
			t.Fatalf("offset after %q = %d, want %d", want, at, offset)
		}
	}
	if cf.reads != 3 {
		t.Errorf("%d reads for 3 lines, want one a line", cf.reads)
	}
}

// A pipe on stdin is read a line at a time and no further, and a canceled
// context (a fatal signal) ends the wait for the next line.
func TestStdinLines_PipeReadsALineAndNoMore(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	if _, err := w.WriteString("one\ntwo\n"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	lines := newStdinLines(ctx, r)
	if _, ok := lines.(*pipeLines); !ok {
		t.Fatalf("a pipe on stdin is read as %T", lines)
	}
	if got, err := lines.ReadString('\n'); got != "one\n" || err != nil {
		t.Fatalf("ReadString = %q, %v; want one", got, err)
	}
	rest := make([]byte, 16)
	if n, _ := r.Read(rest); string(rest[:n]) != "two\n" {
		t.Fatalf("left in the pipe: %q, want two", rest[:n])
	}
	cancel()
	if _, err := lines.ReadString('\n'); err == nil {
		t.Fatal("ReadString waited on after its context ended")
	}
}

// A script's steps say whether it stopped on a fatal signal or otherwise
// (the end of its commands, an exit, an error that ends it: its status
// tells them apart).
func TestScript_StopReason(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	tests := []struct {
		name   string
		ctx    context.Context
		src    string
		want   stopReason
		status int
	}{
		{"end", context.Background(), "true\nfalse\n", stopped, 1},
		{"exit", context.Background(), "exit 3\ntrue\n", stopped, 3},
		{"syntax error", context.Background(), "true\nfi\n", stopped, 2},
		{"signal", canceled, "true\n", stopSignal, 0},
	}
	for _, tt := range tests {
		s := &script{exec: executor.New(), ctx: tt.ctx}
		if got := s.runLines(bufio.NewReader(strings.NewReader(tt.src))); got != tt.want || s.status != tt.status {
			t.Errorf("%s: runLines = %v, status %d; want %v, status %d", tt.name, got, s.status, tt.want, tt.status)
		}
	}
}
