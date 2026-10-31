package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"wwfc/common"
)

func setupSettingsTest(t *testing.T) string {
	t.Helper()
	oldPath := common.DataPath()
	directory := t.TempDir()
	if err := common.SetDataPath(directory); err != nil {
		t.Fatal(err)
	}
	configuration := `<Config><address>127.0.0.1</address><nasPort>80</nasPort><nasPortHttps>443</nasPortHttps><apiSecret>test-only-secret-12345</apiSecret><serverName>Before</serverName></Config>`
	if err := os.WriteFile(common.ConfigPath(), []byte(configuration), 0600); err != nil {
		t.Fatal(err)
	}
	token := issueToken()
	t.Cleanup(func() {
		revokeToken(token)
		_ = common.SetDataPath(oldPath)
	})
	return token
}

func TestConfigAuthenticationValidationAndPersistence(t *testing.T) {
	token := setupSettingsTest(t)
	response := httptest.NewRecorder()
	HandleConfig(response, httptest.NewRequest(http.MethodGet, "/api/config", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated GET = %d", response.Code)
	}
	before, err := os.ReadFile(common.ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		`{"secret":"` + token + `","allowMultipleDeviceIDs":"invalid"}`,
		`{"secret":"` + token + `","logLevel":5}`,
		`{"secret":"` + token + `","serverName":""}`,
		`{"secret":"` + token + `","apiSecret":"replacement"}`,
	} {
		response := httptest.NewRecorder()
		HandleConfig(response, httptest.NewRequest(http.MethodPost, "/api/config", strings.NewReader(body)))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid update %s returned %d", body, response.Code)
		}
		after, _ := os.ReadFile(common.ConfigPath())
		if !bytes.Equal(before, after) {
			t.Fatalf("invalid update %s changed config.xml", body)
		}
	}
	response = httptest.NewRecorder()
	HandleConfig(response, httptest.NewRequest(http.MethodPost, "/api/config", strings.NewReader(`{"secret":"wrong","serverName":"After"}`)))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("wrong secret returned %d", response.Code)
	}
	response = httptest.NewRecorder()
	HandleConfig(response, httptest.NewRequest(http.MethodPost, "/api/config", strings.NewReader(`{"secret":"`+token+`","serverName":"Test & LAN","allowMultipleDeviceIDs":"always","logLevel":2}`)))
	if response.Code != http.StatusNoContent {
		t.Fatalf("valid update returned %d: %s", response.Code, response.Body.String())
	}
	config := common.GetConfig()
	if config.ServerName != "Test & LAN" || config.AllowMultipleDeviceIDs != "always" || *config.LogLevel != 2 {
		t.Fatalf("updated configuration = %+v", config)
	}
	after, _ := os.ReadFile(common.ConfigPath())
	if !bytes.Contains(after, []byte("<serverName>Test &amp; LAN</serverName>")) || !bytes.Contains(after, []byte("<apiSecret>test-only-secret-12345</apiSecret>")) {
		t.Fatalf("persisted configuration lost values: %s", after)
	}
	response = httptest.NewRecorder()
	HandleConfig(response, httptest.NewRequest(http.MethodGet, "/api/config?secret="+token, nil))
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "test-only-secret-12345") {
		t.Fatalf("GET exposed its secret or failed: %d %s", response.Code, response.Body.String())
	}
}

