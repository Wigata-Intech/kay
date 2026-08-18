// Package keys stores kay's SSH key pairs on disk and loads them for auth.
// Generation and parsing are delegated to w-tools/x/sshx/keys; this package
// keeps only what is kay's own: file layout, permissions, and the terminal
// passphrase prompt.
package keys

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Wigata-Intech/kay/internal/config"

	wkeys "github.com/Wigata-Intech/w-tools/x/sshx/keys"
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
	var alg wkeys.Algorithm
	switch t {
	case config.KeyEd25519:
		alg = wkeys.Ed25519
	case config.KeyRSA:
		alg = wkeys.RSA
		if bits < 0 {
			bits = 0 // library default (3072)
		}
	default:
		return nil, fmt.Errorf("unsupported key type %q", t)
	}
	p, err := wkeys.Generate(alg, bits, comment)
	if err != nil {
		return nil, err
	}
	return &Pair{
		PrivatePEM:  p.PrivatePEM,
		PublicAuth:  p.PublicAuthorized,
		Fingerprint: p.Fingerprint,
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
	if err = os.WriteFile(pubPath, p.PublicAuth, 0o644); err != nil { //#nosec G306 -- a public key is meant to be world-readable
		return "", "", err
	}
	return privPath, pubPath, nil
}

// PassphraseFunc supplies the passphrase for an encrypted key, given the
// key file's base name.
type PassphraseFunc func(name string) ([]byte, error)

// LoadSigner reads a private key PEM file and returns an ssh.Signer for auth.
// If the key is passphrase-protected it prompts on the terminal (no echo).
func LoadSigner(privPath string) (ssh.Signer, error) {
	return LoadSignerWith(privPath, promptPassphrase)
}

// LoadSignerWith is LoadSigner with the passphrase prompt injected — the
// console supplies a masked modal, the CLI keeps the terminal prompt.
func LoadSignerWith(privPath string, prompt PassphraseFunc) (ssh.Signer, error) {
	signer, err := wkeys.Load(privPath, func(path string) ([]byte, error) {
		return prompt(filepath.Base(path))
	})
	if err != nil {
		return nil, fmt.Errorf("load key %s: %w", privPath, err)
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
	data, err := os.ReadFile(pubPath) //#nosec G304 -- path comes from kay's own key store, not untrusted input
	if err != nil {
		return "", err
	}
	return string(data), nil
}
