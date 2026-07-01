// White-box: parseSigner is unexported and takes an injected passphrase prompt,
// letting us cover the encrypted-key decrypt paths that LoadSigner cannot reach
// through the exported API without a real terminal.
package keys

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func plainKeyPEM(t *testing.T) []byte {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	return pem.EncodeToMemory(block)
}

func encryptedKeyPEM(t *testing.T, passphrase string) []byte {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	block, err := ssh.MarshalPrivateKeyWithPassphrase(priv, "", []byte(passphrase))
	if err != nil {
		t.Fatalf("marshal encrypted key: %v", err)
	}
	return pem.EncodeToMemory(block)
}

func TestParseSigner(t *testing.T) {
	failPrompt := func(string) ([]byte, error) {
		t.Helper()
		t.Fatal("prompt should not be called for an unencrypted key")
		return nil, nil
	}

	t.Run("unencrypted key skips the prompt", func(t *testing.T) {
		signer, err := parseSigner(plainKeyPEM(t), "id_ed25519", failPrompt)
		if err != nil || signer == nil {
			t.Fatalf("parseSigner = (%v, %v), want a signer and nil error", signer, err)
		}
	})

	t.Run("encrypted key with correct passphrase", func(t *testing.T) {
		data := encryptedKeyPEM(t, "hunter2")
		signer, err := parseSigner(data, "id_ed25519", func(string) ([]byte, error) {
			return []byte("hunter2"), nil
		})
		if err != nil || signer == nil {
			t.Fatalf("parseSigner = (%v, %v), want a signer and nil error", signer, err)
		}
	})

	t.Run("encrypted key with wrong passphrase", func(t *testing.T) {
		data := encryptedKeyPEM(t, "hunter2")
		_, err := parseSigner(data, "id_ed25519", func(string) ([]byte, error) {
			return []byte("nope"), nil
		})
		if err == nil || !strings.Contains(err.Error(), "decrypt private key") {
			t.Errorf("err = %v, want a decrypt error", err)
		}
	})

	t.Run("prompt error propagates", func(t *testing.T) {
		data := encryptedKeyPEM(t, "hunter2")
		sentinel := errors.New("no terminal")
		_, err := parseSigner(data, "id_ed25519", func(string) ([]byte, error) {
			return nil, sentinel
		})
		if !errors.Is(err, sentinel) {
			t.Errorf("err = %v, want the prompt's error", err)
		}
	})

	t.Run("malformed key errors without prompting", func(t *testing.T) {
		_, err := parseSigner([]byte("not a key"), "id_ed25519", failPrompt)
		if err == nil || !strings.Contains(err.Error(), "parse private key") {
			t.Errorf("err = %v, want a parse error", err)
		}
	})
}
