package projectorchestration

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReleaseArtifactBuilder_PackagesDeclaredOutputs(t *testing.T) {
	contract, err := ParseReleaseContract([]byte(`{"version":1,"builder":"pnpm_build","workdir":".","outputs":["dist/app","dist/assets"]}`))
	if err != nil {
		t.Fatalf("parse v1 release contract: %v", err)
	}
	stage := t.TempDir()
	write := func(name, contents string, mode os.FileMode) {
		t.Helper()
		path := filepath.Join(stage, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), mode); err != nil {
			t.Fatal(err)
		}
	}
	write("dist/app/index.html", "<main>release</main>", 0o600)
	write("dist/assets/app.js", "console.log('release')", 0o755)
	write("unlisted.txt", "must not ship", 0o600)
	if err := os.Chtimes(filepath.Join(stage, "dist/app/index.html"), time.Unix(123, 0), time.Unix(456, 0)); err != nil {
		t.Fatal(err)
	}

	first, count, err := deterministicReleaseTar(stage, contract.Outputs)
	if err != nil {
		t.Fatalf("package declared outputs: %v", err)
	}
	if count != 2 {
		t.Fatalf("packaged file count = %d, want 2", count)
	}
	second, secondCount, err := deterministicReleaseTar(stage, contract.Outputs)
	if err != nil {
		t.Fatalf("repeat package declared outputs: %v", err)
	}
	if secondCount != count || !bytes.Equal(first, second) {
		t.Fatal("repeated release packaging was not deterministic")
	}
	hash := sha256.Sum256(first)
	digest := hex.EncodeToString(hash[:])
	reader := tar.NewReader(bytes.NewReader(first))
	seen := map[string][]byte{}
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read package tar: %v", err)
		}
		contents, err := io.ReadAll(reader)
		if err != nil {
			t.Fatalf("read package entry: %v", err)
		}
		seen[header.Name] = contents
	}
	if string(seen["dist/app/index.html"]) != "<main>release</main>" || string(seen["dist/assets/app.js"]) != "console.log('release')" {
		t.Fatalf("package entries = %#v", seen)
	}
	if _, exists := seen["unlisted.txt"]; exists || len(seen) != count {
		t.Fatalf("package included undeclared output: %#v", seen)
	}
	artifact := ReleaseArtifact{Bytes: first, Digest: digest, Builder: contract.Builder, Outputs: contract.Outputs, FileCount: count}
	store := &memoryReleaseArtifactStore{objects: make(map[string][]byte)}
	key, err := uploadReleaseArtifact(context.Background(), store, artifact)
	if err != nil {
		t.Fatalf("store package by digest: %v", err)
	}
	if !strings.Contains(key, digest) || len(store.objects[key]) != len(first) {
		t.Fatalf("stored artifact key/content = %q/%d bytes", key, len(store.objects[key]))
	}
	if err := VerifyStoredReleaseArtifact(context.Background(), store, key, digest); err != nil {
		t.Fatalf("verify persisted package bytes: %v", err)
	}
}

func TestParseReleaseContractRejectsUnboundedOrUnknownInputs(t *testing.T) {
	invalid := []string{
		`{"version":2,"builder":"go_binary","package":"./cmd/server","outputs":["server"]}`,
		`{"version":1,"builder":"go_binary","package":"./cmd/server","outputs":["../server"]}`,
		`{"version":1,"builder":"go_binary","package":"./cmd/server","outputs":["dist/*"]}`,
		`{"version":1,"builder":"go_binary","package":"./cmd/server","outputs":["dist\\n/server"]}`,
		`{"version":1,"builder":"shell","outputs":["server"]}`,
		`{"version":1,"builder":"pnpm_build","workdir":".","outputs":["dist"],"command":"sh -c"}`,
	}
	for _, raw := range invalid {
		if _, err := ParseReleaseContract([]byte(raw)); err == nil {
			t.Errorf("accepted invalid release contract %s", raw)
		}
	}
}

type memoryReleaseArtifactStore struct {
	objects map[string][]byte
}

func (s *memoryReleaseArtifactStore) Upload(_ context.Context, key string, data []byte, _, _ string) (string, error) {
	s.objects[key] = append([]byte(nil), data...)
	return "https://public.example.invalid/unused", nil
}

func (s *memoryReleaseArtifactStore) GetReader(_ context.Context, key string) (io.ReadCloser, error) {
	data, ok := s.objects[key]
	if !ok {
		return nil, os.ErrNotExist
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (s *memoryReleaseArtifactStore) PresignGet(_ context.Context, key string, _ time.Duration) (string, error) {
	if _, ok := s.objects[key]; !ok {
		return "", os.ErrNotExist
	}
	return "https://private.example.invalid/" + key + "?signature=fresh", nil
}
