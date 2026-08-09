// Package tokenprotect protects short-lived capability material for durable
// delivery without persisting plaintext bearer tokens.
package tokenprotect

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
)

type ProtectedValue struct {
	KeyID      string `json:"key_id"`
	Algorithm  string `json:"algorithm"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

type Protector interface {
	Protect(context.Context, []byte, []byte) (ProtectedValue, error)
	Unprotect(context.Context, ProtectedValue, []byte) ([]byte, error)
}

type AESGCM struct {
	KeyID string
	Key   []byte
}

func (protector AESGCM) aead() (cipher.AEAD, error) {
	if protector.KeyID == "" || len(protector.Key) != 32 {
		return nil, errors.New("AES-256-GCM key ID and 32-byte key are required")
	}
	block, err := aes.NewCipher(protector.Key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func (protector AESGCM) Protect(ctx context.Context, plaintext, additionalData []byte) (ProtectedValue, error) {
	if err := context.Cause(ctx); err != nil {
		return ProtectedValue{}, err
	}
	if len(plaintext) == 0 || len(additionalData) == 0 {
		return ProtectedValue{}, errors.New("plaintext capability and additional data are required")
	}
	aead, err := protector.aead()
	if err != nil {
		return ProtectedValue{}, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return ProtectedValue{}, err
	}
	ciphertext := aead.Seal(nil, nonce, plaintext, additionalData)
	return ProtectedValue{KeyID: protector.KeyID, Algorithm: "AES-256-GCM", Nonce: base64.RawURLEncoding.EncodeToString(nonce), Ciphertext: base64.RawURLEncoding.EncodeToString(ciphertext)}, nil
}

func (protector AESGCM) Unprotect(ctx context.Context, protected ProtectedValue, additionalData []byte) ([]byte, error) {
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	if protected.KeyID != protector.KeyID || protected.Algorithm != "AES-256-GCM" || len(additionalData) == 0 {
		return nil, errors.New("protected capability key/algorithm/additional-data mismatch")
	}
	aead, err := protector.aead()
	if err != nil {
		return nil, err
	}
	nonce, err := base64.RawURLEncoding.DecodeString(protected.Nonce)
	if err != nil || len(nonce) != aead.NonceSize() {
		return nil, errors.New("invalid protected capability nonce")
	}
	ciphertext, err := base64.RawURLEncoding.DecodeString(protected.Ciphertext)
	if err != nil {
		return nil, errors.New("invalid protected capability ciphertext")
	}
	plaintext, err := aead.Open(nil, nonce, ciphertext, additionalData)
	if err != nil {
		return nil, errors.New("protected capability authentication failed")
	}
	return plaintext, nil
}
