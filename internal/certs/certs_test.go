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
	hostname := []byte("naswii.nintendowifi.net")
	utf8CommonName := append([]byte{0x0c, byte(len(hostname))}, hostname...)
	if !bytes.Contains(certificate.RawSubject, utf8CommonName) || !bytes.Contains(certificate.RawIssuer, utf8CommonName) {
		t.Fatal("Wii issuer and subject must use the OpenSSL recipe's UTF8String encoding")
	}
	if certificate.Version != 3 || !certificate.BasicConstraintsValid || !certificate.IsCA {
		t.Fatal("Wii certificate must have the OpenSSL recipe's v3 basic constraints")
	}
	if certificate.KeyUsage != 0 || len(certificate.ExtKeyUsage) != 0 || len(certificate.UnknownExtKeyUsage) != 0 {
		t.Fatal("Wii certificate must not add key-usage restrictions")
	}
	if len(certificate.SubjectKeyId) != 20 || !bytes.Equal(certificate.SubjectKeyId, certificate.AuthorityKeyId) {
		t.Fatal("Wii certificate must have matching subject and authority key identifiers")
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
