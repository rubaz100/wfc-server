package common

import "testing"

func TestOnlineCPUConfigDefaultsAndValidation(t *testing.T) {
	for _, test := range []struct {
		name    string
		fields  string
		enabled bool
		count   int
		invalid bool
	}{
		{name: "absent settings", count: 4},
		{name: "explicit off", fields: "<onlineCPUEnabled>false</onlineCPUEnabled><onlineCPUCount>1</onlineCPUCount>", count: 1},
		{name: "enabled maximum", fields: "<onlineCPUEnabled>true</onlineCPUEnabled><onlineCPUCount>11</onlineCPUCount>", enabled: true, count: 11},
		{name: "zero", fields: "<onlineCPUCount>0</onlineCPUCount>", invalid: true},
		{name: "negative", fields: "<onlineCPUCount>-1</onlineCPUCount>", invalid: true},
		{name: "too many", fields: "<onlineCPUCount>12</onlineCPUCount>", invalid: true},
		{name: "fraction", fields: "<onlineCPUCount>4.5</onlineCPUCount>", invalid: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			useTemporaryConfig(t, "<Config>"+test.fields+"</Config>", 0600)
			loaded, err := LoadConfig()
			if test.invalid {
				if err == nil {
					t.Fatal("invalid CPU count was accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if loaded.OnlineCPUEnabled != test.enabled || loaded.OnlineCPUCount != test.count {
				t.Fatalf("CPU settings = enabled:%v count:%d, want enabled:%v count:%d", loaded.OnlineCPUEnabled, loaded.OnlineCPUCount, test.enabled, test.count)
			}
		})
	}
}

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
