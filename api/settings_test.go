package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"wwfc/common"
)

func setupSettingsTest(t *testing.T) {
	t.Helper()
	oldPath := common.DataPath()
	oldSecret := apiSecret
	directory := t.TempDir()
	if err := common.SetDataPath(directory); err != nil {
		t.Fatal(err)
	}
	configuration := `<Config><address>127.0.0.1</address><nasPort>80</nasPort><nasPortHttps>443</nasPortHttps><apiSecret>test-only-secret-12345</apiSecret><serverName>Before</serverName></Config>`
	if err := os.WriteFile(common.ConfigPath(), []byte(configuration), 0600); err != nil {
		t.Fatal(err)
	}
	apiSecret = "test-only-secret-12345"
	t.Cleanup(func() {
		apiSecret = oldSecret
		_ = common.SetDataPath(oldPath)
	})
}

func TestConfigAuthenticationValidationAndPersistence(t *testing.T) {
	setupSettingsTest(t)
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
		`{"secret":"test-only-secret-12345","allowMultipleDeviceIDs":"invalid"}`,
		`{"secret":"test-only-secret-12345","logLevel":5}`,
		`{"secret":"test-only-secret-12345","serverName":""}`,
		`{"secret":"test-only-secret-12345","apiSecret":"replacement"}`,
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
	HandleConfig(response, httptest.NewRequest(http.MethodPost, "/api/config", strings.NewReader(`{"secret":"test-only-secret-12345","serverName":"Test & LAN","allowMultipleDeviceIDs":"always","logLevel":2}`)))
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
	HandleConfig(response, httptest.NewRequest(http.MethodGet, "/api/config?secret=test-only-secret-12345", nil))
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "test-only-secret-12345") {
		t.Fatalf("GET exposed its secret or failed: %d %s", response.Code, response.Body.String())
	}
}

func TestMOTDPersistenceAndAuthentication(t *testing.T) {
	setupSettingsTest(t)
	response := httptest.NewRecorder()
	HandleMOTD(response, httptest.NewRequest(http.MethodPost, "/api/motd", strings.NewReader(`{"secret":"bad","text":"not saved"}`)))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated MOTD update = %d", response.Code)
	}
	response = httptest.NewRecorder()
	HandleMOTD(response, httptest.NewRequest(http.MethodPost, "/api/motd", strings.NewReader(`{"secret":"test-only-secret-12345","text":"LAN race night"}`)))
	if response.Code != http.StatusNoContent {
		t.Fatalf("MOTD update = %d", response.Code)
	}
	response = httptest.NewRecorder()
	HandleMOTD(response, httptest.NewRequest(http.MethodGet, "/api/motd?secret=test-only-secret-12345", nil))
	if response.Code != http.StatusOK || response.Body.String() != `{"text":"LAN race night"}` {
		t.Fatalf("MOTD = %d %s", response.Code, response.Body.String())
	}
	if _, err := os.Stat(filepath.Join(common.DataPath(), "motd.txt")); err != nil {
		t.Fatal(err)
	}
}

func TestEmptyAPISecretNeverAuthenticates(t *testing.T) {
	old := apiSecret
	apiSecret = ""
	t.Cleanup(func() { apiSecret = old })
	if authenticate(AuthInfo{}, RoleModerator) {
		t.Fatal("empty API secret allowed authentication")
	}
}
