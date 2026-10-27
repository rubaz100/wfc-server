package payload

import (
	"crypto"
	"crypto/md5"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"wwfc/internal/assets"
)

func testCatalog(t *testing.T) *Catalog {
	t.Helper()
	bundle, err := assets.Open()
	if err != nil {
		t.Fatal(err)
	}
	key, err := GeneratePrivateKey(filepath.Join(t.TempDir(), "key.pem"))
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := NewCatalog(bundle, key)
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}

func TestCatalogRekeysEveryClient(t *testing.T) {
	catalog := testCatalog(t)
	block, err := PublicKeyBlock(&catalog.key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range []int{0, 1} {
		body, ok := catalog.Stage1(version)
		if !ok || len(body) != 3558 {
			t.Fatalf("stage 1 version %d = %d, %v", version, len(body), ok)
		}
		if strings.Count(string(body), string(block)) != 1 {
			t.Fatalf("stage 1 version %d was not rekeyed", version)
		}
	}
	if _, ok := catalog.Stage1(2); ok {
		t.Fatal("unknown stage 1 version received v1")
	}
	for _, game := range []string{"RMCP", "RMCE", "RMCJ", "RMCK"} {
		body, err := catalog.SBCM(game)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Count(string(body), string(block)) != 1 {
			t.Fatalf("SBCM %s was not rekeyed", game)
		}
	}
	directory := t.TempDir()
	if err := catalog.ExportGecko(directory); err != nil {
		t.Fatal(err)
	}
	stage1, _ := catalog.Stage1(0)
	hash := fmt.Sprintf("%x", md5.Sum(stage1))
	for _, game := range []string{"RMCPD00", "RMCPN0001", "RMCED00", "RMCEN0001", "RMCJD00", "RMCJN0001", "RMCKD00", "RMCKN0001"} {
		text := catalog.gecko[game]
		binary, err := hex.DecodeString(strings.Join(strings.Fields(text), ""))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(binary), hash) {
			t.Fatalf("Gecko %s lacks new stage 1 MD5 %s", game, hash)
		}
		if strings.Contains(string(binary), originalStage1MD5) {
			t.Fatalf("Gecko %s retains original stage 1 MD5", game)
		}
		if err := catalog.ExportGecko(directory); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSignedPayloadRequest(t *testing.T) {
	catalog := testCatalog(t)
	salt := strings.Repeat("ab", 32)
	raw := "c=0&v=00&d=00000001&f=00000000000000&k=1&g=RMCPD00&s=" + salt
	digest := sha256.Sum256([]byte("payload?" + raw))
	request := httptest.NewRequest("GET", "/payload?"+raw+"&h="+hex.EncodeToString(digest[:4]), nil)
	response := httptest.NewRecorder()
	catalog.ServeHTTP(response, request)
	if response.Code != 200 {
		t.Fatalf("signed payload status = %d: %s", response.Code, response.Body.String())
	}
	body, err := io.ReadAll(response.Result().Body)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) != len(catalog.stage2["RMCPD00"]) {
		t.Fatalf("signed payload length = %d", len(body))
	}
	hash := sha256.Sum256(body[0x110:])
	if err := rsa.VerifyPKCS1v15(&catalog.key.PublicKey, crypto.SHA256, hash[:], body[0x10:0x110]); err != nil {
		t.Fatalf("signature verification: %v", err)
	}
	if string(body[0x110:0x120]) != string(digest[:16]) {
		t.Fatal("salt hash was not inserted at 0x110")
	}
}

func TestPayloadValidationAndStage1(t *testing.T) {
	catalog := testCatalog(t)
	for _, target := range []string{
		"/payload?g=bad", "/payload?g=RMCPD00&c=unknown", "/payload?g=RMCPD00&v=z0",
		"/payload?g=RMCPD00&s=" + strings.Repeat("ab", 32),
		"/payload?g=RMCPD00&s=" + strings.Repeat("ab", 32) + "&h=00000000",
		"/payload?g=RMCPD00&k=2", "/payload?g=RMCPD00&v=00&v=01",
		"/payload?g=RMCPD00&" + strings.Repeat("x", 5000),
	} {
		response := httptest.NewRecorder()
		catalog.ServeHTTP(response, httptest.NewRequest("GET", target, nil))
		if response.Code < 400 || response.Code >= 500 {
			t.Errorf("%s status = %d, want client error", target, response.Code)
		}
	}
	for _, version := range []int{0, 1} {
		body, _ := catalog.Stage1(version)
		response := httptest.NewRecorder()
		catalog.ServeHTTP(response, httptest.NewRequest("GET", fmt.Sprintf("/w%d", version), nil))
		if response.Code != 200 || string(response.Body.Bytes()) != string(body) {
			t.Errorf("stage 1 /w%d response mismatch", version)
		}
	}
	response := httptest.NewRecorder()
	catalog.ServeHTTP(response, httptest.NewRequest("GET", "/w2", nil))
	if response.Code != 404 {
		t.Fatalf("/w2 status = %d, want 404", response.Code)
	}
}
