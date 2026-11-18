package executor

import (
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

// sigintPosted returns a function that reports, and forgets, whether a
// SIGINT was sent to this process since it was last called; nil when the
// system cannot be asked. A kqueue signal filter counts a signal as it is
// sent, beside the handler Go has installed and before that handler has
// run: the one place a SIGINT is known ahead of Go's delivery of it.
func sigintPosted() func() bool {
	sigintQueue.once.Do(func() {
		sigintQueue.fd = -1
		kq, err := unix.Kqueue()
		if err != nil {
			return
		}
		unix.CloseOnExec(kq)
		watch := unix.Kevent_t{Ident: uint64(syscall.SIGINT), Filter: unix.EVFILT_SIGNAL, Flags: unix.EV_ADD}
		if _, err := unix.Kevent(kq, []unix.Kevent_t{watch}, nil, nil); err != nil {
			_ = unix.Close(kq)
			return
		}
		sigintQueue.fd = kq
	})
	if sigintQueue.fd < 0 {
		return nil
	}
	return func() bool {
		events := make([]unix.Kevent_t, 1)
		n, err := unix.Kevent(sigintQueue.fd, nil, events, &unix.Timespec{})
		return err == nil && n > 0 && events[0].Data > 0
	}
}

// sigintQueue is the process's one kqueue for SIGINT, made on first use.
var sigintQueue struct {
	once sync.Once
	fd   int
}
