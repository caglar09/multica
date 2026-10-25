package deployment

import (
	"context"
	"errors"
	"testing"
)

func TestCreateOperationRequiresIdempotencyKey(t *testing.T) {
	t.Parallel()

	repo := NewPostgresRepository(nil)
	_, _, err := repo.CreateOperation(context.Background(), CreateOperationInput{})
	if !errors.Is(err, ErrIdempotencyKeyRequired) {
		t.Fatalf("CreateOperation() error = %v, want %v", err, ErrIdempotencyKeyRequired)
	}
}

func TestAppendEventRequiresDedupeKey(t *testing.T) {
	t.Parallel()

	repo := NewPostgresRepository(nil)
	_, _, err := repo.AppendEvent(context.Background(), AppendEventInput{})
	if !errors.Is(err, ErrEventDedupeKeyRequired) {
		t.Fatalf("AppendEvent() error = %v, want %v", err, ErrEventDedupeKeyRequired)
	}
}
