package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

const credentialCiphertextPrefix = "dfcred:v1:"

var (
	ErrInvalidCiphertext      = errors.New("invalid ciphertext")
	ErrInvalidKeyLength       = errors.New("encryption key must be 32 bytes for AES-256")
	ErrCredentialNotEncrypted = errors.New("credential is not encrypted")
	ErrUnknownCredentialKey   = errors.New("credential encryption key is unavailable")
	ErrCredentialVaultConfig  = errors.New("credential vault configuration is invalid")
)

type CredentialVault struct {
	activeKeyID string
	keys        map[string][]byte
}

// NewCredentialVault preserves the single-key constructor for tests and legacy callers.
func NewCredentialVault(masterKey []byte) (*CredentialVault, error) {
	return NewCredentialVaultWithKeyring("default", map[string][]byte{"default": masterKey})
}

func NewCredentialVaultWithKeyring(activeKeyID string, keys map[string][]byte) (*CredentialVault, error) {
	if activeKeyID == "" || strings.Contains(activeKeyID, ":") || len(keys) == 0 {
		return nil, fmt.Errorf("%w: active key ID and at least one key are required", ErrCredentialVaultConfig)
	}
	copied := make(map[string][]byte, len(keys))
	for id, key := range keys {
		if id == "" || strings.Contains(id, ":") {
			return nil, fmt.Errorf("%w: invalid key ID", ErrCredentialVaultConfig)
		}
		if len(key) != 32 {
			return nil, fmt.Errorf("%w: key %q: %w", ErrCredentialVaultConfig, id, ErrInvalidKeyLength)
		}
		copied[id] = append([]byte(nil), key...)
	}
	if _, ok := copied[activeKeyID]; !ok {
		return nil, fmt.Errorf("%w: active key %q is missing", ErrCredentialVaultConfig, activeKeyID)
	}
	return &CredentialVault{activeKeyID: activeKeyID, keys: copied}, nil
}

func (cv *CredentialVault) ActiveKeyID() string { return cv.activeKeyID }

func LoadCredentialVaultFromEnv() (*CredentialVault, error) {
	activeID := strings.TrimSpace(os.Getenv("DATA_FABRIC_CREDENTIAL_ACTIVE_KEY_ID"))
	encoded := strings.TrimSpace(os.Getenv("DATA_FABRIC_CREDENTIAL_KEYS"))
	if activeID == "" || encoded == "" {
		return nil, fmt.Errorf("%w: set DATA_FABRIC_CREDENTIAL_ACTIVE_KEY_ID and DATA_FABRIC_CREDENTIAL_KEYS", ErrCredentialVaultConfig)
	}
	var raw map[string]string
	if err := json.Unmarshal([]byte(encoded), &raw); err != nil {
		return nil, fmt.Errorf("%w: DATA_FABRIC_CREDENTIAL_KEYS must be a JSON object of base64 keys", ErrCredentialVaultConfig)
	}
	keys := make(map[string][]byte, len(raw))
	for id, encodedKey := range raw {
		key, err := base64.StdEncoding.DecodeString(encodedKey)
		if err != nil {
			return nil, fmt.Errorf("%w: key %q is not valid base64", ErrCredentialVaultConfig, id)
		}
		keys[id] = key
	}
	return NewCredentialVaultWithKeyring(activeID, keys)
}

func (cv *CredentialVault) EncryptCredentials(plaintext []byte) (string, error) {
	key := cv.keys[cv.activeKeyID]
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("create GCM: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}
	aad := []byte(credentialCiphertextPrefix + cv.activeKeyID)
	payload := gcm.Seal(nonce, nonce, plaintext, aad)
	return aadString(cv.activeKeyID) + base64.StdEncoding.EncodeToString(payload), nil
}

func aadString(keyID string) string { return credentialCiphertextPrefix + keyID + ":" }

func (cv *CredentialVault) IsEncrypted(value string) bool {
	return strings.HasPrefix(value, "dfcred:")
}

func (cv *CredentialVault) KeyID(value string) (string, bool) {
	if !strings.HasPrefix(value, credentialCiphertextPrefix) {
		return "", false
	}
	id, _, ok := strings.Cut(strings.TrimPrefix(value, credentialCiphertextPrefix), ":")
	return id, ok && id != ""
}

func (cv *CredentialVault) DecryptCredentials(envelope string) ([]byte, error) {
	if !cv.IsEncrypted(envelope) {
		return nil, ErrCredentialNotEncrypted
	}
	if !strings.HasPrefix(envelope, credentialCiphertextPrefix) {
		return nil, fmt.Errorf("%w: unsupported envelope version", ErrInvalidCiphertext)
	}
	id, encoded, ok := strings.Cut(strings.TrimPrefix(envelope, credentialCiphertextPrefix), ":")
	if !ok || id == "" || encoded == "" {
		return nil, ErrInvalidCiphertext
	}
	key, ok := cv.keys[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownCredentialKey, id)
	}
	payload, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid base64", ErrInvalidCiphertext)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create GCM: %w", err)
	}
	if len(payload) < gcm.NonceSize()+gcm.Overhead() {
		return nil, ErrInvalidCiphertext
	}
	nonce, ciphertext := payload[:gcm.NonceSize()], payload[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, []byte(credentialCiphertextPrefix+id))
	if err != nil {
		return nil, fmt.Errorf("%w: authentication failed", ErrInvalidCiphertext)
	}
	return plaintext, nil
}

func (cv *CredentialVault) ReencryptCredentials(envelope string) (string, error) {
	plaintext, err := cv.DecryptCredentials(envelope)
	if err != nil {
		return "", err
	}
	return cv.EncryptCredentials(plaintext)
}
