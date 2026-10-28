package projectorchestration

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/storage"
)

const (
	releaseContractPath        = "multica.release.json"
	maxReleaseContractBytes    = 64 * 1024
	maxReleaseArtifactBytes    = 512 * 1024 * 1024
	maxReleaseArtifactFiles    = 10000
	releaseArtifactContentType = "application/x-tar"
)

// ReleaseContract is the only target-repository input that selects a release
// builder or its outputs. Output paths are exact paths; globs are unsupported.
type ReleaseContract struct {
	Version int      `json:"version"`
	Builder string   `json:"builder"`
	Package string   `json:"package,omitempty"`
	Workdir string   `json:"workdir,omitempty"`
	Outputs []string `json:"outputs"`
}

type ReleaseArtifact struct {
	Bytes      []byte
	Digest     string
	Builder    string
	Outputs    []string
	FileCount  int
	Size       int64
	StorageKey string
}

// ReleaseArtifactStore is the private object-storage capability required by
// releases. It intentionally excludes public object URL helpers.
type ReleaseArtifactStore interface {
	Upload(ctx context.Context, key string, data []byte, contentType string, filename string) (string, error)
	GetReader(ctx context.Context, key string) (io.ReadCloser, error)
	PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error)
}

type releaseArtifactStore struct {
	store     storage.Storage
	presigner storage.Presigner
}

// NewReleaseArtifactStore exposes release storage only when the configured
// backend supports private, short-lived reads. Local public upload URLs do not.
func NewReleaseArtifactStore(store storage.Storage) ReleaseArtifactStore {
	if store == nil {
		return nil
	}
	presigner, ok := store.(storage.Presigner)
	if !ok {
		return nil
	}
	return releaseArtifactStore{store: store, presigner: presigner}
}

func (s releaseArtifactStore) Upload(ctx context.Context, key string, data []byte, contentType, filename string) (string, error) {
	return s.store.Upload(ctx, key, data, contentType, filename)
}

func (s releaseArtifactStore) GetReader(ctx context.Context, key string) (io.ReadCloser, error) {
	return s.store.GetReader(ctx, key)
}

func (s releaseArtifactStore) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	return s.presigner.PresignGet(ctx, key, ttl)
}

type ReleaseArtifactBuilder interface {
	BuildReleaseArtifact(ctx context.Context, request QualityGateRequest, contract ReleaseContract) (ReleaseArtifact, QualityGateResult, error)
}

// ReadReleaseContract reads the contract from the exact Git commit rather than
// trusting the mutable worktree contents.
func ReadReleaseContract(ctx context.Context, repoPath, commitSHA string) (ReleaseContract, error) {
	if !gitCommitSHA.MatchString(commitSHA) {
		return ReleaseContract{}, errors.New("release contract requires a full candidate commit SHA")
	}
	root, err := filepath.Abs(strings.TrimSpace(repoPath))
	if err != nil || strings.TrimSpace(repoPath) == "" {
		return ReleaseContract{}, errors.New("release contract repository path is invalid")
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return ReleaseContract{}, fmt.Errorf("resolve release contract repository: %w", err)
	}
	command := exec.CommandContext(ctx, "git", "-C", root, "show", commitSHA+":"+releaseContractPath)
	var output boundedQualityOutput
	output.limit = maxReleaseContractBytes
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Run(); err != nil {
		return ReleaseContract{}, fmt.Errorf("read %s from candidate commit: %w", releaseContractPath, err)
	}
	if output.truncated {
		return ReleaseContract{}, fmt.Errorf("%s exceeds %d bytes", releaseContractPath, maxReleaseContractBytes)
	}
	return ParseReleaseContract(output.data)
}

func ParseReleaseContract(data []byte) (ReleaseContract, error) {
	var contract ReleaseContract
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&contract); err != nil {
		return ReleaseContract{}, fmt.Errorf("decode release contract: %w", err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return ReleaseContract{}, errors.New("release contract must contain exactly one JSON object")
	}
	if err := validateReleaseContract(contract); err != nil {
		return ReleaseContract{}, err
	}
	return contract, nil
}

func validateReleaseContract(contract ReleaseContract) error {
	if contract.Version != 1 {
		return fmt.Errorf("unsupported release contract version %d", contract.Version)
	}
	if contract.Builder != "go_binary" && contract.Builder != "pnpm_build" {
		return fmt.Errorf("unsupported release builder %q", contract.Builder)
	}
	if len(contract.Outputs) == 0 || len(contract.Outputs) > 128 {
		return errors.New("release contract must declare between 1 and 128 exact output paths")
	}
	for _, output := range contract.Outputs {
		if err := validateReleasePath(output, false); err != nil {
			return fmt.Errorf("invalid release output path %q: %w", output, err)
		}
	}
	outputs := append([]string(nil), contract.Outputs...)
	sort.Strings(outputs)
	for i := 1; i < len(outputs); i++ {
		if outputs[i] == outputs[i-1] || strings.HasPrefix(outputs[i], outputs[i-1]+"/") {
			return fmt.Errorf("release output paths overlap at %q", outputs[i])
		}
	}
	switch contract.Builder {
	case "go_binary":
		if strings.TrimSpace(contract.Package) != contract.Package || !strings.HasPrefix(contract.Package, "./") {
			return errors.New("go_binary release contract requires a repository-local package path beginning with ./")
		}
		if _, err := cleanContractPath(strings.TrimPrefix(contract.Package, "./")); err != nil {
			return fmt.Errorf("invalid Go package path: %w", err)
		}
		if contract.Workdir != "" {
			return errors.New("go_binary release contract does not accept workdir")
		}
		if len(contract.Outputs) != 1 {
			return errors.New("go_binary release contract must declare exactly one binary output")
		}
	case "pnpm_build":
		if contract.Package != "" {
			return errors.New("pnpm_build release contract does not accept package")
		}
		if contract.Workdir == "" {
			return errors.New("pnpm_build release contract requires workdir")
		}
		if _, err := cleanContractPath(contract.Workdir); err != nil {
			return fmt.Errorf("invalid pnpm workdir: %w", err)
		}
	}
	return nil
}

