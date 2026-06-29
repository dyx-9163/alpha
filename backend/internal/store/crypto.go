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

func (s *Store) encryptSecret(value string) (string, error) {
	if strings.TrimSpace(value) == "" || strings.HasPrefix(value, encryptedSecretPrefix) {
		return value, nil
	}
	block, err := aes.NewCipher(s.secretKey)
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
	raw := strings.TrimPrefix(value, encryptedSecretPrefix)
	payload, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(s.secretKey)
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
