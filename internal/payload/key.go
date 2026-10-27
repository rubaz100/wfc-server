package payload

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/binary"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"runtime"
)

const PublicKeyBlockSize = 4 + 256 + 256

func GeneratePrivateKey(path string) (*rsa.PrivateKey, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("generate payload RSA key: %w", err)
	}
	if err := validatePrivateKey(key); err != nil {
		return nil, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}
	data := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, fmt.Errorf("create payload private key: %w", err)
	}
	if _, err = file.Write(data); err != nil {
		_ = file.Close()
		return nil, err
	}
	if err = file.Close(); err != nil {
		return nil, err
	}
	return key, nil
}

func LoadPrivateKey(path string) (*rsa.PrivateKey, error) {
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		if info.Mode().Perm()&0077 != 0 {
			return nil, fmt.Errorf("payload private key %s must have mode 0600", path)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, rest := pem.Decode(data)
	if block == nil || block.Type != "PRIVATE KEY" || len(rest) != 0 {
		return nil, fmt.Errorf("payload key must contain one PKCS#8 PEM private key")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse payload private key: %w", err)
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("payload private key is not RSA")
	}
	if err := validatePrivateKey(key); err != nil {
		return nil, err
	}
	return key, nil
}

func validatePrivateKey(key *rsa.PrivateKey) error {
	if key == nil || key.N == nil || key.N.BitLen() != 2048 || key.E != 65537 {
		return fmt.Errorf("payload key must be RSA 2048 with exponent 65537")
	}
	return key.Validate()
}

func PublicKeyBlock(key *rsa.PublicKey) ([]byte, error) {
	if key == nil || key.N == nil || key.N.BitLen() != 2048 || key.E != 65537 {
		return nil, fmt.Errorf("payload public key must be RSA 2048 with exponent 65537")
	}
	modulus := key.N
	base := new(big.Int).Lsh(big.NewInt(1), 32)
	inverse := new(big.Int).ModInverse(modulus, base)
	if inverse == nil {
		return nil, fmt.Errorf("payload modulus has no inverse modulo 2^32")
	}
	n0inv := new(big.Int).Neg(inverse)
	n0inv.Mod(n0inv, base)
	result := make([]byte, 4, PublicKeyBlockSize)
	binary.BigEndian.PutUint32(result, uint32(n0inv.Uint64()))
	result = appendLittleEndianWords(result, modulus)
	r2 := new(big.Int).Exp(big.NewInt(2), big.NewInt(4096), modulus)
	result = appendLittleEndianWords(result, r2)
	if len(result) != PublicKeyBlockSize {
		return nil, fmt.Errorf("payload public block has unexpected size %d", len(result))
	}
	return result, nil
}

func appendLittleEndianWords(destination []byte, value *big.Int) []byte {
	if value.Sign() == 0 {
		return append(destination, make([]byte, 256)...)
	}
	data := value.Bytes()
	if len(data) > 256 {
		panic("oversized RSA field")
	}
	padded := make([]byte, 256)
	copy(padded[256-len(data):], data)
	for offset := 256; offset > 0; offset -= 4 {
		destination = append(destination, padded[offset-4:offset]...)
	}
	return destination
}
