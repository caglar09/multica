package deployment

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/google/uuid"
)

// DeriveIdempotencyKey creates a stable key for one deployment intent. NUL
// separators keep concatenated fields unambiguous without another encoding.
func DeriveIdempotencyKey(targetID uuid.UUID, sourceRevision, environment, operationKind, approvalReference string) string {
	h := sha256.Sum256([]byte(strings.Join([]string{
		targetID.String(), sourceRevision, environment, operationKind, approvalReference,
	}, "\x00")))
	return hex.EncodeToString(h[:])
}
