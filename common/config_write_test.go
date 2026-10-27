package common

import (
	"os"
	"strings"
	"testing"
)

func useTemporaryConfig(t *testing.T, contents string, mode os.FileMode) string {
	t.Helper()

	oldDataPath := DataPath()
	oldConfig, oldLoaded := config, configLoaded
	directory := t.TempDir()
	if err := SetDataPath(directory); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := SetDataPath(oldDataPath); err != nil {
			t.Errorf("restore data directory: %v", err)
		}
		config = oldConfig
		configLoaded = oldLoaded
	})

	if contents != "" {
		if err := os.WriteFile(ConfigPath(), []byte(contents), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(ConfigPath(), mode); err != nil {
			t.Fatal(err)
		}
	}
	return ConfigPath()
}

func TestApplyConfigChangesSurvivesReload(t *testing.T) {
	configPath := useTemporaryConfig(t, `<Config>
  <apiSecret>test-only-secret</apiSecret>
  <allowMultipleDeviceIDs>always</allowMultipleDeviceIDs>
</Config>
`, 0600)
	config = Config{AllowMultipleDeviceIDs: "always"}
	configLoaded = true

	if err := ApplyConfigChanges(map[string]string{
		"allowMultipleDeviceIDs": "always",
		"serverName":             "Test & LAN",
	}); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Fatalf("config permissions = %o, want 600", got)
	}

	contents, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), "<apiSecret>test-only-secret</apiSecret>") {
		t.Fatal("existing config field was not preserved")
	}

	config = Config{}
	configLoaded = false
	updated := GetConfig()
	if updated.AllowMultipleDeviceIDs != "always" {
		t.Fatalf("device policy after reload = %q, want always", updated.AllowMultipleDeviceIDs)
	}
	if updated.ServerName != "Test & LAN" {
		t.Fatalf("server name after reload = %q, want Test & LAN", updated.ServerName)
	}
	if updated.AdminAddress != "127.0.0.1:29990" {
		t.Fatalf("default admin address = %q, want loopback", updated.AdminAddress)
	}
}

func TestApplyConfigChangesDoesNotMutateOnFailure(t *testing.T) {
	useTemporaryConfig(t, "", 0600)
	config = Config{ServerName: "before", AllowMultipleDeviceIDs: "always"}
	configLoaded = true

	if err := ApplyConfigChanges(map[string]string{"serverName": "after"}); err == nil {
		t.Fatal("expected a missing-config error")
	}
	if config.ServerName != "before" || config.AllowMultipleDeviceIDs != "always" {
		t.Fatalf("failed save changed live config: %+v", config)
	}
}

func TestApplyConfigChangesRejectsInvalidValues(t *testing.T) {
	useTemporaryConfig(t, "<Config></Config>\n", 0600)
	config = Config{AllowMultipleDeviceIDs: "always"}
	configLoaded = true

	for _, fields := range []map[string]string{
		{"allowMultipleDeviceIDs": "invalid"},
		{"allowDefaultDolphinKeys": "yes"},
		{"logLevel": "5"},
	} {
		if err := ApplyConfigChanges(fields); err == nil {
			t.Fatalf("ApplyConfigChanges(%v) accepted an invalid value", fields)
		}
	}
	if config.AllowMultipleDeviceIDs != "always" {
		t.Fatalf("invalid updates changed device policy to %q", config.AllowMultipleDeviceIDs)
	}
}
