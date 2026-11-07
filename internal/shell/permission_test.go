package shell

import (
	"testing"
)

func TestPermissionAnswerForKey(t *testing.T) {
	tests := []struct {
		name string
		key  byte
		want permissionAnswer
	}{
		{name: "allow lowercase y", key: 'y', want: permissionAllow},
		{name: "allow uppercase y", key: 'Y', want: permissionAllow},
		{name: "deny carriage return", key: '\r', want: permissionDeny},
		{name: "deny newline", key: '\n', want: permissionDeny},
		{name: "allow always lowercase a", key: 'a', want: permissionAlwaysAllow},
		{name: "allow always uppercase a", key: 'A', want: permissionAlwaysAllow},
		{name: "deny lowercase n", key: 'n', want: permissionDeny},
		{name: "deny escape", key: 0x1b, want: permissionDeny},
		{name: "cancel ctrl+c", key: 0x03, want: permissionCancel},
		{name: "deny unknown", key: 'x', want: permissionDeny},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := permissionAnswerForKey(tt.key); got != tt.want {
				t.Fatalf("permissionAnswerForKey(%q) = %v, want %v", tt.key, got, tt.want)
			}
		})
	}
}
