package executor

import (
	"flag"
	"os"
	"strconv"
	"testing"
)

// maxParallel is how many of the package's tests run side by side. They
// start real programs and time them, so a machine's every core running
// one starves the ones that measure a wait.
const maxParallel = 4

func TestMain(m *testing.M) {
	flag.Parse()
	if f := flag.Lookup("test.parallel"); f != nil {
		if n, err := strconv.Atoi(f.Value.String()); err == nil && n > maxParallel {
			_ = f.Value.Set(strconv.Itoa(maxParallel))
		}
	}
	os.Exit(m.Run())
}
