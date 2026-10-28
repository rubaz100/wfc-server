package api

import "testing"

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
