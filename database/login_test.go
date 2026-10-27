package database

import "testing"

func TestCanUseAdditionalDeviceID(t *testing.T) {
	lastIP := "192.0.2.10"
	tests := []struct {
		name   string
		policy string
		lastIP *string
		want   bool
	}{
		{name: "always allows", policy: "always", want: true},
		{name: "never rejects", policy: "never", lastIP: &lastIP, want: false},
		{name: "unknown rejects", policy: "invalid", lastIP: &lastIP, want: false},
		{name: "same IP allows", policy: "SameIPAddress", lastIP: &lastIP, want: true},
		{name: "missing IP rejects", policy: "SameIPAddress", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := canUseAdditionalDeviceID(tt.policy, tt.lastIP, lastIP); got != tt.want {
				t.Fatalf("canUseAdditionalDeviceID(%q) = %t, want %t", tt.policy, got, tt.want)
			}
		})
	}

	otherIP := "192.0.2.11"
	if canUseAdditionalDeviceID("SameIPAddress", &lastIP, otherIP) {
		t.Fatal("SameIPAddress allowed a different IP")
	}
}
