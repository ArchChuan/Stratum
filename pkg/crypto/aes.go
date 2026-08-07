package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

// DeriveAESKey derives a 32-byte AES-256 key from a PEM string via SHA-256.
func DeriveAESKey(jwtPrivateKeyPEM string) [32]byte {
	return sha256.Sum256([]byte(jwtPrivateKeyPEM))
}

// ResolveDataKey resolves the at-rest data-encryption key material.
//
// configured（DATA_ENCRYPTION_KEY）优先，是独立于 JWT 签名密钥的密钥材料；
// 为空时回退 fallback（JWT 私钥），保证存量部署的密文仍然可解（旧实现以
// sha256(JWT 私钥) 派生）；两者皆空时返回错误 fail closed——禁止以
// SHA-256("") 落公开常量密钥，否则拿到 DB 备份即可解密全部密文。
// 派生方式与 DeriveAESKey 一致，密文格式兼容。
func ResolveDataKey(configured, fallback string) ([32]byte, error) {
	if configured != "" {
		return DeriveAESKey(configured), nil
	}
	if fallback != "" {
		return DeriveAESKey(fallback), nil
	}
	return [32]byte{}, errors.New("crypto: data encryption key required")
}

// Encrypt encrypts plaintext with AES-256-GCM. Returns base64(nonce || ciphertext || tag).
func Encrypt(key [32]byte, plaintext string) (string, error) {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", fmt.Errorf("crypto: new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("crypto: new gcm: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("crypto: nonce: %w", err)
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt decrypts a base64-encoded AES-256-GCM ciphertext produced by Encrypt.
func Decrypt(key [32]byte, encoded string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("crypto: base64 decode: %w", err)
	}
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", fmt.Errorf("crypto: new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("crypto: new gcm: %w", err)
	}
	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return "", fmt.Errorf("crypto: ciphertext too short")
	}
	plaintext, err := gcm.Open(nil, data[:nonceSize], data[nonceSize:], nil)
	if err != nil {
		return "", fmt.Errorf("crypto: decrypt: %w", err)
	}
	return string(plaintext), nil
}
