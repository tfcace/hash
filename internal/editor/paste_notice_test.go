package editor

import (
	"context"
	"os"
	"strings"
	"testing"
)

// A paste over the reader's paste size keeps its head; the user is told the rest was
// dropped instead of finding out from the command.
func TestRun_OversizedPasteSaysItWasCut(t *testing.T) {
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pr.Close()
	defer pw.Close()
	out := &screenBuf{}
	keys := NewInputReader(pr)
	keys.SetMaxPasteSize(4)
	ed := New(Config{Input: keys}, pr, out)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go ed.Run(ctx) //nolint:errcheck // canceled at the end of the test
	pw.WriteString("\x1b[200~abcdefgh\x1b[201~")
	eventually(t, func() bool { return strings.Contains(out.String(), "abcd") }, "paste never drawn")
	eventually(t, func() bool { return strings.Contains(out.lastFrame(), "paste cut") }, "no notice that the paste was cut")
}
