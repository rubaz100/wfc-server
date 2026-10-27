package certs

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGenerateAndValidate(t *testing.T) {
	paths := DefaultPaths(t.TempDir())
	if err := Generate(paths, "mkwfc.lan", net.ParseIP("10.0.20.246"), time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := tls.LoadX509KeyPair(paths.ModernCert, paths.ModernKey); err != nil {
		t.Fatal(err)
	}
	der, err := os.ReadFile(paths.WiiCert)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Count(der, mutatedWiiSignatureMarker) != 1 || bytes.Contains(der, originalWiiSignatureMarker) {
		t.Fatal("Wii signature-area mutation is not exactly once")
	}
	original := bytes.Replace(der, mutatedWiiSignatureMarker, originalWiiSignatureMarker, 1)
	certificate, err := x509.ParseCertificate(original)
	if err != nil {
		t.Fatal(err)
	}
	if certificate.Subject.CommonName != "naswii.nintendowifi.net" || certificate.PublicKeyAlgorithm != x509.RSA {
		t.Fatalf("unexpected Wii certificate: %+v", certificate.Subject)
	}
	if err := Validate(paths); err != nil {
		t.Fatal(err)
	}
	if _, err := tls.LoadX509KeyPair(paths.ModernCert, paths.WiiKey); err == nil {
		t.Fatal("modern and Wii private keys must be separate")
	}
	if err := Generate(paths, "mkwfc.lan", net.ParseIP("10.0.20.246"), time.Now()); err == nil {
		t.Fatal("certificate generation must not overwrite existing keys")
	}
}

func TestRejectsMalformedWiiMaterial(t *testing.T) {
	directory := t.TempDir()
	certPath := filepath.Join(directory, "bad.der")
	keyPath := filepath.Join(directory, "bad.pem")
	if err := os.WriteFile(certPath, []byte("bad"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, []byte("bad"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateWii(certPath, keyPath); err == nil {
		t.Fatal("malformed Wii material was accepted")
	}
}
