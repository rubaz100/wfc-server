package assets

import (
	"bytes"
	"strings"
	"testing"
)

func TestEmbeddedManifestAndTemplates(t *testing.T) {
	bundle, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.Files) != 23 {
		t.Fatalf("embedded assets = %d, want 23", len(bundle.Files))
	}
	if !bytes.Equal(bundle.Files["stage1.bin"], bundle.Files["stage1v1.bin"]) {
		t.Fatal("stage 1 versions differ")
	}
	for path, data := range bundle.Files {
		if strings.Contains(strings.ToLower(path), "key") || strings.HasSuffix(path, ".pem") {
			t.Fatalf("private key-like artifact embedded: %s", path)
		}
		if strings.HasPrefix(path, "sbcm/") || strings.HasPrefix(path, "stage1") {
			if bytes.Count(data, bundle.TemplatePublicKey) != 1 {
				t.Fatalf("%s does not contain exactly one template key", path)
			}
		}
	}
}
