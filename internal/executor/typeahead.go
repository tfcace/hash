package executor

import "sync"

// typeahead is what the PTY commands of finished lines left unread of the
// keys typed while they ran, in the order typed: the next prompt's, as in
// bash, where they stay on the terminal. Every line adds to it, whoever
// ran it (a line the user typed, a hook, an init command, a startup
// file), and the shell takes it once before it reads each prompt line
// (TakeUnread), so no caller hands the keys on, and none can drop them.
type typeahead struct {
	mu   sync.Mutex
	keys []byte
}

// keep adds the keys a line's PTY commands left unread.
func (t *typeahead) keep(keys []byte) {
	if len(keys) == 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.keys = append(t.keys, keys...)
}

// take takes every key kept so far.
func (t *typeahead) take() []byte {
	t.mu.Lock()
	defer t.mu.Unlock()
	keys := t.keys
	t.keys = nil
	return keys
}

// TakeUnread takes the keys the lines run so far left unread (typed while
// one of their commands ran on a PTY, and not read by it), as the terminal
// queued them: the next prompt reads them before what is still on the
// terminal. It returns nil when there are none.
func (e *Executor) TakeUnread() []byte {
	return e.typeahead.take()
}
