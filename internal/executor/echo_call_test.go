package executor

import (
	"bytes"
	"context"
	"io"
	"testing"
)

// echo is one write of what echo prints, through the printf builtin: a
// function named printf the user defined does not take every echo.
func TestExecute_EchoIsNotAPrintfFunction(t *testing.T) {
	var out bytes.Buffer
	if _, err := New().Execute(context.Background(), `printf() { builtin printf 'mine\n'; }; echo hi; echo -n a; echo -E b`, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "hi\nab\n" {
		t.Errorf("echo with a printf function: %q, want %q", got, "hi\nab\n")
	}
}
