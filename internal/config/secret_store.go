package config

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const encryptedSecretPrefix = "dfmc:v1:"

func MasterKeyPath() string {
	return filepath.Join(UserConfigDir(), "secrets", "master.key")
}

func EncryptSecret(plain string) (string, error) {
	plain = strings.TrimSpace(plain)
	if plain == "" {
		return "", nil
	}
	key, err := loadOrCreateMasterKey()
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ciphertext := gcm.Seal(nil, nonce, []byte(plain), nil)
	return encryptedSecretPrefix +
		base64.RawURLEncoding.EncodeToString(nonce) + ":" +
		base64.RawURLEncoding.EncodeToString(ciphertext), nil
}

func DecryptSecret(encoded string) (string, error) {
	encoded = strings.TrimSpace(encoded)
	if encoded == "" {
		return "", nil
	}
	if !strings.HasPrefix(encoded, encryptedSecretPrefix) {
		return "", fmt.Errorf("unsupported encrypted secret format")
	}
	parts := strings.Split(strings.TrimPrefix(encoded, encryptedSecretPrefix), ":")
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid encrypted secret payload")
	}
	nonce, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", fmt.Errorf("decode nonce: %w", err)
	}
	ciphertext, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("decode ciphertext: %w", err)
	}
	key, err := loadOrCreateMasterKey()
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(nonce) != gcm.NonceSize() {
		return "", fmt.Errorf("invalid nonce size")
	}
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt secret: %w", err)
	}
	return string(plain), nil
}

func (c *Config) applyEncryptedProviderKeys() {
	if c == nil || c.Providers.Profiles == nil {
		return
	}
	for name, prof := range c.Providers.Profiles {
		if strings.TrimSpace(prof.APIKey) != "" || strings.TrimSpace(prof.APIKeyEncrypted) == "" {
			continue
		}
		if plain, err := DecryptSecret(prof.APIKeyEncrypted); err == nil && strings.TrimSpace(plain) != "" {
			prof.APIKey = plain
			c.Providers.Profiles[name] = prof
		}
	}
}

func loadOrCreateMasterKey() ([]byte, error) {
	path := MasterKeyPath()
	data, err := os.ReadFile(path)
	if err == nil {
		key, decodeErr := base64.RawURLEncoding.DecodeString(strings.TrimSpace(string(data)))
		if decodeErr != nil {
			return nil, fmt.Errorf("decode master key: %w", decodeErr)
		}
		if len(key) != 32 {
			return nil, fmt.Errorf("master key has %d bytes, want 32", len(key))
		}
		return key, nil
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read master key: %w", err)
	}
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create master key dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(base64.RawURLEncoding.EncodeToString(key)), 0o600); err != nil {
		return nil, fmt.Errorf("write master key: %w", err)
	}
	return key, nil
}
