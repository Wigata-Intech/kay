// Package config defines the persistent on-disk model for kay: the set of
// keys, users (login users live on the server record) and servers the operator
// has registered. State is a single human-inspectable JSON file; private keys
// are never stored in it (only paths to PEM files on disk).
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// KeyType enumerates the supported key algorithms.
type KeyType string

const (
	KeyEd25519 KeyType = "ed25519"
	KeyRSA     KeyType = "rsa"
)

// Key is a locally generated key pair, referenced by servers via Name.
type Key struct {
	Name        string    `json:"name"`
	Type        KeyType   `json:"type"`
	PrivatePath string    `json:"privatePath"`
	PublicPath  string    `json:"publicPath"`
	Fingerprint string    `json:"fingerprint"`
	CreatedAt   time.Time `json:"createdAt"`
}

// Server is a registered remote host plus the login user and key to use.
type Server struct {
	Alias   string `json:"alias"`
	Host    string `json:"host"`
	Port    int    `json:"port"`
	User    string `json:"user"`
	KeyName string `json:"keyName"`
}

// Addr returns the host:port string used for dialing.
func (s Server) Addr() string {
	return fmt.Sprintf("%s:%d", s.Host, s.Port)
}

// Store is the whole persisted state.
type Store struct {
	Keys    []Key    `json:"keys"`
	Servers []Server `json:"servers"`

	dir  string // resolved config dir (not serialised)
	path string // resolved config.json path (not serialised)
}

// Dir returns the resolved configuration directory.
func (s *Store) Dir() string { return s.dir }

// KeysDir returns the directory where private/public key files live.
func (s *Store) KeysDir() string { return filepath.Join(s.dir, "keys") }

// KnownHostsPath returns the path to the TOFU known_hosts file.
func (s *Store) KnownHostsPath() string { return filepath.Join(s.dir, "known_hosts") }

// defaultDir resolves <UserConfigDir>/kay, honouring KAY_HOME for tests.
func defaultDir() (string, error) {
	if h := os.Getenv("KAY_HOME"); h != "" {
		return h, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "kay"), nil
}

// Load reads the store from disk, creating directories as needed. A missing
// config file is not an error: it yields an empty, ready-to-use store.
func Load() (*Store, error) {
	dir, err := defaultDir()
	if err != nil {
		return nil, err
	}
	return LoadFrom(dir)
}

// LoadFrom loads the store rooted at a specific directory (used by tests).
func LoadFrom(dir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, "keys"), 0o700); err != nil {
		return nil, err
	}
	s := &Store{dir: dir, path: filepath.Join(dir, "config.json")}
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, s); err != nil {
		return nil, fmt.Errorf("parse %s: %w", s.path, err)
	}
	return s, nil
}

// Save atomically writes the store to disk with restrictive permissions.
func (s *Store) Save() error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// FindKey returns the named key, or an error if it does not exist.
func (s *Store) FindKey(name string) (*Key, error) {
	for i := range s.Keys {
		if s.Keys[i].Name == name {
			return &s.Keys[i], nil
		}
	}
	return nil, fmt.Errorf("no key named %q", name)
}

// FindServer returns the server with the given alias, or an error.
func (s *Store) FindServer(alias string) (*Server, error) {
	for i := range s.Servers {
		if s.Servers[i].Alias == alias {
			return &s.Servers[i], nil
		}
	}
	return nil, fmt.Errorf("no server with alias %q", alias)
}

// AddKey appends a key, rejecting duplicate names.
func (s *Store) AddKey(k Key) error {
	if _, err := s.FindKey(k.Name); err == nil {
		return fmt.Errorf("key %q already exists", k.Name)
	}
	s.Keys = append(s.Keys, k)
	return nil
}

// AddServer appends a server, rejecting duplicate aliases and verifying the
// referenced key exists.
func (s *Store) AddServer(srv Server) error {
	if _, err := s.FindServer(srv.Alias); err == nil {
		return fmt.Errorf("server %q already exists", srv.Alias)
	}
	if _, err := s.FindKey(srv.KeyName); err != nil {
		return fmt.Errorf("server %q references unknown key %q", srv.Alias, srv.KeyName)
	}
	s.Servers = append(s.Servers, srv)
	return nil
}

// RemoveServer deletes a server by alias.
func (s *Store) RemoveServer(alias string) error {
	for i := range s.Servers {
		if s.Servers[i].Alias == alias {
			s.Servers = append(s.Servers[:i], s.Servers[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("no server with alias %q", alias)
}
