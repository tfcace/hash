package shell

import "testing"

func TestPermissionDecisionForKey(t *testing.T) {
	tests := []struct {
		name       string
		key        byte
		wantAllow  bool
		wantAlways bool
	}{
		{name: "allow lowercase y", key: 'y', wantAllow: true, wantAlways: false},
		{name: "allow uppercase y", key: 'Y', wantAllow: true, wantAlways: false},
		{name: "allow carriage return", key: '\r', wantAllow: true, wantAlways: false},
		{name: "allow newline", key: '\n', wantAllow: true, wantAlways: false},
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
