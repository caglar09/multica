package deployment

import (
	"testing"

	"github.com/google/uuid"
)

func TestDeriveIdempotencyKey(t *testing.T) {
	t.Parallel()
	target := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	a := DeriveIdempotencyKey(target, "abc", "preview", "deploy", "")
	if a == "" || a != DeriveIdempotencyKey(target, "abc", "preview", "deploy", "") {
		t.Fatal("idempotency key must be deterministic")
	}
	if a == DeriveIdempotencyKey(target, "def", "preview", "deploy", "") {
		t.Fatal("source revisions must produce different keys")
	}
}
