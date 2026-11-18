//go:build !darwin

package executor

// sigintPosted is nil where the system cannot say that a SIGINT was sent
// before Go has delivered it. Linux has no such count for a signal that
// is not blocked (signalfd needs it blocked, which Go's runtime does not
// allow), so there the timing of lateSIGINT is all there is.
func sigintPosted() func() bool { return nil }
