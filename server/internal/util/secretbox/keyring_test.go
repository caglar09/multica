package secretbox

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"testing"
)

func generateKey(t *testing.T) []byte {
	t.Helper()
	k := make([]byte, KeySize)
	if _, err := rand.Read(k); err != nil {
		t.Fatalf("rand.Read failed: %v", err)
	}
	return k
}

func TestKeyRing_RoundTrip(t *testing.T) {
	key1 := generateKey(t)
	key2 := generateKey(t)

	kr, err := NewKeyRing(1, map[uint8][]byte{
		1: key1,
		2: key2,
	}, nil)
	if err != nil {
		t.Fatalf("NewKeyRing failed: %v", err)
	}

	plaintext := []byte("secret deployment token: vercel_tok_123456789")
	sealed, err := kr.Seal(plaintext)
	if err != nil {
		t.Fatalf("kr.Seal failed: %v", err)
	}

	// Verify header bytes: magic byte + primary key ID (1)
	if len(sealed) < MinVersionedLength {
		t.Fatalf("sealed length %d too short", len(sealed))
	}
	if sealed[0] != VersionMagic {
		t.Fatalf("expected magic byte 0x%x, got 0x%x", VersionMagic, sealed[0])
	}
	if sealed[1] != 1 {
		t.Fatalf("expected key ID 1, got %d", sealed[1])
	}

	opened, err := kr.Open(sealed)
	if err != nil {
		t.Fatalf("kr.Open failed: %v", err)
	}
	if !bytes.Equal(opened, plaintext) {
		t.Fatalf("opened plaintext mismatch: got %q, want %q", opened, plaintext)
	}
}

func TestKeyRing_Rotation(t *testing.T) {
	key1 := generateKey(t)
	key2 := generateKey(t)

	kr, err := NewKeyRing(1, map[uint8][]byte{
		1: key1,
		2: key2,
	}, nil)
	if err != nil {
		t.Fatalf("NewKeyRing failed: %v", err)
	}

	msg1 := []byte("encrypted with key 1")
	sealed1, err := kr.Seal(msg1)
	if err != nil {
		t.Fatalf("Seal with key 1 failed: %v", err)
	}

	// Rotate primary to key 2
	if err := kr.RotatePrimary(2); err != nil {
		t.Fatalf("RotatePrimary failed: %v", err)
	}
	if kr.PrimaryID() != 2 {
		t.Fatalf("expected primary ID 2, got %d", kr.PrimaryID())
	}

	msg2 := []byte("encrypted with key 2")
	sealed2, err := kr.Seal(msg2)
	if err != nil {
		t.Fatalf("Seal with key 2 failed: %v", err)
	}
	if sealed2[1] != 2 {
		t.Fatalf("expected key ID 2 on sealed2, got %d", sealed2[1])
	}

	// KeyRing should be able to decrypt both sealed1 (key 1) and sealed2 (key 2)
	opened1, err := kr.Open(sealed1)
	if err != nil {
		t.Fatalf("Open sealed1 failed: %v", err)
	}
	if !bytes.Equal(opened1, msg1) {
		t.Fatalf("opened1 mismatch: got %q, want %q", opened1, msg1)
	}

	opened2, err := kr.Open(sealed2)
	if err != nil {
		t.Fatalf("Open sealed2 failed: %v", err)
	}
	if !bytes.Equal(opened2, msg2) {
		t.Fatalf("opened2 mismatch: got %q, want %q", opened2, msg2)
	}
}

func TestKeyRing_LegacyFallback(t *testing.T) {
	legacyKey := generateKey(t)
	key1 := generateKey(t)

	// Create legacy unversioned box directly
	legacyBox, err := New(legacyKey)
	if err != nil {
		t.Fatalf("New legacy box failed: %v", err)
	}
	legacyPlaintext := []byte("historical lark secret encrypted unversioned")
	legacySealed, err := legacyBox.Seal(legacyPlaintext)
	if err != nil {
		t.Fatalf("legacyBox.Seal failed: %v", err)
	}

	// Setup keyring with key1 as primary and legacyKey as fallback
	kr, err := NewKeyRing(1, map[uint8][]byte{1: key1}, legacyKey)
	if err != nil {
		t.Fatalf("NewKeyRing failed: %v", err)
	}

	// KeyRing should decrypt the unversioned legacy ciphertext via fallback
	opened, err := kr.Open(legacySealed)
	if err != nil {
		t.Fatalf("kr.Open legacy ciphertext failed: %v", err)
	}
	if !bytes.Equal(opened, legacyPlaintext) {
		t.Fatalf("legacy opened mismatch: got %q, want %q", opened, legacyPlaintext)
	}
}

