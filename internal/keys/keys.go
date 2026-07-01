// Package keys generates and encodes SSH key pairs (ed25519 or RSA) using only
// the standard library plus golang.org/x/crypto/ssh for SSH wire encoding.
package keys

import (
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Wigata-Intech/kay/internal/config"

	"golang.org/x/crypto/ssh"
	"golang.org/x/term"
)

// Pair holds the encoded private (OpenSSH PEM) and public (authorized_keys
// line) representations of a freshly generated key, plus its fingerprint.
type Pair struct {
	PrivatePEM  []byte
	PublicAuth  []byte // single authorized_keys line, newline-terminated
	Fingerprint string
}

// Generate creates a new key pair. For RSA, bits defaults to 3072 when <=0.
func Generate(t config.KeyType, bits int, comment string) (*Pair, error) {
	var privKey crypto.PrivateKey
	var pubKey crypto.PublicKey

	switch t {
	case config.KeyEd25519:
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, err
		}
		privKey, pubKey = priv, pub
	case config.KeyRSA:
		if bits <= 0 {
			bits = 3072
		}
		if bits < 2048 {
			return nil, fmt.Errorf("rsa key size %d too small; use at least 2048", bits)
		}
		priv, err := rsa.GenerateKey(rand.Reader, bits)
		if err != nil {
			return nil, err
		}
		privKey, pubKey = priv, priv.Public()
	default:
		return nil, fmt.Errorf("unsupported key type %q", t)
	}

	block, err := ssh.MarshalPrivateKey(privKey, comment)
	if err != nil {
		return nil, fmt.Errorf("encode private key: %w", err)
	}
	sshPub, err := ssh.NewPublicKey(pubKey)
	if err != nil {
		return nil, fmt.Errorf("encode public key: %w", err)
	}

	return &Pair{
		PrivatePEM:  pem.EncodeToMemory(block),
		PublicAuth:  ssh.MarshalAuthorizedKey(sshPub),
		Fingerprint: ssh.FingerprintSHA256(sshPub),
	}, nil
}

// Write stores the pair as <dir>/<name> (private, 0600) and <dir>/<name>.pub
// (public, 0644) and returns the two paths.
func (p *Pair) Write(dir, name string) (privPath, pubPath string, err error) {
	if err = os.MkdirAll(dir, 0o700); err != nil {
		return "", "", err
	}
	privPath = filepath.Join(dir, name)
	pubPath = privPath + ".pub"
	if _, statErr := os.Stat(privPath); statErr == nil {
		return "", "", fmt.Errorf("key file %s already exists", privPath)
	}
	if err = os.WriteFile(privPath, p.PrivatePEM, 0o600); err != nil {
		return "", "", err
	}
	if err = os.WriteFile(pubPath, p.PublicAuth, 0o644); err != nil {
		return "", "", err
	}
	return privPath, pubPath, nil
}

// LoadSigner reads a private key PEM file and returns an ssh.Signer for auth.
// If the key is passphrase-protected it prompts for the passphrase (no echo).
func LoadSigner(privPath string) (ssh.Signer, error) {
	data, err := os.ReadFile(privPath)
	if err != nil {
		return nil, err
	}
	signer, err := ssh.ParsePrivateKey(data)
	if err == nil {
		return signer, nil
	}
	var missing *ssh.PassphraseMissingError
	if !errors.As(err, &missing) {
		return nil, fmt.Errorf("parse private key %s: %w", privPath, err)
	}
	pass, perr := promptPassphrase(filepath.Base(privPath))
	if perr != nil {
		return nil, perr
	}
	signer, err = ssh.ParsePrivateKeyWithPassphrase(data, pass)
	if err != nil {
		return nil, fmt.Errorf("decrypt private key %s: %w", privPath, err)
	}
	return signer, nil
}

// promptPassphrase reads a passphrase from the terminal without echoing it.
func promptPassphrase(name string) ([]byte, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return nil, fmt.Errorf("key %q is passphrase-protected but no terminal is available to prompt", name)
	}
	fmt.Fprintf(os.Stderr, "Enter passphrase for key %q: ", name)
	pass, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	return pass, err
}

// ReadPublic returns the stored authorized_keys line for display/installation.
func ReadPublic(pubPath string) (string, error) {
	data, err := os.ReadFile(pubPath)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
