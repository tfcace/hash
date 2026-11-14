package editor

import (
	"os"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// select(2) takes a fixed 1024-bit set: a terminal on a higher descriptor
// (a shell with many files open) used to panic the reader.
func TestHasDataAvailable_HighDescriptor(t *testing.T) {
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pr.Close()
	defer pw.Close()
	const high = 1500
	if err := unix.Dup2(int(pr.Fd()), high); err != nil {
		t.Skipf("cannot open descriptor %d: %v", high, err)
	}
	defer unix.Close(high)

	if hasDataAvailableWithTimeout(high, 0) {
		t.Fatal("empty pipe reported readable")
	}
	pw.WriteString("x")
	if !hasDataAvailableWithTimeout(high, 100*time.Millisecond) {
		t.Fatal("written pipe not reported readable")
	}
}
