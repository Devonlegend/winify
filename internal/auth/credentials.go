package auth

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Devonlegend/winify/internal/models"
)

// MasterKeySize is the required AES-256 key length in bytes.
const MasterKeySize = 32

// CredentialStore encrypts secret values (SSH keys, WinRM passwords, API
// tokens) at rest with AES-256-GCM. Decrypted values exist only in memory and
// are returned to the caller; nothing here logs them.
type CredentialStore struct {
	store *models.Store
	aead  cipher.AEAD
}

// NewCredentialStore builds the store from a 32-byte key.
func NewCredentialStore(store *models.Store, key []byte) (*CredentialStore, error) {
	if len(key) != MasterKeySize {
		return nil, fmt.Errorf("master key must be %d bytes, got %d", MasterKeySize, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("new cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("new gcm: %w", err)
	}
	return &CredentialStore{store: store, aead: aead}, nil
}

// Put encrypts secret under name and stores nonce+ciphertext. The name is used
// as additional authenticated data, so a ciphertext cannot be moved to another
// credential name without detection.
func (c *CredentialStore) Put(ctx context.Context, name, secret string) error {
	nonce, ciphertext, err := c.seal(name, secret)
	if err != nil {
		return err
	}
	return c.store.PutCredential(ctx, name, nonce, ciphertext)
}

// Encrypt seals a value for the models layer's at-rest configuration codec.
// The caller supplies a stable, non-secret AAD name such as project:<id>:env.
func (c *CredentialStore) Encrypt(name, value string) (string, error) {
	nonce, ciphertext, err := c.seal(name, value)
	if err != nil {
		return "", err
	}
	payload := append(nonce, ciphertext...)
	return base64.RawStdEncoding.EncodeToString(payload), nil
}

// Decrypt opens a value produced by Encrypt.
func (c *CredentialStore) Decrypt(name, value string) (string, error) {
	payload, err := base64.RawStdEncoding.DecodeString(value)
	if err != nil {
		return "", fmt.Errorf("decode encrypted %s: %w", name, err)
	}
	if len(payload) < c.aead.NonceSize() {
		return "", fmt.Errorf("encrypted %s is truncated", name)
	}
	nonce := payload[:c.aead.NonceSize()]
	ciphertext := payload[c.aead.NonceSize():]
	plaintext, err := c.aead.Open(nil, nonce, ciphertext, []byte(name))
	if err != nil {
		return "", fmt.Errorf("decrypt %s: %w", name, err)
	}
	return string(plaintext), nil
}

func (c *CredentialStore) seal(name, value string) (nonce, ciphertext []byte, err error) {
	nonce = make([]byte, c.aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, nil, fmt.Errorf("generate nonce: %w", err)
	}
	ciphertext = c.aead.Seal(nil, nonce, []byte(value), []byte(name))
	return nonce, ciphertext, nil
}

// Get loads and decrypts the credential named name. Errors mention the name
// only — never the plaintext, which is not available on failure anyway.
func (c *CredentialStore) Get(ctx context.Context, name string) (string, error) {
	nonce, ciphertext, err := c.store.Credential(ctx, name)
	if err != nil {
		return "", err
	}
	plaintext, err := c.aead.Open(nil, nonce, ciphertext, []byte(name))
	if err != nil {
		return "", fmt.Errorf("decrypt credential %q: %w", name, err)
	}
	return string(plaintext), nil
}

// Delete removes a stored credential. Removing a credential that is still
// referenced by a server or project will break that connection; callers should
// warn the operator.
func (c *CredentialStore) Delete(ctx context.Context, name string) error {
	return c.store.DeleteCredential(ctx, name)
}

// LoadMasterKey resolves the AES master key. Precedence:
//  1. explicit base64 key (CC_MASTER_KEY env or config)
//  2. an existing key file at path
//  3. a freshly generated key written to path with 0600 permissions
//
// The key is never logged.
func LoadMasterKey(explicitBase64, path string) ([]byte, error) {
	if explicitBase64 != "" {
		if strings.TrimSpace(path) != "" {
			if err := prepareMasterKeyDir(path); err != nil {
				return nil, err
			}
		}
		return decodeKey(explicitBase64, "master key")
	}
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("master key path is required")
	}
	if err := prepareMasterKeyDir(path); err != nil {
		return nil, err
	}

	// Reject symlinks and non-regular files. The key is a trust anchor: an
	// attacker who can replace it can decrypt every stored credential.
	data, err := readMasterKeyFile(path)
	switch {
	case err == nil:
		return decodeKey(strings.TrimSpace(string(data)), path)
	case !errors.Is(err, os.ErrNotExist):
		return nil, fmt.Errorf("read master key %s: %w", path, err)
	}

	key := make([]byte, MasterKeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate master key: %w", err)
	}
	encoded := base64.StdEncoding.EncodeToString(key)
	// O_EXCL prevents two processes starting together from overwriting one
	// another's trust anchor. If another process won the race, read its key.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		data, readErr := readMasterKeyFile(path)
		if readErr != nil {
			return nil, fmt.Errorf("read concurrently created master key %s: %w", path, readErr)
		}
		return decodeKey(strings.TrimSpace(string(data)), path)
	}
	if err != nil {
		return nil, fmt.Errorf("create master key %s: %w", path, err)
	}
	if _, err := f.WriteString(encoded); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("write master key %s: %w", path, err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("sync master key %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return nil, fmt.Errorf("close master key %s: %w", path, err)
	}
	return key, nil
}

func prepareMasterKeyDir(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create key dir: %w", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("stat key dir: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("key parent %s is not a directory", dir)
	}
	if info.Mode().Perm()&0o077 != 0 {
		if err := os.Chmod(dir, 0o700); err != nil {
			return fmt.Errorf("secure key dir: %w", err)
		}
	}
	return nil
}

func readMasterKeyFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("master key %s is not a regular file", path)
	}
	if info.Mode().Perm()&0o077 != 0 {
		if err := os.Chmod(path, 0o600); err != nil {
			return nil, fmt.Errorf("secure master key %s: %w", path, err)
		}
	}
	return os.ReadFile(path)
}

func decodeKey(encoded, source string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("%s: invalid base64: %w", source, err)
	}
	if len(key) != MasterKeySize {
		return nil, fmt.Errorf("%s: decoded to %d bytes, want %d", source, len(key), MasterKeySize)
	}
	return key, nil
}
