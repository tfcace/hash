package history

import (
	"sort"
	"strings"
	"sync"
)

// prefixIndex keeps the distinct successful commands from history in memory,
// sorted by command text, so the per-keystroke ghost-text lookup never touches
// SQLite. The GROUP BY query it replaces aggregates every historical
// occurrence of every matching command, which grows without bound under the
// default unlimited-retention config; a lookup here is one binary search plus
// a scan of the matching range.
//
// Recency is a session-local sequence number, not the stored timestamp: the
// loader hands over commands most-recent-first and numbers them downward from
// -1, while live additions count up from 0. Anything executed in this session
// therefore outranks anything loaded from disk, and no timestamp parsing is
// needed. Commands written to the shared history database by other
// concurrently running shells only appear after the next install; a
// hook-triggered refresh is planned (see TODO.md, cross-shell freshness).
type prefixIndex struct {
	mu      sync.RWMutex
	loaded  bool
	entries []prefixEntry // sorted ascending by cmd, one per distinct command
	nextSeq int64         // sequence for the next live addition
}

type prefixEntry struct {
	cmd string
	seq int64 // higher = more recent
}

// record notes that cmd just ran successfully. Safe to call before install:
// entries recorded during the initial load are merged back as the newest.
func (x *prefixIndex) record(cmd string) {
	if cmd == "" {
		return
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	x.upsertLocked(cmd, x.nextSeq)
	x.nextSeq++
}

// install replaces the loaded entries with cmds (distinct, most recent
// first), keeping anything recorded live in the meantime as more recent.
func (x *prefixIndex) install(cmds []string) {
	entries := make([]prefixEntry, 0, len(cmds))
	for i, cmd := range cmds {
		entries = append(entries, prefixEntry{cmd: cmd, seq: int64(-1 - i)})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].cmd < entries[j].cmd })

	x.mu.Lock()
	defer x.mu.Unlock()
	live := x.entries
	x.entries = entries
	for _, e := range live {
		x.upsertLocked(e.cmd, e.seq)
	}
	x.loaded = true
}

// upsertLocked inserts cmd at its sorted position, or raises an existing
// entry's sequence. Callers must hold mu.
func (x *prefixIndex) upsertLocked(cmd string, seq int64) {
	i := sort.Search(len(x.entries), func(i int) bool { return x.entries[i].cmd >= cmd })
	if i < len(x.entries) && x.entries[i].cmd == cmd {
		if seq > x.entries[i].seq {
			x.entries[i].seq = seq
		}
		return
	}
	x.entries = append(x.entries, prefixEntry{})
	copy(x.entries[i+1:], x.entries[i:])
	x.entries[i] = prefixEntry{cmd: cmd, seq: seq}
}

// search returns up to limit commands with the given prefix, most recent
// first. ok is false until install has run; callers fall back to SQL.
func (x *prefixIndex) search(prefix string, limit int) (results []string, ok bool) {
	x.mu.RLock()
	defer x.mu.RUnlock()
	if !x.loaded {
		return nil, false
	}
	if prefix == "" || limit <= 0 {
		return nil, true
	}

	// best holds the most recent matches seen so far, newest first.
	var best []prefixEntry
	i := sort.Search(len(x.entries), func(i int) bool { return x.entries[i].cmd >= prefix })
	for ; i < len(x.entries) && strings.HasPrefix(x.entries[i].cmd, prefix); i++ {
		e := x.entries[i]
		if len(best) == limit && best[len(best)-1].seq >= e.seq {
			continue
		}
		pos := len(best)
		for pos > 0 && best[pos-1].seq < e.seq {
			pos--
		}
		if len(best) < limit {
			best = append(best, prefixEntry{})
		}
		copy(best[pos+1:], best[pos:])
		best[pos] = e
	}
	if len(best) == 0 {
		return nil, true
	}
	results = make([]string, len(best))
	for i, e := range best {
		results[i] = e.cmd
	}
	return results, true
}
