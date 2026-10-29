package model

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// writeFileAtomic writes a file so that a crash, a full disk or a pulled USB
// stick never leaves it half-written. write gets a temp file in the same
// folder; the original is only replaced once that file is complete:
//
//  1. write the temp file
//  2. Sync it to disk
//  3. Close it, checking the error (some filesystems report write errors
//     only here)
//  4. give it the original's permissions (or normal new-file ones)
//  5. rename it over the original (atomic within one folder)
//  6. sync the folder, so the rename itself survives a power cut (Linux)
//
// If any step up to the rename fails, the temp file is removed and the
// original is untouched.
func writeFileAtomic(path string, write func(w io.Writer) error) (err error) {
	mode, err := targetMode(path)
	if err != nil {
		return fmt.Errorf("save %s: %w", path, err)
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("save %s: %w", path, err)
	}
	tmpName := tmp.Name()
	closed, renamed := false, false
	defer func() {
		if err == nil || renamed {
			return
		}
		if !closed {
			err = errors.Join(err, tmp.Close())
		}
		err = errors.Join(err, removeTemp(tmpName))
	}()

	if err := write(tmp); err != nil {
		return fmt.Errorf("save %s: %w", path, err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("save %s: sync: %w", path, err)
	}
	closed = true
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("save %s: close: %w", path, err)
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return fmt.Errorf("save %s: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("save %s: %w", path, err)
	}
	renamed = true
	// The new file is in place now. A failed folder sync only means the
	// rename might not survive a power cut yet, so report it but keep going.
	if err := syncDir(dir); err != nil {
		return fmt.Errorf("saved %s, but syncing its folder failed: %w", path, err)
	}
	return nil
}

// targetMode returns the permissions the saved file should get: the
// original's if it exists, otherwise what a normal new file gets.
// (excelize's own SaveAs would make new files executable: 0777 minus umask.)
func targetMode(path string) (fs.FileMode, error) {
	info, err := os.Stat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return newFileMode(), nil
	case err != nil:
		return 0, err
	case !info.Mode().IsRegular():
		return 0, errors.New("not a regular file")
	}
	return info.Mode().Perm(), nil
}

// removeTemp deletes a leftover temp file. On Windows a read-only file
// can't be deleted, so it is made writable first if needed.
func removeTemp(name string) error {
	err := os.Remove(name)
	if err == nil || errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if os.Chmod(name, 0o600) == nil && os.Remove(name) == nil {
		return nil
	}
	return fmt.Errorf("remove temp file: %w", err)
}
