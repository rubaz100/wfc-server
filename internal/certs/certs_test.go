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

func TestProvisionImportsSuppliedCertificates(t *testing.T) {
	// Generate a valid set to act as the operator's pre-made certificates.
	source := DefaultPaths(t.TempDir())
	if err := Generate(source, "mkwfc.lan", net.ParseIP("10.0.20.246"), time.Now()); err != nil {
		t.Fatal(err)
	}

	// Import both pairs into a fresh data directory (no LAN IP needed).
	imported := DefaultPaths(t.TempDir())
	sources := Sources{ModernCert: source.ModernCert, ModernKey: source.ModernKey, WiiCert: source.WiiCert, WiiKey: source.WiiKey}
	if err := Provision(imported, sources, "mkwfc.lan", nil, time.Now()); err != nil {
		t.Fatalf("import both pairs: %v", err)
	}
	for _, pair := range [][2]string{{source.ModernCert, imported.ModernCert}, {source.ModernKey, imported.ModernKey}, {source.WiiCert, imported.WiiCert}, {source.WiiKey, imported.WiiKey}} {
		want, _ := os.ReadFile(pair[0])
		got, _ := os.ReadFile(pair[1])
		if !bytes.Equal(want, got) {
			t.Fatalf("imported file %s does not match the supplied source", pair[1])
		}
	}
	if info, err := os.Stat(imported.ModernKey); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("imported modern key mode = %v, err %v", info.Mode(), err)
	}

	// A mixed run imports the modern pair and generates the Wii pair.
	mixed := DefaultPaths(t.TempDir())
	if err := Provision(mixed, Sources{ModernCert: source.ModernCert, ModernKey: source.ModernKey}, "mkwfc.lan", net.ParseIP("10.0.20.246"), time.Now()); err != nil {
		t.Fatalf("import modern + generate Wii: %v", err)
	}
	generatedWii, _ := os.ReadFile(mixed.WiiCert)
	if bytes.Count(generatedWii, mutatedWiiSignatureMarker) != 1 {
		t.Fatal("mixed provisioning did not generate a mutated Wii certificate")
	}
}

func TestProvisionRejectsHalfOfAPair(t *testing.T) {
	source := DefaultPaths(t.TempDir())
	if err := Generate(source, "mkwfc.lan", net.ParseIP("10.0.20.246"), time.Now()); err != nil {
		t.Fatal(err)
	}
	// Supplying only the certificate (no key) of the modern pair must fail
	// before anything is written.
	dest := DefaultPaths(t.TempDir())
	if err := Provision(dest, Sources{ModernCert: source.ModernCert}, "mkwfc.lan", net.ParseIP("10.0.20.246"), time.Now()); err == nil {
		t.Fatal("import with a missing private key was accepted")
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
