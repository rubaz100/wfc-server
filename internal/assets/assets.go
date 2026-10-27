package assets

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"
)

//go:embed files
var embedded embed.FS

type Artifact struct {
	Path   string `json:"path"`
	Source string `json:"source"`
	Size   int    `json:"size"`
	SHA256 string `json:"sha256"`
}

type Manifest struct {
	Format                  int        `json:"format"`
	PayloadVersion          string     `json:"payload_version"`
	WFCServerRevision       string     `json:"wfc_server_revision"`
	PatcherRevision         string     `json:"patcher_revision"`
	DevelopmentPublicKeySHA string     `json:"development_public_key_sha256"`
	DevelopmentPublicKey    string     `json:"development_public_key"`
	Artifacts               []Artifact `json:"artifacts"`
}

type Bundle struct {
	Manifest          Manifest
	Files             map[string][]byte
	TemplatePublicKey []byte
}

func Open() (*Bundle, error) {
	data, err := embedded.ReadFile("files/manifest.json")
	if err != nil {
		return nil, fmt.Errorf("read embedded asset manifest: %w", err)
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("parse embedded asset manifest: %w", err)
	}
	if manifest.Format != 1 || manifest.PayloadVersion == "" {
		return nil, fmt.Errorf("unsupported embedded asset manifest")
	}
	key, err := hex.DecodeString(manifest.DevelopmentPublicKey)
	if err != nil || len(key) != 516 {
		return nil, fmt.Errorf("invalid embedded development public key")
	}
	sum := sha256.Sum256(key)
	if hex.EncodeToString(sum[:]) != manifest.DevelopmentPublicKeySHA {
		return nil, fmt.Errorf("development public key hash mismatch")
	}
	bundle := &Bundle{Manifest: manifest, Files: map[string][]byte{}, TemplatePublicKey: key}
	for _, artifact := range manifest.Artifacts {
		if artifact.Path == "" || artifact.Path[0] == '/' || len(artifact.Path) > 128 {
			return nil, fmt.Errorf("invalid embedded asset path %q", artifact.Path)
		}
		if _, exists := bundle.Files[artifact.Path]; exists {
			return nil, fmt.Errorf("duplicate embedded asset %q", artifact.Path)
		}
		data, err := embedded.ReadFile("files/" + artifact.Path)
		if err != nil {
			return nil, fmt.Errorf("read embedded asset %q: %w", artifact.Path, err)
		}
		if len(data) != artifact.Size {
			return nil, fmt.Errorf("embedded asset %q has size %d, want %d", artifact.Path, len(data), artifact.Size)
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != artifact.SHA256 {
			return nil, fmt.Errorf("embedded asset %q hash mismatch", artifact.Path)
		}
		bundle.Files[artifact.Path] = data
	}
	var paths []string
	err = fs.WalkDir(embedded, "files", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || path == "files/manifest.json" {
			return err
		}
		paths = append(paths, path[len("files/"):])
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	if len(paths) != len(bundle.Files) {
		return nil, fmt.Errorf("embedded asset count %d does not match manifest %d", len(paths), len(bundle.Files))
	}
	for index, path := range paths {
		if _, exists := bundle.Files[path]; !exists {
			return nil, fmt.Errorf("embedded asset %q is not whitelisted in manifest", path)
		}
		if index > 0 && paths[index-1] == path {
			return nil, fmt.Errorf("duplicate embedded asset path %q", path)
		}
	}
	if bundle.Files["game_list.tsv"] == nil {
		return nil, fmt.Errorf("embedded game list is missing")
	}
	return bundle, nil
}

func (b *Bundle) File(path string) ([]byte, bool) {
	data, exists := b.Files[path]
	return append([]byte(nil), data...), exists
}
