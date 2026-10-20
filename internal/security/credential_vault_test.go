package security

import (
	"bytes"
	"encoding/base64"
	"errors"
	"testing"
)

func TestCredentialVaultEncryptDecryptAndRejectTampering(t *testing.T) {
	key := bytes.Repeat([]byte{0x31}, 32)
	vault, err := NewCredentialVault(key)
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := vault.EncryptCredentials([]byte("sensitive-value"))
	if err != nil {
		t.Fatal(err)
	}
	if !vault.IsEncrypted(encrypted) {
		t.Fatalf("missing envelope: %q", encrypted)
	}
	if keyID, ok := vault.KeyID(encrypted); !ok || keyID != "default" {
		t.Fatalf("KeyID() = %q, %v", keyID, ok)
	}
	plaintext, err := vault.DecryptCredentials(encrypted)
	if err != nil {
		t.Fatal(err)
	}
	if string(plaintext) != "sensitive-value" {
		t.Fatalf("decrypted = %q", plaintext)
	}
	tampered := encrypted[:len(encrypted)-1] + "A"
	if _, err := vault.DecryptCredentials(tampered); !errors.Is(err, ErrInvalidCiphertext) {
		t.Fatalf("tamper error = %v", err)
	}
	if _, err := vault.DecryptCredentials("plaintext"); !errors.Is(err, ErrCredentialNotEncrypted) {
		t.Fatalf("plaintext error = %v", err)
	}
}

func TestCredentialVaultReencryptsWithActiveKey(t *testing.T) {
	oldKey, newKey := bytes.Repeat([]byte{0x11}, 32), bytes.Repeat([]byte{0x22}, 32)
	oldVault, err := NewCredentialVaultWithKeyring("old", map[string][]byte{"old": oldKey})
	if err != nil {
		t.Fatal(err)
	}
	oldCiphertext, err := oldVault.EncryptCredentials([]byte("rotate-me"))
	if err != nil {
		t.Fatal(err)
	}
	newVault, err := NewCredentialVaultWithKeyring("new", map[string][]byte{"old": oldKey, "new": newKey})
	if err != nil {
		t.Fatal(err)
	}
	newCiphertext, err := newVault.ReencryptCredentials(oldCiphertext)
	if err != nil {
		t.Fatal(err)
	}
	if id, _ := newVault.KeyID(newCiphertext); id != "new" {
		t.Fatalf("key ID = %q", id)
	}
	plaintext, err := newVault.DecryptCredentials(newCiphertext)
	if err != nil || string(plaintext) != "rotate-me" {
		t.Fatalf("decrypt rotated value = %q, %v", plaintext, err)
	}
	withoutOld, err := NewCredentialVaultWithKeyring("new", map[string][]byte{"new": newKey})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := withoutOld.DecryptCredentials(oldCiphertext); !errors.Is(err, ErrUnknownCredentialKey) {
		t.Fatalf("missing old key error = %v", err)
	}
}

func TestLoadCredentialVaultFromEnv(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x33}, 32))
	t.Setenv("DATA_FABRIC_CREDENTIAL_ACTIVE_KEY_ID", "test")
	t.Setenv("DATA_FABRIC_CREDENTIAL_KEYS", "{\"test\":\""+key+"\"}")
	vault, err := LoadCredentialVaultFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if vault.ActiveKeyID() != "test" {
		t.Fatalf("active ID = %q", vault.ActiveKeyID())
	}
	t.Setenv("DATA_FABRIC_CREDENTIAL_ACTIVE_KEY_ID", "")
	if _, err := LoadCredentialVaultFromEnv(); !errors.Is(err, ErrCredentialVaultConfig) {
		t.Fatalf("missing config error = %v", err)
	}
}
