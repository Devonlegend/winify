package auth

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Devonlegend/winify/internal/models"
)

func newTestCredStore(t *testing.T) (*CredentialStore, *models.Store) {
	t.Helper()
	db, err := models.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := models.Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	store := models.NewStore(db)

	key := make([]byte, MasterKeySize)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	cs, err := NewCredentialStore(store, key)
	if err != nil {
		t.Fatalf("NewCredentialStore: %v", err)
	}
	return cs, store
}

// TestCredentialRoundTrip is the "encrypt, store, decrypt" proof: the value
// survives the round trip, and the stored ciphertext does not contain the
// plaintext.
func TestCredentialRoundTrip(t *testing.T) {
	cs, store := newTestCredStore(t)
	ctx := context.Background()
	const name = "server-001-winrm"
	const secret = "correct horse battery staple"

	if err := cs.Put(ctx, name, secret); err != nil {
		t.Fatalf("Put: %v", err)
	}

	_, ciphertext, err := store.Credential(ctx, name)
	if err != nil {
		t.Fatalf("Credential: %v", err)
	}
	if bytes.Contains(ciphertext, []byte(secret)) {
		t.Fatal("stored ciphertext contains the plaintext secret")
	}

	got, err := cs.Get(ctx, name)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != secret {
		t.Fatalf("Get = %q, want %q", got, secret)
	}
}

// TestCredentialNameIsAuthenticated proves the credential name is bound as
// additional authenticated data: moving the blob to another name must fail.
func TestCredentialNameIsAuthenticated(t *testing.T) {
	cs, store := newTestCredStore(t)
	ctx := context.Background()

	if err := cs.Put(ctx, "alpha", "value-alpha"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	nonce, ciphertext, err := store.Credential(ctx, "alpha")
	if err != nil {
		t.Fatalf("Credential: %v", err)
	}
	if err := store.PutCredential(ctx, "beta", nonce, ciphertext); err != nil {
		t.Fatalf("PutCredential: %v", err)
	}
	if _, err := cs.Get(ctx, "beta"); err == nil {
		t.Fatal("Get with wrong name succeeded; AAD binding is not enforced")
	}
}

func TestCredentialStoreConfigCodec(t *testing.T) {
	cs, _ := newTestCredStore(t)
	encoded, err := cs.Encrypt("project:p1:env", `{"TOKEN":"secret"}`)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if strings.Contains(encoded, "secret") {
		t.Fatalf("encoded config value contains plaintext: %s", encoded)
	}
	decoded, err := cs.Decrypt("project:p1:env", encoded)
	if err != nil || decoded != `{"TOKEN":"secret"}` {
		t.Fatalf("Decrypt = %q, %v", decoded, err)
	}
}

func TestCredentialMissing(t *testing.T) {
	cs, _ := newTestCredStore(t)
	if _, err := cs.Get(context.Background(), "nope"); err == nil {
		t.Fatal("Get on missing credential returned nil error")
	}
}

func TestLoadMasterKeyGeneratesAndPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "master.key")

	first, err := LoadMasterKey("", path)
	if err != nil {
		t.Fatalf("first LoadMasterKey: %v", err)
	}
	if len(first) != MasterKeySize {
		t.Fatalf("key length = %d, want %d", len(first), MasterKeySize)
	}
	second, err := LoadMasterKey("", path)
	if err != nil {
		t.Fatalf("second LoadMasterKey: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("generated key was not persisted")
	}
}

func TestLoadMasterKeyTightensExistingPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "master.key")
	key := make([]byte, MasterKeySize)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	encoded := base64.StdEncoding.EncodeToString(key)
	if err := os.WriteFile(path, []byte(encoded), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadMasterKey("", path); err != nil {
		t.Fatalf("LoadMasterKey: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("key permissions = %o, want 600", info.Mode().Perm())
	}
}

func TestLoadMasterKeyRejectsBadBase64(t *testing.T) {
	if _, err := LoadMasterKey("not-base64!!", ""); err == nil {
		t.Fatal("LoadMasterKey accepted invalid base64")
	}
}

func TestLoadMasterKeyRejectsWrongLength(t *testing.T) {
	// base64 of a 4-byte value
	if _, err := LoadMasterKey("AAAAAA==", ""); err == nil || !strings.Contains(err.Error(), "want 32") {
		t.Fatalf("LoadMasterKey wrong-length error = %v, want length error", err)
	}
}
