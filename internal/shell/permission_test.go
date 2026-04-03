package shell

import (
	"testing"
	"time"
)

func TestPermissionDecisionForKey(t *testing.T) {
	tests := []struct {
		name       string
		key        byte
		wantAllow  bool
		wantAlways bool
	}{
		{name: "allow lowercase y", key: 'y', wantAllow: true, wantAlways: false},
		{name: "allow uppercase y", key: 'Y', wantAllow: true, wantAlways: false},
		{name: "deny carriage return", key: '\r', wantAllow: false, wantAlways: false},
		{name: "deny newline", key: '\n', wantAllow: false, wantAlways: false},
		{name: "allow always lowercase a", key: 'a', wantAllow: true, wantAlways: true},
		{name: "allow always uppercase a", key: 'A', wantAllow: true, wantAlways: true},
		{name: "deny lowercase n", key: 'n', wantAllow: false, wantAlways: false},
		{name: "deny escape", key: 0x1b, wantAllow: false, wantAlways: false},
		{name: "deny unknown", key: 'x', wantAllow: false, wantAlways: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotAllow, gotAlways := permissionDecisionForKey(tt.key)
			if gotAllow != tt.wantAllow || gotAlways != tt.wantAlways {
				t.Fatalf(
					"permissionDecisionForKey(%q) = (allow=%v, always=%v), want (allow=%v, always=%v)",
					tt.key,
					gotAllow,
					gotAlways,
					tt.wantAllow,
					tt.wantAlways,
				)
			}
		})
	}
}

func TestReadSingleKeyWithHooks_SkipsStaleNewlines(t *testing.T) {
	// Simulate: drain clears buffer, but a stale \n arrives before
	// the real keypress. readSingleKeyWithHooks should skip the \n
	// and return the real key.
	callCount := 0
	read := func(buf []byte) (int, error) {
		callCount++
		switch callCount {
		case 1:
			buf[0] = '\n' // stale newline
		case 2:
			buf[0] = '\r' // another stale CR
		default:
			buf[0] = 'y' // real keypress
		}
		return 1, nil
	}

	drain := func(int) {}
	sleep := func(time.Duration) {}

	got := readSingleKeyWithHooks(0, read, drain, sleep)
	if got != 'y' {
		t.Fatalf("readSingleKeyWithHooks() = %q, want %q", got, 'y')
	}
	if callCount != 3 {
		t.Fatalf("read called %d times, want 3 (2 stale newlines + real key)", callCount)
	}
}

func TestReadSingleKeyWithHooks_DrainsLateSubmitNewline(t *testing.T) {
	var queue []byte

	read := func(buf []byte) (int, error) {
		if len(queue) == 0 {
			queue = append(queue, 'y')
		}
		buf[0] = queue[0]
		queue = queue[1:]
		return 1, nil
	}

	drainCalls := 0
	drain := func(int) {
		drainCalls++
		queue = nil
	}

	sleep := func(time.Duration) {
		queue = append(queue, '\n')
	}

	got := readSingleKeyWithHooks(0, read, drain, sleep)
	if got != 'y' {
		t.Fatalf("readSingleKeyWithHooks() = %q, want %q", got, 'y')
	}
	if drainCalls != 2 {
		t.Fatalf("drain called %d times, want 2", drainCalls)
	}
}

func TestReadSingleKeyWithHooks_EscapeThroughNewlines(t *testing.T) {
	// ESC should be returned even after stale newlines.
	callCount := 0
	read := func(buf []byte) (int, error) {
		callCount++
		if callCount == 1 {
			buf[0] = '\n'
		} else {
			buf[0] = 0x1b
		}
		return 1, nil
	}

	got := readSingleKeyWithHooks(0, read, func(int) {}, func(time.Duration) {})
	if got != 0x1b {
		t.Fatalf("readSingleKeyWithHooks() = %q, want ESC", got)
	}
}
