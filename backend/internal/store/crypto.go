package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
)

const encryptedSecretPrefix = "enc:v1:"

func deriveSecretKey(secret string) []byte {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		secret = "aifar-local-store-secret-change-me"
	}
	sum := sha256.Sum256([]byte(secret))
	return sum[:]
}

func deriveOptionalSecretKey(secret string) []byte {
	if secret == "" {
		return nil
	}
	return deriveSecretKey(secret)
}

func (s *Store) encryptSecret(value string) (string, error) {
	if strings.TrimSpace(value) == "" || strings.HasPrefix(value, encryptedSecretPrefix) {
		return value, nil
	}
	current := s.currentSecretKey()
	defer zeroSecretKey(current)
	return encryptPlaintextSecret(value, current)
}

func encryptPlaintextSecret(value string, key []byte) (string, error) {
	if strings.TrimSpace(value) == "" {
		return value, nil
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
	sealed := gcm.Seal(nonce, nonce, []byte(value), nil)
	return encryptedSecretPrefix + base64.RawURLEncoding.EncodeToString(sealed), nil
}

func (s *Store) decryptSecret(value string) (string, error) {
	if strings.TrimSpace(value) == "" || !strings.HasPrefix(value, encryptedSecretPrefix) {
		return value, nil
	}
	current, previous := s.secretKeys()
	defer zeroSecretKey(current)
	defer zeroSecretKey(previous)
	return decryptSecretWithKeys(value, current, previous)
}

func decryptSecretWithKeys(value string, current, previous []byte) (string, error) {
	if strings.TrimSpace(value) == "" || !strings.HasPrefix(value, encryptedSecretPrefix) {
		return value, nil
	}
	plain, currentErr := decryptEncryptedSecret(value, current)
	if currentErr == nil {
		return plain, nil
	}
	if len(previous) == 0 {
		return "", fmt.Errorf("decrypt secret with current credential key: %w", currentErr)
	}
	plain, previousErr := decryptEncryptedSecret(value, previous)
	if previousErr == nil {
		return plain, nil
	}
	return "", fmt.Errorf("decrypt secret with current or previous credential key: current: %v; previous: %v", currentErr, previousErr)
}

func decryptEncryptedSecret(value string, key []byte) (string, error) {
	raw := strings.TrimPrefix(value, encryptedSecretPrefix)
	payload, err := base64.RawURLEncoding.DecodeString(raw)
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
	if len(payload) < gcm.NonceSize() {
		return "", fmt.Errorf("encrypted secret payload is too short")
	}
	nonce := payload[:gcm.NonceSize()]
	ciphertext := payload[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func (s *Store) secretKeys() ([]byte, []byte) {
	s.secretKeyMu.RLock()
	defer s.secretKeyMu.RUnlock()
	return append([]byte(nil), s.secretKey...), append([]byte(nil), s.previousSecretKey...)
}

func (s *Store) currentSecretKey() []byte {
	s.secretKeyMu.RLock()
	defer s.secretKeyMu.RUnlock()
	return append([]byte(nil), s.secretKey...)
}

func (s *Store) clearPreviousSecretKey() {
	s.secretKeyMu.Lock()
	defer s.secretKeyMu.Unlock()
	zeroSecretKey(s.previousSecretKey)
	s.previousSecretKey = nil
}

func (s *Store) clearSecretKeys() {
	s.secretKeyMu.Lock()
	defer s.secretKeyMu.Unlock()
	zeroSecretKey(s.secretKey)
	zeroSecretKey(s.previousSecretKey)
	s.secretKey = nil
	s.previousSecretKey = nil
}
