package payload

import (
	"bytes"
	"crypto/md5"
	"crypto/rsa"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"wwfc/internal/assets"
)

const originalStage1MD5 = "b61166eef539ef802dfb97676f5c9b47"

type Catalog struct {
	key        *rsa.PrivateKey
	stage1     map[int][]byte
	stage2     map[string][]byte
	sbcm       map[string][]byte
	gecko      map[string]string
	deployment map[string][]string
}

func LoadCatalog(keyPath string) (*Catalog, error) {
	bundle, err := assets.Open()
	if err != nil {
		return nil, err
	}
	key, err := LoadPrivateKey(keyPath)
	if err != nil {
		return nil, err
	}
	return NewCatalog(bundle, key)
}

func NewCatalog(bundle *assets.Bundle, key *rsa.PrivateKey) (*Catalog, error) {
	if bundle == nil {
		return nil, fmt.Errorf("payload asset bundle is nil")
	}
	if key == nil {
		return nil, fmt.Errorf("payload private key is nil")
	}
	publicBlock, err := PublicKeyBlock(&key.PublicKey)
	if err != nil {
		return nil, err
	}
	catalog := &Catalog{
		key: key, stage1: map[int][]byte{}, stage2: map[string][]byte{},
		sbcm: map[string][]byte{}, gecko: map[string]string{},
	}
	original, exists := bundle.File("stage1.bin")
	if !exists {
		return nil, fmt.Errorf("stage1.bin is missing")
	}
	version1, exists := bundle.File("stage1v1.bin")
	if !exists || !bytes.Equal(original, version1) {
		return nil, fmt.Errorf("stage1.bin and stage1v1.bin must be identical")
	}
	hash := md5.Sum(append([]byte{0x01, 0x2c}, original...))
	if hex.EncodeToString(hash[:]) != originalStage1MD5 {
		return nil, fmt.Errorf("unexpected original stage 1 MD5 %x", hash)
	}
	var patchedStage1 []byte
	for version, body := range map[int][]byte{0: original, 1: version1} {
		patched, err := rekey(body, bundle.TemplatePublicKey, publicBlock)
		if err != nil {
			return nil, fmt.Errorf("stage 1 version %d: %w", version, err)
		}
		catalog.stage1[version] = append([]byte{0x01, 0x2c}, patched...)
		if version == 0 {
			patchedStage1 = patched
		}
	}
	newHash := md5.Sum(append([]byte{0x01, 0x2c}, patchedStage1...))
	newHashText := hex.EncodeToString(newHash[:])
	for path, body := range bundle.Files {
		switch {
		case strings.HasPrefix(path, "binary/payload.") && strings.HasSuffix(path, ".bin"):
			game := strings.TrimSuffix(strings.TrimPrefix(path, "binary/payload."), ".bin")
			if len(body) < 0x130+256 {
				return nil, fmt.Errorf("stage 2 payload %q is too small", game)
			}
			catalog.stage2[game] = append([]byte(nil), body...)
		case strings.HasPrefix(path, "sbcm/payload.") && strings.HasSuffix(path, ".bin"):
			game := strings.TrimSuffix(strings.TrimPrefix(path, "sbcm/payload."), ".bin")
			if len(body) < 0xb || len(game) != 4 {
				return nil, fmt.Errorf("invalid SBCM payload %q", game)
			}
			patched, err := rekey(body, bundle.TemplatePublicKey, publicBlock)
			if err != nil {
				return nil, fmt.Errorf("SBCM %s: %w", game, err)
			}
			catalog.sbcm[game] = patched
		case strings.HasPrefix(path, "gecko/") && strings.HasSuffix(path, ".bin"):
			game := strings.TrimSuffix(strings.TrimPrefix(path, "gecko/"), ".bin")
			if len(body)%8 != 0 {
				return nil, fmt.Errorf("Gecko template %q is not a sequence of 32-bit words", game)
			}
			if bytes.Count(body, []byte(originalStage1MD5)) != 1 {
				return nil, fmt.Errorf("Gecko template %q must contain exactly one stage 1 MD5", game)
			}
			patched := bytes.Replace(body, []byte(originalStage1MD5), []byte(newHashText), 1)
			var text strings.Builder
			for offset := 0; offset < len(patched); offset += 8 {
				fmt.Fprintf(&text, "%08X %08X\n", patched[offset:offset+4], patched[offset+4:offset+8])
			}
			catalog.gecko[game] = text.String()
		}
	}
	if len(catalog.stage2) != 8 || len(catalog.sbcm) != 4 || len(catalog.gecko) != 8 {
		return nil, fmt.Errorf("incomplete payload assets: stage2=%d sbcm=%d gecko=%d", len(catalog.stage2), len(catalog.sbcm), len(catalog.gecko))
	}
	versions := []string{bundle.Manifest.PayloadVersion}
	catalog.deployment = map[string][]string{
		"prod": append([]string(nil), versions...),
		"beta": append([]string(nil), versions...),
		"0":    append([]string(nil), versions...),
		"1":    append([]string(nil), versions...),
	}
	return catalog, nil
}

func rekey(template, oldKey, newKey []byte) ([]byte, error) {
	if len(oldKey) != PublicKeyBlockSize || len(newKey) != PublicKeyBlockSize || bytes.Equal(oldKey, newKey) {
		return nil, fmt.Errorf("invalid or unchanged public key block")
	}
	if bytes.Count(template, oldKey) != 1 {
		return nil, fmt.Errorf("expected exactly one development public key block")
	}
	return bytes.Replace(append([]byte(nil), template...), oldKey, newKey, 1), nil
}

func (c *Catalog) Stage1(version int) ([]byte, bool) {
	body, exists := c.stage1[version]
	return append([]byte(nil), body...), exists
}

func (c *Catalog) SBCM(game string) ([]byte, error) {
	body, exists := c.sbcm[game]
	if !exists {
		return nil, fmt.Errorf("no SBCM payload for game %q", game)
	}
	return append([]byte(nil), body...), nil
}

func (c *Catalog) Deployment() map[string][]string {
	result := map[string][]string{}
	for channel, versions := range c.deployment {
		result[channel] = append([]string(nil), versions...)
	}
	return result
}

func (c *Catalog) ExportGecko(directory string) error {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	for game, text := range c.gecko {
		path := filepath.Join(directory, game+".txt")
		data := []byte(text)
		if previous, err := os.ReadFile(path); err == nil {
			if !bytes.Equal(previous, data) {
				return fmt.Errorf("refusing to overwrite changed Gecko code %s", path)
			}
			continue
		} else if !os.IsNotExist(err) {
			return err
		}
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if err != nil {
			return err
		}
		if _, err := file.Write(data); err != nil {
			_ = file.Close()
			return err
		}
		if err := file.Close(); err != nil {
			return err
		}
	}
	return nil
}
