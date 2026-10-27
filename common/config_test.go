package common

import "testing"

func TestNormalizeMultipleDeviceIDs(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "always", value: "always", want: "always"},
		{name: "legacy true", value: "true", want: "always"},
		{name: "legacy yes", value: "yes", want: "always"},
		{name: "same IP", value: "SameIPAddress", want: "SameIPAddress"},
		{name: "never", value: "never", want: "never"},
		{name: "default", value: "", want: "never"},
		{name: "unknown", value: "invalid", want: "never"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeMultipleDeviceIDs(tt.value); got != tt.want {
				t.Fatalf("normalizeMultipleDeviceIDs(%q) = %q, want %q", tt.value, got, tt.want)
			}
		})
	}
}
