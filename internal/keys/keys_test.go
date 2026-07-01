package keys

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wigata-Intech/kay/internal/config"

	"golang.org/x/crypto/ssh"
)

func TestGenerateAndLoad(t *testing.T) {
	for _, tc := range []struct {
		typ  config.KeyType
		bits int
	}{
		{config.KeyEd25519, 0},
		{config.KeyRSA, 2048},
	} {
		pair, err := Generate(tc.typ, tc.bits, "test")
		if err != nil {
			t.Fatalf("%s generate: %v", tc.typ, err)
		}
		if !strings.HasPrefix(pair.Fingerprint, "SHA256:") {
			t.Errorf("%s: unexpected fingerprint %q", tc.typ, pair.Fingerprint)
		}
		// Public output must parse as an authorized_keys line.
		if _, _, _, _, err := ssh.ParseAuthorizedKey(pair.PublicAuth); err != nil {
			t.Errorf("%s: public key not parseable: %v", tc.typ, err)
		}

		dir := t.TempDir()
		privPath, pubPath, err := pair.Write(dir, "id")
		if err != nil {
			t.Fatalf("%s write: %v", tc.typ, err)
		}
		if filepath.Dir(privPath) != dir {
			t.Errorf("unexpected priv path %q", privPath)
		}
		// Private key must load into a usable signer whose public key matches.
		signer, err := LoadSigner(privPath)
		if err != nil {
			t.Fatalf("%s load signer: %v", tc.typ, err)
		}
		got := ssh.MarshalAuthorizedKey(signer.PublicKey())
		if strings.TrimSpace(string(got)) != strings.TrimSpace(string(pair.PublicAuth)) {
			t.Errorf("%s: signer public key does not match generated public key", tc.typ)
		}
		if _, err := ReadPublic(pubPath); err != nil {
			t.Errorf("%s read public: %v", tc.typ, err)
		}
		// Writing over an existing file must fail.
		if _, _, err := pair.Write(dir, "id"); err == nil {
			t.Errorf("%s: expected error writing over existing key", tc.typ)
		}
	}
}

func TestGenerateRejectsBadInput(t *testing.T) {
	if _, err := Generate("dsa", 0, ""); err == nil {
		t.Error("expected error for unsupported type")
	}
	if _, err := Generate(config.KeyRSA, 512, ""); err == nil {
		t.Error("expected error for too-small RSA key")
	}
}
