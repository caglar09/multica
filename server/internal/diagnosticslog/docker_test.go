package diagnosticslog

import (
	"encoding/binary"
	"testing"
)

func TestParseDockerLogBodyMultiplexed(t *testing.T) {
	payload := []byte("2026-09-03T12:34:56.123456789Z level=error workflow stalled\n")
	frame := make([]byte, 8+len(payload))
	frame[0] = 2
	binary.BigEndian.PutUint32(frame[4:8], uint32(len(payload)))
	copy(frame[8:], payload)

	entries := parseDockerLogBody(frame, "backend", "multica-backend-1", "")
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	entry := entries[0]
	if entry.Source != "backend" || entry.Stream != "stderr" || entry.Level != "error" {
		t.Fatalf("unexpected entry: %#v", entry)
	}
	if entry.Timestamp != "2026-09-03T12:34:56.123456789Z" {
		t.Fatalf("timestamp = %q", entry.Timestamp)
	}
	if entry.Message != "level=error workflow stalled" {
		t.Fatalf("message = %q", entry.Message)
	}
}

func TestParseLinesFiltersSearchCaseInsensitively(t *testing.T) {
	body := []byte("2026-09-03T12:00:00Z worker ready\n2026-09-03T12:00:01Z BLOCKED workflow\n")
	entries := parseLines(body, "worker", "multica-worker-1", "stdout", "blocked")
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	if entries[0].Message != "BLOCKED workflow" {
		t.Fatalf("message = %q", entries[0].Message)
	}
}
