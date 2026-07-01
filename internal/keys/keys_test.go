package keys_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wigata-Intech/kay/internal/config"
	"github.com/Wigata-Intech/kay/internal/keys"

	"golang.org/x/crypto/ssh"
)

// TestGenerate covers key generation. Positive cases (supported types) come
// first and run the full generate → write → load → read flow; the error cases
// (unsupported type, too-small RSA) follow.
func TestGenerate(t *testing.T) {
	tests := []struct {
		name    string
		typ     config.KeyType
		bits    int
		wantErr bool
	}{
		{"ed25519", config.KeyEd25519, 0, false},
		{"rsa-2048", config.KeyRSA, 2048, false},
		{"unsupported type", "dsa", 0, true},
		{"rsa too small", config.KeyRSA, 512, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pair, err := keys.Generate(tt.typ, tt.bits, "test")
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Generate(%s, %d) = nil error, want error", tt.typ, tt.bits)
				}
				return
			}
			if err != nil {
				t.Fatalf("generate: %v", err)
			}
			if !strings.HasPrefix(pair.Fingerprint, "SHA256:") {
				t.Errorf("unexpected fingerprint %q", pair.Fingerprint)
			}
			// Public output must parse as an authorized_keys line.
			if _, _, _, _, err := ssh.ParseAuthorizedKey(pair.PublicAuth); err != nil {
				t.Errorf("public key not parseable: %v", err)
			}

			dir := t.TempDir()
			privPath, pubPath, err := pair.Write(dir, "id")
			if err != nil {
				t.Fatalf("write: %v", err)
			}
			if filepath.Dir(privPath) != dir {
				t.Errorf("unexpected priv path %q", privPath)
			}
			// Private key must load into a usable signer whose public key matches.
			signer, err := keys.LoadSigner(privPath)
			if err != nil {
				t.Fatalf("load signer: %v", err)
			}
			got := ssh.MarshalAuthorizedKey(signer.PublicKey())
			if strings.TrimSpace(string(got)) != strings.TrimSpace(string(pair.PublicAuth)) {
				t.Errorf("signer public key does not match generated public key")
			}
			if _, err := keys.ReadPublic(pubPath); err != nil {
				t.Errorf("read public: %v", err)
			}
			// Writing over an existing file must fail.
			if _, _, err := pair.Write(dir, "id"); err == nil {
				t.Errorf("expected error writing over existing key")
			}
		})
	}
}
