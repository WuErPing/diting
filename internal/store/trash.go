package store

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// moveDirToTrash moves a directory into ~/.Trash (or DITING_TRASH_DIR) and
// returns the destination. Rename is tried first; cross-device moves fall back
// to a copy followed by removing the source.
func moveDirToTrash(dir, label string) (string, error) {
	root := os.Getenv("DITING_TRASH_DIR")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("locate home directory: %w", err)
		}
		root = filepath.Join(home, ".Trash")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", fmt.Errorf("create trash directory: %w", err)
	}
	destination := uniqueTrashPath(root, label)
	if err := os.Rename(dir, destination); err == nil {
		return destination, nil
	}
	if err := copyDir(dir, destination); err != nil {
		_ = os.RemoveAll(destination)
		return "", err
	}
	if err := os.RemoveAll(dir); err != nil {
		return destination, fmt.Errorf("remove original audio directory: %w", err)
	}
	return destination, nil
}

func uniqueTrashPath(root, label string) string {
	path := filepath.Join(root, label)
	for i := 2; ; i++ {
		if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
			return path
		}
		path = filepath.Join(root, fmt.Sprintf("%s-%d", label, i))
	}
}

func copyDir(source, destination string) error {
	if err := os.Mkdir(destination, 0o700); err != nil {
		return fmt.Errorf("create trash destination: %w", err)
	}
	entries, err := os.ReadDir(source)
	if err != nil {
		return fmt.Errorf("read source directory: %w", err)
	}
	for _, entry := range entries {
		src := filepath.Join(source, entry.Name())
		dst := filepath.Join(destination, entry.Name())
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.IsDir() {
			if err := copyDir(src, dst); err != nil {
				return err
			}
			continue
		}
		if err := copyFile(src, dst); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(source, destination string) error {
	in, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open source audio: %w", err)
	}
	defer in.Close()

	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create trash audio: %w", err)
	}
	_, copyErr := io.Copy(out, in)
	if copyErr == nil {
		copyErr = out.Sync()
	}
	closeErr := out.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(destination)
		return fmt.Errorf("copy audio to trash: %w", errors.Join(copyErr, closeErr))
	}
	return nil
}
