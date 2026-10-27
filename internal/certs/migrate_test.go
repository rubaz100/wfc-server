package certs

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRegenerateWiiCertificatePreservesKeyAndBackup(t *testing.T) {
	directory := t.TempDir()
	certPath := filepath.Join(directory, "naswii-cert.der")
	keyPath := filepath.Join(directory, "naswii-key.pem")
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	legacy, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "naswii.nintendowifi.net"},
		NotBefore:    now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		KeyUsage:           x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:        []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		SignatureAlgorithm: x509.SHA256WithRSA,
	}, &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "naswii.nintendowifi.net"},
		NotBefore:    now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		KeyUsage:           x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:        []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		SignatureAlgorithm: x509.SHA256WithRSA,
	}, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	legacy = bytes.Replace(legacy, originalWiiSignatureMarker, mutatedWiiSignatureMarker, 1)
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(keyPath, keyPEM, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(certPath, legacy, 0644); err != nil {
		t.Fatal(err)
	}
	changed, err := RegenerateWiiCertificate(certPath, keyPath, now)
	if err != nil || !changed {
		t.Fatalf("regeneration = %v, %v", changed, err)
	}
	backup, err := os.ReadFile(certPath + ".before-openssl-compat")
	if err != nil || !bytes.Equal(backup, legacy) {
		t.Fatalf("backup does not preserve the original certificate: %v", err)
	}
	replacement, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatal(err)
	}
	original := bytes.Replace(replacement, mutatedWiiSignatureMarker, originalWiiSignatureMarker, 1)
	certificate, err := x509.ParseCertificate(original)
	if err != nil || !matchesOpenSSLRecipe(certificate) {
		t.Fatalf("replacement has the wrong DER encoding: %v", err)
	}
	if err := ValidateWii(certPath, keyPath); err != nil {
		t.Fatal(err)
	}
	changed, err = RegenerateWiiCertificate(certPath, keyPath, now)
	if err != nil || changed {
		t.Fatalf("second regeneration = %v, %v", changed, err)
	}
	unchanged, err := os.ReadFile(certPath)
	if err != nil || !bytes.Equal(unchanged, replacement) {
		t.Fatalf("compatible certificate was rewritten: %v", err)
	}
	unchangedKey, err := os.ReadFile(keyPath)
	if err != nil || !bytes.Equal(unchangedKey, keyPEM) {
		t.Fatalf("private key changed: %v", err)
	}
}
