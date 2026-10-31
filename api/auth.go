package api

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"wwfc/logging"
)

// Admin authentication for the Host Manager.
//
// The admin password is stored (salted + hashed with PBKDF2-SHA256) in the
// SQLite admin_config table, not config.xml. Clients log in with the password
// and receive a short-lived session token which they send as the request
// "secret" thereafter, so the slow password hash runs only at login.
//
// First run initializes a default password. Existing passwords are preserved;
// the config.xml apiSecret is never accepted as a session credential.

const (
	adminPasswordKey     = "admin_password" // admin_config key
	defaultAdminPassword = "MKWii"
	pbkdf2Iterations     = 210000
	pbkdf2KeyLength      = 32
	pbkdf2SaltLength     = 16
	tokenTTL             = 12 * time.Hour
)

// adminPasswordSet caches the public authentication status. Updated on set.
var adminPasswordSet atomic.Bool

var (
	tokenMutex sync.Mutex
	tokens     = map[string]time.Time{} // token -> expiry
)

// loadAdminState refreshes the cached "password set" flag from the DB. On first
// run (no password stored), it auto-sets the default password so the panel is
// immediately usable without the apiSecret setup gate.
func loadAdminState() error {
	adminPasswordSet.Store(false)
	_, ok, err := db.GetAdminConfig(adminPasswordKey)
	if err != nil {
		return fmt.Errorf("read admin password: %w", err)
	}
	if ok {
		adminPasswordSet.Store(true)
		return nil
	}
	encoded, err := hashPassword(defaultAdminPassword)
	if err != nil {
		return err
	}
	created, err := db.SetAdminConfigIfAbsent(adminPasswordKey, encoded)
	if err != nil {
		return fmt.Errorf("initialize admin password: %w", err)
	}
	adminPasswordSet.Store(true)
	if created {
		logging.Notice("API:admin", "Default admin password set to \""+defaultAdminPassword+
			"\" — change it in Settings → Admin password")
	}
	return nil
}

// IsAdminPasswordSet reports whether a password has been configured.
func IsAdminPasswordSet() bool { return adminPasswordSet.Load() }

// hashPassword returns an encoded "pbkdf2_sha256$iter$salt$hash" string.
func hashPassword(password string) (string, error) {
	salt := make([]byte, pbkdf2SaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, pbkdf2Iterations, pbkdf2KeyLength)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("pbkdf2_sha256$%d$%s$%s", pbkdf2Iterations,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// verifyPassword checks a plaintext password against an encoded hash.
func verifyPassword(password, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2_sha256" {
		return false
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter < 1 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, iter, len(want))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(got, want) == 1
}

// SetAdminPassword hashes and stores a new admin password and updates the cache.
func SetAdminPassword(password string) error {
	encoded, err := hashPassword(password)
	if err != nil {
		return err
	}
	if err := db.SetAdminConfig(adminPasswordKey, encoded); err != nil {
		return err
	}
	adminPasswordSet.Store(true)
	return nil
}

// CheckAdminPassword verifies a password against the stored hash.
func CheckAdminPassword(password string) bool {
	encoded, ok, err := db.GetAdminConfig(adminPasswordKey)
	if err != nil || !ok {
		return false
	}
	return verifyPassword(password, encoded)
}

// issueToken creates a session token valid for tokenTTL.
func issueToken() string {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return ""
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	tokenMutex.Lock()
	tokens[token] = time.Now().Add(tokenTTL)
	// Opportunistically sweep expired tokens.
	for t, exp := range tokens {
		if time.Now().After(exp) {
			delete(tokens, t)
		}
	}
	tokenMutex.Unlock()
	return token
}

// validToken reports whether the token is live, refreshing its TTL if so.
func validToken(token string) bool {
	if token == "" {
		return false
	}
	tokenMutex.Lock()
	defer tokenMutex.Unlock()
	exp, ok := tokens[token]
	if !ok || time.Now().After(exp) {
		delete(tokens, token)
		return false
	}
	tokens[token] = time.Now().Add(tokenTTL)
	return true
}

// revokeToken drops a session token (logout).
func revokeToken(token string) {
	tokenMutex.Lock()
	delete(tokens, token)
	tokenMutex.Unlock()
}
