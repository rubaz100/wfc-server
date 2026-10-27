package certs

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

var (
	originalWiiSignatureMarker = []byte{0x05, 0x00, 0x03, 0x81, 0x81}
	mutatedWiiSignatureMarker  = []byte{0x04, 0x00, 0x03, 0x81, 0x81}
)

type Paths struct {
	ModernCert, ModernKey, WiiCert, WiiKey string
}

func DefaultPaths(dataDir string) Paths {
	return Paths{
		ModernCert: filepath.Join(dataDir, "cert.pem"),
		ModernKey:  filepath.Join(dataDir, "key.pem"),
		WiiCert:    filepath.Join(dataDir, "naswii-cert.der"),
		WiiKey:     filepath.Join(dataDir, "naswii-key.pem"),
	}
}

func Generate(paths Paths, domain string, lanIP net.IP, now time.Time) error {
	if lanIP == nil || lanIP.To4() == nil || !lanIP.IsPrivate() {
		return fmt.Errorf("certificate LAN address must be a private IPv4 address")
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		return err
	}
	modernKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}
	modernTemplate := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: domain, Organization: []string{"mkwfc local development"}},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(10 * 365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{domain, "nas." + domain, "naswii.nintendowifi.net", "conntest.nintendowifi.net"},
		IPAddresses:  []net.IP{lanIP.To4()},
	}
	modernDER, err := x509.CreateCertificate(rand.Reader, modernTemplate, modernTemplate, &modernKey.PublicKey, modernKey)
	if err != nil {
		return fmt.Errorf("create modern NAS certificate: %w", err)
	}
	modernKeyDER, err := x509.MarshalPKCS8PrivateKey(modernKey)
	if err != nil {
		return err
	}
	if err := writeNew(paths.ModernKey, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: modernKeyDER}), 0600); err != nil {
		return err
	}
	if err := writeNew(paths.ModernCert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: modernDER}), 0644); err != nil {
		return err
	}

	wiiKey, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		return err
	}
	wiiSerial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		return err
	}
	wiiTemplate := &x509.Certificate{
		SerialNumber:       wiiSerial,
		Subject:            pkix.Name{CommonName: "naswii.nintendowifi.net"},
		NotBefore:          now.Add(-time.Hour),
		NotAfter:           now.Add(20 * 365 * 24 * time.Hour),
		KeyUsage:           x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:        []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		SignatureAlgorithm: x509.SHA256WithRSA,
	}
	wiiDER, err := x509.CreateCertificate(rand.Reader, wiiTemplate, wiiTemplate, &wiiKey.PublicKey, wiiKey)
	if err != nil {
		return fmt.Errorf("create Wii compatibility certificate: %w", err)
	}
	if bytes.Count(wiiDER, originalWiiSignatureMarker) != 1 {
		return fmt.Errorf("Wii certificate signature-area marker is missing or ambiguous")
	}
	mutated := bytes.Replace(wiiDER, originalWiiSignatureMarker, mutatedWiiSignatureMarker, 1)
	if bytes.Count(mutated, mutatedWiiSignatureMarker) != 1 {
		return fmt.Errorf("Wii certificate mutation failed")
	}
	wiiKeyDER, err := x509.MarshalPKCS8PrivateKey(wiiKey)
	if err != nil {
		return err
	}
	if err := writeNew(paths.WiiKey, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: wiiKeyDER}), 0600); err != nil {
		return err
	}
	if err := writeNew(paths.WiiCert, mutated, 0644); err != nil {
		return err
	}
	return Validate(paths)
}

func writeNew(path string, data []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	if _, err = file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func Validate(paths Paths) error {
	modernCert, err := os.ReadFile(paths.ModernCert)
	if err != nil {
		return err
	}
	modernKey, err := os.ReadFile(paths.ModernKey)
	if err != nil {
		return err
	}
	if err := validateKeyPermissions(paths.ModernKey); err != nil {
		return err
	}
	pair, err := tls.X509KeyPair(modernCert, modernKey)
	if err != nil {
		return fmt.Errorf("modern NAS TLS key pair: %w", err)
	}
	if len(pair.Certificate) == 0 {
		return fmt.Errorf("modern NAS certificate is empty")
	}
	certificate, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return fmt.Errorf("parse modern NAS certificate: %w", err)
	}
	publicKey, ok := certificate.PublicKey.(*rsa.PublicKey)
	if !ok || publicKey.N.BitLen() < 2048 {
		return fmt.Errorf("modern NAS certificate must use an RSA key of at least 2048 bits")
	}
	return ValidateWii(paths.WiiCert, paths.WiiKey)
}

func ValidateWii(certPath, keyPath string) error {
	certData, err := os.ReadFile(certPath)
	if err != nil {
		return err
	}
	keyData, err := os.ReadFile(keyPath)
	if err != nil {
		return err
	}
	if err := validateKeyPermissions(keyPath); err != nil {
		return err
	}
	block, rest := pem.Decode(keyData)
	if block == nil || len(rest) != 0 {
		return fmt.Errorf("Wii key must be one PKCS#8 PEM private key")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return fmt.Errorf("Wii key: %w", err)
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok || key.N.BitLen() != 1024 || key.E != 65537 {
		return fmt.Errorf("Wii key must be RSA 1024 with exponent 65537")
	}
	if err := key.Validate(); err != nil {
		return fmt.Errorf("Wii private key: %w", err)
	}
	if bytes.Count(certData, mutatedWiiSignatureMarker) != 1 {
		return fmt.Errorf("Wii certificate must contain exactly one compatibility mutation")
	}
	original := bytes.Replace(certData, mutatedWiiSignatureMarker, originalWiiSignatureMarker, 1)
	certificate, err := x509.ParseCertificate(original)
	if err != nil {
		return fmt.Errorf("Wii certificate before compatibility mutation: %w", err)
	}
	if certificate.Subject.CommonName != "naswii.nintendowifi.net" {
		return fmt.Errorf("Wii certificate has unexpected subject %q", certificate.Subject.CommonName)
	}
	publicKey, ok := certificate.PublicKey.(*rsa.PublicKey)
	if !ok || publicKey.N.Cmp(key.N) != 0 || publicKey.E != key.E {
		return fmt.Errorf("Wii certificate and private key do not match")
	}
	if err := certificate.CheckSignature(x509.SHA256WithRSA, certificate.RawTBSCertificate, certificate.Signature); err != nil {
		return fmt.Errorf("Wii certificate/key signature: %w", err)
	}
	return nil
}
