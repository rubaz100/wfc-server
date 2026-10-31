package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"wwfc/common"
	"wwfc/database"
)

func authDatabase(t *testing.T) string {
	t.Helper()
	oldDB, oldState := db, IsAdminPasswordSet()
	path := filepath.Join(t.TempDir(), "auth.db")
	db = database.Start(common.Config{DatabasePath: path})
	t.Cleanup(func() {
		db.Close()
		db = oldDB
		adminPasswordSet.Store(oldState)
	})
	return path
}

func TestDefaultAdminPasswordLifecycle(t *testing.T) {
	path := authDatabase(t)
	if err := loadAdminState(); err != nil {
		t.Fatal(err)
	}
	if !IsAdminPasswordSet() || !CheckAdminPassword("MKWii") {
		t.Fatal("fresh database did not initialize the default password")
	}
	encoded, ok, err := db.GetAdminConfig(adminPasswordKey)
	if err != nil || !ok || !strings.HasPrefix(encoded, "pbkdf2_sha256$") {
		t.Fatalf("password was not persisted as a hash: ok=%v err=%v", ok, err)
	}
	if err := loadAdminState(); err != nil {
		t.Fatal(err)
	}
	again, _, _ := db.GetAdminConfig(adminPasswordKey)
	if again != encoded {
		t.Fatal("startup replaced an existing password hash")
	}
	mux := http.NewServeMux()
	RegisterExtendedHandlers(mux)
	post := func(path, body string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
		return response
	}
	response := post("/api/login", `{"password":"wrong"}`)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password returned %d", response.Code)
	}
	response = post("/api/login", `{"password":"MKWii"}`)
	var login struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &login); err != nil || response.Code != http.StatusOK || login.Token == "" {
		t.Fatalf("default login failed: %d %s", response.Code, response.Body.String())
	}
	t.Cleanup(func() { revokeToken(login.Token) })
	response = post("/api/password", `{"secret":"`+login.Token+`","current_password":"wrong","new_password":"changed password"}`)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("password change without current password returned %d", response.Code)
	}
	response = post("/api/password", `{"secret":"`+login.Token+`","current_password":"MKWii","new_password":" changed password "}`)
	if response.Code != http.StatusNoContent {
		t.Fatalf("password change failed: %d %s", response.Code, response.Body.String())
	}
	if CheckAdminPassword("MKWii") || !CheckAdminPassword(" changed password ") {
		t.Fatal("password change did not replace the default with the exact new password")
	}
	post("/api/logout", `{"secret":"`+login.Token+`"}`)
	if authenticate(AuthInfo{Secret: login.Token}, RoleModerator) {
		t.Fatal("logged-out session still authenticates")
	}
	db.Close()
	db = database.Start(common.Config{DatabasePath: path})
	if err := loadAdminState(); err != nil {
		t.Fatal(err)
	}
	if !CheckAdminPassword(" changed password ") || CheckAdminPassword("MKWii") {
		t.Fatal("restart replaced the existing password with the default")
	}
}

func TestAdminInitializationFailsOnDatabaseError(t *testing.T) {
	authDatabase(t)
	db.Close()
	if err := loadAdminState(); err == nil {
		t.Fatal("database read failure was treated as a fresh installation")
	}
	if IsAdminPasswordSet() {
		t.Fatal("failed initialization advertised working authentication")
	}
}

func TestPasswordHashRoundTrip(t *testing.T) {
	encoded, err := hashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !verifyPassword("correct horse battery staple", encoded) {
		t.Fatal("correct password did not verify")
	}
	if verifyPassword("wrong password", encoded) {
		t.Fatal("wrong password verified")
	}
	// Each hash uses a fresh salt, so two encodings of the same password differ.
	encoded2, err := hashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if encoded == encoded2 {
		t.Fatal("expected a unique salt per hash")
	}
	if verifyPassword("x", "garbage") || verifyPassword("x", "pbkdf2_sha256$bad") {
		t.Fatal("malformed hash must not verify")
	}
}

func TestTokenLifecycle(t *testing.T) {
	tok := issueToken()
	if tok == "" {
		t.Fatal("issueToken returned empty")
	}
	if !validToken(tok) {
		t.Fatal("freshly issued token is invalid")
	}
	if validToken("") || validToken("not-a-real-token") {
		t.Fatal("bogus token validated")
	}
	revokeToken(tok)
	if validToken(tok) {
		t.Fatal("revoked token still valid")
	}
}
