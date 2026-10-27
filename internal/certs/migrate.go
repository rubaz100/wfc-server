package certs

import (
	"bytes"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func RegenerateWiiCertificate(certPath, keyPath string, now time.Time) (bool, error) {
	if err := ValidateWii(certPath, keyPath); err != nil {
		return false, err
	}
	certData, err := os.ReadFile(certPath)
	if err != nil {
		return false, err
	}
	original := bytes.Replace(certData, mutatedWiiSignatureMarker, originalWiiSignatureMarker, 1)
	certificate, err := x509.ParseCertificate(original)
	if err != nil {
		return false, err
	}
	if matchesOpenSSLRecipe(certificate) {
		return false, nil
	}
	keyData, err := os.ReadFile(keyPath)
	if err != nil {
		return false, err
	}
	block, _ := pem.Decode(keyData)
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return false, err
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return false, fmt.Errorf("Wii key is not RSA")
	}
	der, err := createWiiCertificate(key, now)
	if err != nil {
		return false, err
	}
	if bytes.Count(der, originalWiiSignatureMarker) != 1 {
		return false, fmt.Errorf("Wii certificate signature-area marker is missing or ambiguous")
	}
	mutated := bytes.Replace(der, originalWiiSignatureMarker, mutatedWiiSignatureMarker, 1)
	info, err := os.Stat(certPath)
	if err != nil || !info.Mode().IsRegular() {
		return false, fmt.Errorf("Wii certificate is not a regular file")
	}
	backupPath := certPath + ".before-openssl-compat"
	if err := writeNew(backupPath, certData, info.Mode().Perm()); err != nil {
		return false, fmt.Errorf("back up original Wii certificate: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(certPath), ".wii-certificate-*")
	if err != nil {
		return false, err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(info.Mode().Perm()); err != nil {
		_ = temporary.Close()
		return false, err
	}
	if _, err := temporary.Write(mutated); err != nil {
		_ = temporary.Close()
		return false, err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return false, err
	}
	if err := temporary.Close(); err != nil {
		return false, err
	}
	if err := os.Rename(temporaryPath, certPath); err != nil {
		return false, err
	}
	if err := ValidateWii(certPath, keyPath); err != nil {
		_ = os.Rename(backupPath, certPath)
		return false, fmt.Errorf("validate replacement; original certificate restored: %w", err)
	}
	return true, nil
}

func matchesOpenSSLRecipe(certificate *x509.Certificate) bool {
	hostname := []byte("naswii.nintendowifi.net")
	utf8CommonName := append([]byte{0x0c, byte(len(hostname))}, hostname...)
	return certificate.Version == 3 &&
		certificate.BasicConstraintsValid && certificate.IsCA &&
		certificate.KeyUsage == 0 && len(certificate.ExtKeyUsage) == 0 &&
		len(certificate.UnknownExtKeyUsage) == 0 &&
		len(certificate.SubjectKeyId) == 20 &&
		bytes.Equal(certificate.SubjectKeyId, certificate.AuthorityKeyId) &&
		bytes.Contains(certificate.RawSubject, utf8CommonName) &&
		bytes.Contains(certificate.RawIssuer, utf8CommonName)
}
