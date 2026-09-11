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
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return fmt.Errorf("generate nonce: %w", err)
	}
	ciphertext := c.aead.Seal(nil, nonce, []byte(secret), []byte(name))
	return c.store.PutCredential(ctx, name, nonce, ciphertext)
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
		return decodeKey(explicitBase64, "master key")
	}

	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		return decodeKey(strings.TrimSpace(string(data)), path)
	case errors.Is(err, os.ErrNotExist):
		// Fall through to generate.
	default:
		return nil, fmt.Errorf("read master key %s: %w", path, err)
	}

	key := make([]byte, MasterKeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate master key: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create key dir: %w", err)
	}
	encoded := base64.StdEncoding.EncodeToString(key)
	if err := os.WriteFile(path, []byte(encoded), 0o600); err != nil {
		return nil, fmt.Errorf("write master key %s: %w", path, err)
	}
	return key, nil
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
