package executor

import "syscall"

// ignoredByDefault reports a signal, beyond those both systems share, that a
// process ignores unless it handles it: none on linux (no SIGINFO).
func ignoredByDefault(syscall.Signal) bool { return false }
