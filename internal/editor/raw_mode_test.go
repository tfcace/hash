package editor

import (
	"context"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
	"golang.org/x/term"
)

// The editor puts the terminal its keys come from (its key reader's) in raw
// mode, whatever input it was made with: it used to set the mode on that
// second input, so keys read from a terminal reader stayed line-buffered
// and echoed, and a terminal passed beside a pipe reader went raw for
// nothing.
func TestEditor_RawModeIsTheKeyReadersTerminal(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Skipf("no pty: %v", err)
	}
	defer master.Close()
	defer slave.Close()
	go io.Copy(io.Discard, master) //nolint:errcheck // drains the echo until the test closes the pty
	fd := int(slave.Fd())
	cooked, err := term.GetState(fd)
	if err != nil {
		t.Fatal(err)
	}

	var duringRun *term.State
	ed := New(Config{
		Input:        NewInputReader(slave),
		OnInputReady: func() { duringRun, _ = term.GetState(fd) },
	}, strings.NewReader(""), io.Discard)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := master.WriteString("ls\r"); err != nil {
		t.Fatal(err)
	}
	res, err := ed.Run(ctx)
	if err != nil || res.Text != "ls" {
		t.Fatalf("Run = %+v, %v; want ls", res, err)
	}
	if duringRun == nil || reflect.DeepEqual(duringRun, cooked) {
		t.Error("the key reader's terminal stayed cooked while the editor ran")
	}
	if after, _ := term.GetState(fd); !reflect.DeepEqual(after, cooked) {
		t.Error("the terminal mode was not restored")
	}
}
