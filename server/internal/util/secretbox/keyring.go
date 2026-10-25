package secretbox

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

// VersionMagic is the single-byte prefix indicating a versioned secretbox envelope.
// Wire format for versioned ciphertexts:
// [VersionMagic (1B)][KeyID (1B)][Nonce (12B)][Ciphertext + GCM Tag]
const VersionMagic byte = 0x56 // ASCII 'V'

// MinVersionedLength is the minimum byte size for a valid versioned ciphertext:
// 1 byte magic + 1 byte keyID + 12 bytes nonce + 16 bytes GCM tag = 30 bytes.
const MinVersionedLength = 1 + 1 + 12 + 16

// ErrUnknownKeyID is returned when an envelope specifies a key ID not in the keyring.
var ErrUnknownKeyID = errors.New("secretbox: unknown key id in envelope")

// ErrEmptyKeyRing is returned when constructing a KeyRing with no keys.
var ErrEmptyKeyRing = errors.New("secretbox: keyring must contain at least one key")

// ErrPrimaryNotFound is returned when the primary key ID is not present in the keyring.
var ErrPrimaryNotFound = errors.New("secretbox: primary key id not present in keys")

// KeyRing manages multiple versioned cryptographic keys, supporting zero-downtime
// key rotation and backward compatibility with legacy unversioned ciphertexts.
//
// KeyRing is safe for concurrent use across multiple goroutines.
type KeyRing struct {
	mu          sync.RWMutex
	primaryID   uint8
	boxes       map[uint8]*Box
	fallbackBox *Box
}

// NewKeyRing constructs a KeyRing with the designated primary key ID and key map.
// If legacyFallbackKey is non-empty, unversioned legacy ciphertexts (sealed without
// VersionMagic) will attempt decryption against this fallback key.
func NewKeyRing(primaryID uint8, keys map[uint8][]byte, legacyFallbackKey []byte) (*KeyRing, error) {
	if len(keys) == 0 {
		return nil, ErrEmptyKeyRing
	}
	if _, ok := keys[primaryID]; !ok {
		return nil, ErrPrimaryNotFound
	}

	boxes := make(map[uint8]*Box, len(keys))
	for id, k := range keys {
		box, err := New(k)
		if err != nil {
			return nil, fmt.Errorf("secretbox: key %d invalid: %w", id, err)
		}
		boxes[id] = box
	}

	var fallbackBox *Box
	if len(legacyFallbackKey) > 0 {
		fb, err := New(legacyFallbackKey)
		if err != nil {
			return nil, fmt.Errorf("secretbox: legacy fallback key invalid: %w", err)
		}
		fallbackBox = fb
	} else {
		// Default legacy fallback to the primary key if not explicitly given
		fallbackBox = boxes[primaryID]
	}

	return &KeyRing{
		primaryID:   primaryID,
		boxes:       boxes,
		fallbackBox: fallbackBox,
	}, nil
}

// Seal encrypts plaintext with the active primary key and returns a versioned envelope:
// [VersionMagic (0x56)][primaryID][nonce (12B)][ciphertext + tag].
func (kr *KeyRing) Seal(plaintext []byte) ([]byte, error) {
	kr.mu.RLock()
	primaryID := kr.primaryID
	box := kr.boxes[primaryID]
	kr.mu.RUnlock()

	if box == nil {
		return nil, ErrPrimaryNotFound
	}

	sealed, err := box.Seal(plaintext)
	if err != nil {
		return nil, err
	}

	// Prepend [VersionMagic, primaryID] to the box seal output (which is nonce || ciphertext || tag)
	out := make([]byte, 2+len(sealed))
	out[0] = VersionMagic
	out[1] = primaryID
	copy(out[2:], sealed)
	return out, nil
}

// Open inspects the envelope: if versioned (starts with VersionMagic), it routes decryption
// to the key designated by KeyID. If unversioned, it falls back to the legacy fallback box.
func (kr *KeyRing) Open(sealed []byte) ([]byte, error) {
	if len(sealed) >= MinVersionedLength && sealed[0] == VersionMagic {
		keyID := sealed[1]
		kr.mu.RLock()
		box := kr.boxes[keyID]
		kr.mu.RUnlock()

		if box == nil {
			return nil, fmt.Errorf("%w: %d", ErrUnknownKeyID, keyID)
		}

		// The rest is standard [nonce (12B)][ciphertext + tag]
		return box.Open(sealed[2:])
	}

	// Legacy / Unversioned fallback
	kr.mu.RLock()
	fb := kr.fallbackBox
	kr.mu.RUnlock()

	if fb == nil {
		return nil, ErrCiphertextTooShort
	}
	return fb.Open(sealed)
}

// PrimaryID returns the active primary key ID used for all new Seal operations.
func (kr *KeyRing) PrimaryID() uint8 {
	kr.mu.RLock()
	defer kr.mu.RUnlock()
	return kr.primaryID
}

// RotatePrimary switches the active key ID used for Seal without service interruption.
// Returns an error if the new key ID does not exist in the KeyRing.
func (kr *KeyRing) RotatePrimary(newPrimaryID uint8) error {
	kr.mu.Lock()
	defer kr.mu.Unlock()

	if _, ok := kr.boxes[newPrimaryID]; !ok {
		return fmt.Errorf("%w: %d", ErrPrimaryNotFound, newPrimaryID)
	}
	kr.primaryID = newPrimaryID
	return nil
}

// LoadKeyRing parses a comma-separated list of "id:base64Key" pairs from an env var
// and configures the KeyRing with the specified primary key ID string.
// Example: MULTICA_SECRETBOX_KEYRING="1:b64...,2:b64...", primaryID="2"
func LoadKeyRing(rawKeyRing, rawPrimaryID string) (*KeyRing, error) {
	rawKeyRing = strings.TrimSpace(rawKeyRing)
	if rawKeyRing == "" {
		return nil, errors.New("secretbox: empty keyring string")
	}

	keys := make(map[uint8][]byte)
	pairs := strings.Split(rawKeyRing, ",")
	for _, pair := range pairs {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		parts := strings.SplitN(pair, ":", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("secretbox: invalid keyring entry format: %q", pair)
		}
		idNum, err := strconv.ParseUint(strings.TrimSpace(parts[0]), 10, 8)
		if err != nil {
			return nil, fmt.Errorf("secretbox: invalid key ID %q: %w", parts[0], err)
		}
		keyBytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(parts[1]))
		if err != nil {
			return nil, fmt.Errorf("secretbox: invalid base64 for key %d: %w", idNum, err)
		}
		if len(keyBytes) != KeySize {
			return nil, fmt.Errorf("secretbox: key %d has invalid size %d, expected %d", idNum, len(keyBytes), KeySize)
		}
		keys[uint8(idNum)] = keyBytes
	}

	primaryNum, err := strconv.ParseUint(strings.TrimSpace(rawPrimaryID), 10, 8)
	if err != nil {
		return nil, fmt.Errorf("secretbox: invalid primary key ID %q: %w", rawPrimaryID, err)
	}

	return NewKeyRing(uint8(primaryNum), keys, nil)
}
