package executor

import "syscall"

// ignoredByDefault reports a signal, beyond those both systems share, that a
// process ignores unless it handles it: SIGINFO (Ctrl+T) on darwin.
func ignoredByDefault(sig syscall.Signal) bool { return sig == syscall.SIGINFO }
