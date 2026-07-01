package keys_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Wigata-Intech/kay/internal/config"
	"github.com/Wigata-Intech/kay/internal/keys"
)

// TestGenerateRSADefaultBits covers the bits<=0 branch, which defaults RSA to
// 3072 (the existing tests always pass an explicit size).
func TestGenerateRSADefaultBits(t *testing.T) {
	pair, err := keys.Generate(config.KeyRSA, 0, "test")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(pair.PrivatePEM) == 0 || len(pair.PublicAuth) == 0 {
		t.Fatalf("generate returned empty key material")
	}
}

// TestLoadSignerErrors covers the failure paths of LoadSigner: a missing file
// and a file whose contents are not a valid PEM private key.
func TestLoadSignerErrors(t *testing.T) {
	dir := t.TempDir()

	garbage := filepath.Join(dir, "garbage")
	if err := os.WriteFile(garbage, []byte("not a pem key"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	tests := []struct {
		name string
		path string
	}{
		{"missing file", filepath.Join(dir, "does-not-exist")},
		{"invalid pem", garbage},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := keys.LoadSigner(tt.path); err == nil {
				t.Errorf("LoadSigner(%s) = nil error, want error", tt.name)
			}
		})
	}
}

// TestReadPublicMissing covers ReadPublic on a nonexistent file.
func TestReadPublicMissing(t *testing.T) {
	if _, err := keys.ReadPublic(filepath.Join(t.TempDir(), "absent.pub")); err == nil {
		t.Errorf("ReadPublic on missing file = nil error, want error")
	}
}
