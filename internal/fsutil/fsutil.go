package fsutil

import (
	"os"
	"path/filepath"
)

func EnsurePrivateDir(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	return os.Chmod(path, 0o700)
}

func EnsurePrivateFile(path string) error {
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	return os.Chmod(path, 0o600)
}

func DirOf(path string) string {
	return filepath.Dir(path)
}