func validateReleasePath(path string, allowDot bool) error {
	if strings.TrimSpace(path) != path || strings.ContainsAny(path, `\\*?[]{}$;|&<>:, `) || strings.ContainsAny(path, "\r\n\t") {
		return errors.New("path contains unsupported characters")
	}
	cleaned, err := cleanContractPath(path)
	if err != nil {
		return err
	}
	if cleaned == "." && !allowDot {
		return errors.New("path must identify a file or directory")
	}
	return nil
}

func cleanContractPath(path string) (string, error) {
	if path == "" || filepath.IsAbs(path) || strings.Contains(path, `\`) {
		return "", errors.New("path must be relative")
	}
	cleaned := filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") || cleaned != path {
		return "", errors.New("path must be normalized and remain within the repository")
	}
	return cleaned, nil
}

func releaseBuilderCommand(contract ReleaseContract) (daemonQualityCommand, error) {
	if err := validateReleaseContract(contract); err != nil {
		return daemonQualityCommand{}, err
	}
	switch contract.Builder {
	case "go_binary":
		return daemonQualityCommand{
			project: string(projectGo), image: qualityGoImage, name: "go",
			args:    []string{"build", "-trimpath", "-buildvcs=false", "-o", contract.Outputs[0], contract.Package},
			display: "go build -trimpath -buildvcs=false -o " + contract.Outputs[0] + " " + contract.Package,
		}, nil
	case "pnpm_build":
		args := []string{"pnpm", "--dir", contract.Workdir, "run", "build"}
		if contract.Workdir == "." {
			args = []string{"pnpm", "run", "build"}
		}
		return daemonQualityCommand{
			project: string(projectNode), image: qualityNodeImage, name: "corepack", args: args,
			display: "corepack " + strings.Join(args, " "),
		}, nil
	default:
		return daemonQualityCommand{}, fmt.Errorf("unsupported release builder %q", contract.Builder)
	}
}

func (r *DaemonQualityGateRunner) BuildReleaseArtifact(ctx context.Context, request QualityGateRequest, contract ReleaseContract) (ReleaseArtifact, QualityGateResult, error) {
	if err := validateReleaseContract(contract); err != nil {
		return ReleaseArtifact{}, QualityGateResult{}, err
	}
	request.GateType = "build"
	request.ReleaseContract = &contract
	result, err := r.Run(ctx, request)
	if err != nil {
		return ReleaseArtifact{}, result, err
	}
	if !result.Passed || len(result.BuildArtifact) == 0 || !digestSHA256.MatchString(result.BuildArtifactDigest) {
		return ReleaseArtifact{}, result, errors.New("isolated release builder did not produce a verifiable package")
	}
	return ReleaseArtifact{
		Bytes:     result.BuildArtifact,
		Digest:    result.BuildArtifactDigest,
		Builder:   contract.Builder,
		Outputs:   append([]string(nil), contract.Outputs...),
		FileCount: result.BuildArtifactFileCount,
		Size:      int64(len(result.BuildArtifact)),
	}, result, nil
}

func packageReleaseOutputs(stage string, contract ReleaseContract) (ReleaseArtifact, error) {
	packageBytes, count, err := deterministicReleaseTar(stage, contract.Outputs)
	if err != nil {
		return ReleaseArtifact{}, err
	}
	digest := sha256.Sum256(packageBytes)
	return ReleaseArtifact{
		Bytes: packageBytes, Digest: hex.EncodeToString(digest[:]),
		Builder: contract.Builder, Outputs: append([]string(nil), contract.Outputs...),
		FileCount: count, Size: int64(len(packageBytes)),
	}, nil
}

func deterministicReleaseTar(stage string, outputs []string) ([]byte, int, error) {
	files := make([]string, 0)
	for _, output := range outputs {
		if err := validateReleasePath(output, false); err != nil {
			return nil, 0, err
		}
		root := filepath.Join(stage, filepath.FromSlash(output))
		info, err := os.Lstat(root)
		if err != nil {
			return nil, 0, fmt.Errorf("inspect declared release output %q: %w", output, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, 0, fmt.Errorf("declared release output %q is a symlink", output)
		}
		if info.IsDir() {
			err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if path == root {
					return nil
				}
				entryInfo, err := os.Lstat(path)
				if err != nil {
					return err
				}
				if entryInfo.Mode()&os.ModeSymlink != 0 || (!entryInfo.IsDir() && !entryInfo.Mode().IsRegular()) {
					return fmt.Errorf("release output contains unsupported file type at %q", path)
				}
				if entryInfo.Mode().IsRegular() {
					relative, err := filepath.Rel(stage, path)
					if err != nil {
						return err
					}
					files = append(files, filepath.ToSlash(relative))
				}
				if len(files) > maxReleaseArtifactFiles {
					return fmt.Errorf("release output exceeds %d files", maxReleaseArtifactFiles)
				}
				return nil
			})
			if err != nil {
				return nil, 0, fmt.Errorf("walk declared release output %q: %w", output, err)
			}
		} else if info.Mode().IsRegular() {
			files = append(files, filepath.ToSlash(output))
		} else {
			return nil, 0, fmt.Errorf("declared release output %q is not a regular file or directory", output)
		}
	}
	if len(files) == 0 {
		return nil, 0, errors.New("release contract produced no regular output files")
	}
	sort.Strings(files)
	var archive bytes.Buffer
	writer := tar.NewWriter(&archive)
	var uncompressed int64
	for _, name := range files {
		path := filepath.Join(stage, filepath.FromSlash(name))
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			return nil, 0, fmt.Errorf("release output %q changed during packaging", name)
		}
		if info.Size() < 0 || uncompressed+info.Size() > maxReleaseArtifactBytes {
			return nil, 0, fmt.Errorf("release artifact exceeds %d bytes", maxReleaseArtifactBytes)
		}
		mode := int64(0o644)
		if info.Mode().Perm()&0o111 != 0 {
			mode = 0o755
		}
		header := &tar.Header{
			Name: name, Mode: mode, Size: info.Size(), Typeflag: tar.TypeReg,
			ModTime: time.Unix(0, 0).UTC(), Uid: 0, Gid: 0, Format: tar.FormatPAX,
		}
		if err := writer.WriteHeader(header); err != nil {
			return nil, 0, fmt.Errorf("write release artifact header: %w", err)
		}
		file, err := os.Open(path)
		if err != nil {
			return nil, 0, fmt.Errorf("open release output %q: %w", name, err)
		}
		written, copyErr := io.CopyN(writer, file, info.Size())
		closeErr := file.Close()
		if copyErr != nil || written != info.Size() {
			return nil, 0, fmt.Errorf("read release output %q: %w", name, errors.Join(copyErr, closeErr))
		}
		if closeErr != nil {
			return nil, 0, fmt.Errorf("close release output %q: %w", name, closeErr)
		}
		uncompressed += written
	}
	if err := writer.Close(); err != nil {
		return nil, 0, fmt.Errorf("close release artifact archive: %w", err)
	}
	if archive.Len() > maxReleaseArtifactBytes {
		return nil, 0, fmt.Errorf("release artifact exceeds %d bytes", maxReleaseArtifactBytes)
	}
	return archive.Bytes(), len(files), nil
}

func uploadReleaseArtifact(ctx context.Context, store ReleaseArtifactStore, artifact ReleaseArtifact) (string, error) {
	if store == nil {
		return "", errors.New("private release artifact storage is not configured")
	}
	if len(artifact.Bytes) == 0 || !digestSHA256.MatchString(artifact.Digest) {
		return "", errors.New("release artifact is empty or has an invalid digest")
	}
	hash := sha256.Sum256(artifact.Bytes)
	if hex.EncodeToString(hash[:]) != artifact.Digest {
		return "", errors.New("release artifact digest does not match its package bytes")
	}
	key := "autonomous-project/releases/sha256/" + artifact.Digest + ".tar"
	if _, err := store.Upload(ctx, key, artifact.Bytes, releaseArtifactContentType, artifact.Digest+".tar"); err != nil {
		return "", fmt.Errorf("upload release artifact to private storage: %w", err)
	}
	if err := VerifyStoredReleaseArtifact(ctx, store, key, artifact.Digest); err != nil {
		return "", err
	}
	return key, nil
}

func VerifyStoredReleaseArtifact(ctx context.Context, store ReleaseArtifactStore, key, expectedDigest string) error {
	if store == nil || key == "" || !digestSHA256.MatchString(expectedDigest) {
		return errors.New("release artifact storage reference is incomplete")
	}
	reader, err := store.GetReader(ctx, key)
	if err != nil {
		return fmt.Errorf("read persisted release artifact: %w", err)
	}
	defer reader.Close()
	hash := sha256.New()
	count, err := io.Copy(hash, io.LimitReader(reader, maxReleaseArtifactBytes+1))
	if err != nil {
		return fmt.Errorf("verify persisted release artifact: %w", err)
	}
	if count > maxReleaseArtifactBytes || hex.EncodeToString(hash.Sum(nil)) != expectedDigest {
		return errors.New("persisted release artifact does not match its content digest")
	}
	return nil
}
