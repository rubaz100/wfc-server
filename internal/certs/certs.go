package certs

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
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

// Sources names optional pre-made certificate files to import instead of
// generating fresh ones. The modern pair (ModernCert/ModernKey) and the Wii
// pair (WiiCert/WiiKey) are independent: when a pair's fields are set the
// supplied files are copied in, and when they are empty that pair is generated.
// Supplying only one half of a pair is an error.
type Sources struct {
	ModernCert, ModernKey string
	WiiCert, WiiKey       string
}

func (s Sources) hasModern() bool { return s.ModernCert != "" || s.ModernKey != "" }
func (s Sources) hasWii() bool    { return s.WiiCert != "" || s.WiiKey != "" }

// Any reports whether any pre-made certificate file was supplied.
func (s Sources) Any() bool { return s.hasModern() || s.hasWii() }

// Generate writes freshly generated modern and Wii certificate material to
// paths and validates the result.
func Generate(paths Paths, domain string, lanIP net.IP, now time.Time) error {
	return Provision(paths, Sources{}, domain, lanIP, now)
}

// Provision writes the certificate material to paths: each pair named in
// sources is imported from the supplied files, and every pair left unset is
// generated. The result is validated before returning, so imported files must
// already satisfy the same requirements generated ones do (see Validate):
// the modern certificate an RSA key of at least 2048 bits, and the Wii pair an
// RSA-1024 PKCS#8 key with a certificate carrying the compatibility mutation.
// lanIP is only required when the modern certificate is generated.
func Provision(paths Paths, sources Sources, domain string, lanIP net.IP, now time.Time) error {
	if sources.hasModern() {
		if err := importPair(sources.ModernCert, paths.ModernCert, 0644, sources.ModernKey, paths.ModernKey); err != nil {
			return fmt.Errorf("import modern NAS certificate: %w", err)
		}
	} else if err := generateModern(paths, domain, lanIP, now); err != nil {
		return err
	}
	if sources.hasWii() {
		if err := importPair(sources.WiiCert, paths.WiiCert, 0644, sources.WiiKey, paths.WiiKey); err != nil {
			return fmt.Errorf("import Wii certificate: %w", err)
		}
	} else if err := generateWii(paths, now); err != nil {
		return err
	}
	return Validate(paths)
}

// importPair copies a supplied certificate and private key to their
// destinations, refusing to overwrite an existing file (writeNew is O_EXCL) and
// requiring both halves of the pair.
func importPair(certSrc, certDst string, certMode os.FileMode, keySrc, keyDst string) error {
	if certSrc == "" || keySrc == "" {
		return fmt.Errorf("both a certificate and its private key must be supplied together")
	}
	if err := copyNew(certSrc, certDst, certMode); err != nil {
		return err
	}
	return copyNew(keySrc, keyDst, 0600)
}

func copyNew(src, dst string, mode os.FileMode) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("read %s: %w", src, err)
	}
	return writeNew(dst, data, mode)
}

func generateModern(paths Paths, domain string, lanIP net.IP, now time.Time) error {
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
	return nil
}

func generateWii(paths Paths, now time.Time) error {
	wiiKey, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		return err
	}
	wiiDER, err := createWiiCertificate(wiiKey, now)
	if err != nil {
		return err
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
	return nil
}

func createWiiCertificate(key *rsa.PrivateKey, now time.Time) ([]byte, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 160))
	if err != nil {
		return nil, err
	}
	const hostname = "naswii.nintendowifi.net"
	commonName := pkix.AttributeTypeAndValue{
		Type:  asn1.ObjectIdentifier{2, 5, 4, 3},
		Value: asn1.RawValue{Tag: asn1.TagUTF8String, Bytes: []byte(hostname)},
	}
	subjectKeyID := sha1.Sum(x509.MarshalPKCS1PublicKey(&key.PublicKey))
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: hostname, ExtraNames: []pkix.AttributeTypeAndValue{commonName}},
		NotBefore:             now,
		NotAfter:              now.AddDate(10, 0, 0),
		BasicConstraintsValid: true,
		IsCA:                  true,
		SubjectKeyId:          subjectKeyID[:],
		AuthorityKeyId:        subjectKeyID[:],
		SignatureAlgorithm:    x509.SHA256WithRSA,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("create Wii compatibility certificate: %w", err)
	}
	return der, nil
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
