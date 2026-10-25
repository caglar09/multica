// Package selfexec resolves the executable backing the current process.
package selfexec

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Resolve prefers the OS-reported executable path. Some launch environments
// can omit that metadata, so it falls back to argv[0] using normal executable
// lookup semantics instead of treating a bare command name as relative to the
// current directory.
func Resolve() (string, error) {
	return resolveWith(os.Executable, os.Args)
}

func resolveWith(osExecutable func() (string, error), args []string) (string, error) {
	exePath, err := osExecutable()
	if err == nil {
		info, statErr := os.Stat(exePath)
		if statErr == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
			return exePath, nil
		}
		if statErr == nil {
			statErr = fmt.Errorf("%s is not an executable regular file", exePath)
		}
		err = statErr
	}
	osExecutableErr := fmt.Errorf("os.Executable: %w", err)

	if len(args) == 0 || args[0] == "" {
		return "", errors.Join(osExecutableErr, errors.New("argv[0] is empty"))
	}

	resolveCandidate := func(argv0 string) (string, error) {
		candidate, candidateErr := exec.LookPath(argv0)
		if candidateErr == nil {
			candidate, candidateErr = filepath.Abs(candidate)
		}
		if candidateErr == nil {
			var info os.FileInfo
			info, candidateErr = os.Stat(candidate)
			if candidateErr == nil && (!info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0) {
				candidateErr = fmt.Errorf("%s is not an executable regular file", candidate)
			}
		}
		return candidate, candidateErr
	}

	candidate, fallbackErr := resolveCandidate(args[0])
	if fallbackErr != nil && filepath.Base(args[0]) != args[0] {
		candidate, fallbackErr = resolveCandidate(filepath.Base(args[0]))
	}
	if fallbackErr != nil {
		return "", errors.Join(
			osExecutableErr,
			fmt.Errorf("resolve argv[0] %q: %w", args[0], fallbackErr),
		)
	}

	return candidate, nil
}