func TestKeyRing_UnknownKeyID(t *testing.T) {
	key1 := generateKey(t)
	kr, err := NewKeyRing(1, map[uint8][]byte{1: key1}, nil)
	if err != nil {
		t.Fatalf("NewKeyRing failed: %v", err)
	}

	// Fabricate a versioned payload claiming key ID 99
	fabricated := make([]byte, MinVersionedLength)
	fabricated[0] = VersionMagic
	fabricated[1] = 99

	_, err = kr.Open(fabricated)
	if !errors.Is(err, ErrUnknownKeyID) {
		t.Fatalf("expected ErrUnknownKeyID, got %v", err)
	}
}

func TestKeyRing_Tampered(t *testing.T) {
	key1 := generateKey(t)
	kr, err := NewKeyRing(1, map[uint8][]byte{1: key1}, nil)
	if err != nil {
		t.Fatalf("NewKeyRing failed: %v", err)
	}

	sealed, err := kr.Seal([]byte("tamper test"))
	if err != nil {
		t.Fatalf("Seal failed: %v", err)
	}

	// Tamper a byte in the ciphertext portion
	tampered := append([]byte(nil), sealed...)
	tampered[len(tampered)-1] ^= 0x01

	if _, err := kr.Open(tampered); err == nil {
		t.Fatal("expected decryption error on tampered ciphertext, got nil")
	}
}

func TestKeyRing_LoadKeyRing(t *testing.T) {
	k1 := generateKey(t)
	k2 := generateKey(t)
	b64_1 := base64.StdEncoding.EncodeToString(k1)
	b64_2 := base64.StdEncoding.EncodeToString(k2)

	rawRing := "1:" + b64_1 + ", 2:" + b64_2
	kr, err := LoadKeyRing(rawRing, "2")
	if err != nil {
		t.Fatalf("LoadKeyRing failed: %v", err)
	}

	if kr.PrimaryID() != 2 {
		t.Fatalf("expected primary ID 2, got %d", kr.PrimaryID())
	}

	plaintext := []byte("testing env loader")
	sealed, err := kr.Seal(plaintext)
	if err != nil {
		t.Fatalf("Seal failed: %v", err)
	}
	if sealed[1] != 2 {
		t.Fatalf("expected sealed key ID 2, got %d", sealed[1])
	}

	opened, err := kr.Open(sealed)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	if !bytes.Equal(opened, plaintext) {
		t.Fatalf("mismatch after env load: got %q, want %q", opened, plaintext)
	}
}

// TestSecretBox_KeyRing satisfies ADR-0002 Section 7:
// Validates multi-key rotation, sealing with primary key, opening with older keys, and rejection of unregistered keys.
func TestSecretBox_KeyRing(t *testing.T) {
	TestKeyRing_Rotation(t)
	TestKeyRing_UnknownKeyID(t)
}

// TestSecretBox_LegacyFallback satisfies ADR-0002 Section 7:
// Proves unversioned ciphertexts continue to decrypt seamlessly alongside versioned ciphertexts.
func TestSecretBox_LegacyFallback(t *testing.T) {
	TestKeyRing_LegacyFallback(t)
}

// TestBox_SealVersioned verifies that secretbox.Box can seal and open versioned payloads.
func TestBox_SealVersioned(t *testing.T) {
	key := generateKey(t)
	box, err := New(key)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	plaintext := []byte("deployment token with key version metadata")
	sealed, err := box.SealVersioned(1, plaintext)
	if err != nil {
		t.Fatalf("SealVersioned failed: %v", err)
	}

	if len(sealed) < MinVersionedLength {
		t.Fatalf("sealed length %d too short", len(sealed))
	}
	if sealed[0] != VersionMagic {
		t.Fatalf("expected magic byte 0x%x, got 0x%x", VersionMagic, sealed[0])
	}
	if sealed[1] != 1 {
		t.Fatalf("expected key ID 1, got %d", sealed[1])
	}

	opened, err := box.Open(sealed)
	if err != nil {
		t.Fatalf("box.Open failed on versioned ciphertext: %v", err)
	}
	if !bytes.Equal(opened, plaintext) {
		t.Fatalf("opened plaintext mismatch: got %q, want %q", opened, plaintext)
	}

	// Legacy unversioned must also open with the same box
	legacySealed, err := box.Seal(plaintext)
	if err != nil {
		t.Fatalf("box.Seal failed: %v", err)
	}
	legacyOpened, err := box.Open(legacySealed)
	if err != nil {
		t.Fatalf("box.Open failed on legacy ciphertext: %v", err)
	}
	if !bytes.Equal(legacyOpened, plaintext) {
		t.Fatalf("legacy opened plaintext mismatch: got %q, want %q", legacyOpened, plaintext)
	}
}

