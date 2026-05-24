package auth

import (
	"encoding/base64"
	"testing"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	plaintext := "my-secret-token"

	encrypted, err := EncryptToken(plaintext, key)
	if err != nil {
		t.Fatalf("encryption failed: %v", err)
	}

	decrypted, err := DecryptToken(encrypted, key)
	if err != nil {
		t.Fatalf("decryption failed: %v", err)
	}

	if decrypted != plaintext {
		t.Errorf("expected %q, got %q", plaintext, decrypted)
	}
}

func TestKeySizeBoundary(t *testing.T) {
	invalidKey := []byte("too-short")
	plaintext := "token"

	_, err := EncryptToken(plaintext, invalidKey)
	if err == nil {
		t.Error("expected error for invalid key size, got nil")
	}

	_, err = DecryptToken("c29tZWNpcGhlcg==", invalidKey)
	if err == nil {
		t.Error("expected error for invalid key size, got nil")
	}
}

func TestCryptographicTampering(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	plaintext := "secure-token"

	encrypted, err := EncryptToken(plaintext, key)
	if err != nil {
		t.Fatalf("encryption failed: %v", err)
	}

	data, err := base64.StdEncoding.DecodeString(encrypted)
	if err != nil {
		t.Fatalf("base64 decode failed: %v", err)
	}

	if len(data) > 20 {
		data[20] ^= 0xff
	}

	tampered := base64.StdEncoding.EncodeToString(data)
	_, err = DecryptToken(tampered, key)
	if err == nil {
		t.Error("expected decryption failure for tampered ciphertext, got nil")
	}
}

func TestEmptyDecrypt(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")

	_, err := DecryptToken("", key)
	if err == nil {
		t.Error("expected decryption failure for empty ciphertext, got nil")
	}
}
