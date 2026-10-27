package payload

import (
	"encoding/binary"
	"math/big"
	"os"
	"path/filepath"
	"testing"
)

func TestPrivateKeyLifecycleAndPublicBlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "payload.pem")
	key, err := GeneratePrivateKey(path)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadPrivateKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.N.Cmp(key.N) != 0 {
		t.Fatal("key changed after reload")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("private key mode = %v, err %v", info.Mode(), err)
	}
	block, err := PublicKeyBlock(&loaded.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if len(block) != 516 {
		t.Fatalf("public block length = %d, want 516", len(block))
	}
	base := new(big.Int).Lsh(big.NewInt(1), 32)
	product := new(big.Int).Mul(loaded.N, new(big.Int).SetUint64(uint64(binary.BigEndian.Uint32(block))))
	product.Mod(product, base)
	if product.Cmp(new(big.Int).Sub(base, big.NewInt(1))) != 0 {
		t.Fatal("n0inv does not satisfy n*n0inv = -1 mod 2^32")
	}
	wantR2 := new(big.Int).Exp(big.NewInt(2), big.NewInt(4096), loaded.N)
	if new(big.Int).SetBytes(reverseWords(block[260:])).Cmp(wantR2) != 0 {
		t.Fatal("Montgomery R^2 encoding mismatch")
	}
}

func reverseWords(data []byte) []byte {
	result := make([]byte, len(data))
	for offset := 0; offset < len(data); offset += 4 {
		copy(result[len(data)-offset-4:len(data)-offset], data[offset:offset+4])
	}
	return result
}