func TestOnlineCPUSettingsAuthenticationValidationAndPersistence(t *testing.T) {
	token := setupSettingsTest(t)
	readSettings := func() ConfigResponseSpec {
		t.Helper()
		response := httptest.NewRecorder()
		HandleConfig(response, httptest.NewRequest(http.MethodGet, "/api/config?secret="+token, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("GET settings = %d: %s", response.Code, response.Body.String())
		}
		var settings ConfigResponseSpec
		if err := json.Unmarshal(response.Body.Bytes(), &settings); err != nil {
			t.Fatal(err)
		}
		if settings.OnlineCPUScope != "all" || settings.OnlineCPUAvailable != (common.OnlineCPUProtocolVersion == "1") {
			t.Fatalf("CPU settings must retain public/private scope and the linked protocol gate: %+v", settings)
		}
		return settings
	}
	if defaults := readSettings(); defaults.OnlineCPUEnabled || defaults.OnlineCPUCount != 4 {
		t.Fatalf("CPU defaults = enabled:%v count:%d", defaults.OnlineCPUEnabled, defaults.OnlineCPUCount)
	}
	before, err := os.ReadFile(common.ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	HandleConfig(response, httptest.NewRequest(http.MethodPost, "/api/config", strings.NewReader(`{"onlineCPUEnabled":true,"onlineCPUCount":4}`)))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated CPU update = %d", response.Code)
	}
	for _, fields := range []string{
		`"onlineCPUCount":0`, `"onlineCPUCount":-1`, `"onlineCPUCount":12`,
		`"onlineCPUCount":4.5`, `"onlineCPUCount":"4"`, `"onlineCPUEnabled":"true"`,
	} {
		response := httptest.NewRecorder()
		HandleConfig(response, httptest.NewRequest(http.MethodPost, "/api/config", strings.NewReader(`{"secret":"`+token+`",`+fields+`}`)))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid CPU update %s = %d", fields, response.Code)
		}
	}
	after, err := os.ReadFile(common.ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("rejected CPU settings changed config.xml")
	}
	for _, update := range []struct {
		fields  string
		enabled bool
		count   int
	}{
		{fields: `"onlineCPUCount":11`, count: 11},
		{fields: `"onlineCPUEnabled":true,"onlineCPUCount":1`, enabled: true, count: 1},
		{fields: `"onlineCPUEnabled":false`, count: 1},
	} {
		response := httptest.NewRecorder()
		HandleConfig(response, httptest.NewRequest(http.MethodPost, "/api/config", strings.NewReader(`{"secret":"`+token+`",`+update.fields+`}`)))
		if response.Code != http.StatusNoContent {
			t.Fatalf("valid CPU update %s = %d: %s", update.fields, response.Code, response.Body.String())
		}
		settings := readSettings()
		if settings.OnlineCPUEnabled != update.enabled || settings.OnlineCPUCount != update.count {
			t.Fatalf("CPU update %s returned enabled:%v count:%d", update.fields, settings.OnlineCPUEnabled, settings.OnlineCPUCount)
		}
	}
	after, err = os.ReadFile(common.ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(after, []byte("<onlineCPUEnabled>false</onlineCPUEnabled>")) || !bytes.Contains(after, []byte("<onlineCPUCount>1</onlineCPUCount>")) {
		t.Fatalf("CPU settings were not persisted: %s", after)
	}
}

func TestOnlineCPUAvailabilityRequiresLinkedProtocol(t *testing.T) {
	token := setupSettingsTest(t)
	previous := common.OnlineCPUProtocolVersion
	t.Cleanup(func() { common.OnlineCPUProtocolVersion = previous })
	for _, protocol := range []string{"", "0", "1", "2"} {
		common.OnlineCPUProtocolVersion = protocol
		response := httptest.NewRecorder()
		HandleConfig(response, httptest.NewRequest(http.MethodGet, "/api/config?secret="+token, nil))
		var settings ConfigResponseSpec
		if err := json.Unmarshal(response.Body.Bytes(), &settings); err != nil {
			t.Fatal(err)
		}
		if settings.OnlineCPUAvailable != (protocol == "1") {
			t.Fatalf("protocol %q advertised available:%v", protocol, settings.OnlineCPUAvailable)
		}
	}
}

func TestMOTDPersistenceAndAuthentication(t *testing.T) {
	token := setupSettingsTest(t)
	response := httptest.NewRecorder()
	HandleMOTD(response, httptest.NewRequest(http.MethodPost, "/api/motd", strings.NewReader(`{"secret":"bad","text":"not saved"}`)))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated MOTD update = %d", response.Code)
	}
	response = httptest.NewRecorder()
	HandleMOTD(response, httptest.NewRequest(http.MethodPost, "/api/motd", strings.NewReader(`{"secret":"`+token+`","text":"LAN race night"}`)))
	if response.Code != http.StatusNoContent {
		t.Fatalf("MOTD update = %d", response.Code)
	}
	response = httptest.NewRecorder()
	HandleMOTD(response, httptest.NewRequest(http.MethodGet, "/api/motd?secret="+token, nil))
	if response.Code != http.StatusOK || response.Body.String() != `{"text":"LAN race night"}` {
		t.Fatalf("MOTD = %d %s", response.Code, response.Body.String())
	}
	if _, err := os.Stat(filepath.Join(common.DataPath(), "motd.txt")); err != nil {
		t.Fatal(err)
	}
}

func TestAPISecretNeverAuthenticates(t *testing.T) {
	token := setupSettingsTest(t)
	for _, credential := range []string{"", "test-only-secret-12345", "invalid-token"} {
		if authenticate(AuthInfo{Secret: credential}, RoleModerator) {
			t.Fatalf("non-session credential %q authenticated", credential)
		}
	}
	if !authenticate(AuthInfo{Secret: token}, RoleModerator) {
		t.Fatal("session token did not authenticate")
	}
	response := httptest.NewRecorder()
	HandleConfig(response, httptest.NewRequest(http.MethodGet, "/api/config?secret=test-only-secret-12345", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("API secret bypassed password login: %d", response.Code)
	}
}
