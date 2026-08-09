package tokenprotect

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

func TestAESGCMRoundTripBindsAdditionalData(t *testing.T) {
	protector := AESGCM{KeyID: "delivery-key-1", Key: bytes.Repeat([]byte{7}, 32)}
	protected, err := protector.Protect(context.Background(), []byte("lease-token-secret"), []byte("command-1"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(protected.Ciphertext, "lease-token-secret") {
		t.Fatal("protected value contains plaintext token")
	}
	plaintext, err := protector.Unprotect(context.Background(), protected, []byte("command-1"))
	if err != nil || string(plaintext) != "lease-token-secret" {
		t.Fatalf("unprotected = %q, error = %v", plaintext, err)
	}
	if _, err := protector.Unprotect(context.Background(), protected, []byte("command-2")); err == nil {
		t.Fatal("protected token accepted for different Command")
	}
	wrongRevision := AESGCM{KeyID: "delivery-key-2", Key: bytes.Repeat([]byte{8}, 32)}
	if _, err := wrongRevision.Unprotect(context.Background(), protected, []byte("command-1")); !errors.Is(err, ErrKeyUnavailable) {
		t.Fatalf("wrong key revision error = %v", err)
	}
}
