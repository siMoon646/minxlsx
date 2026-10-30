//go:build !windows

package model

import (
	"errors"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// syncDir flushes a folder's entries (e.g. a rename) to disk.
func syncDir(dir string) error {
	d, err := os.Open(dir) // #nosec G304 -- the folder of the file being saved
	if err != nil {
		return err
	}
	return errors.Join(d.Sync(), d.Close())
}

// fileWritable reports whether the file may be saved over. A file without
// the owner-write bit is read-only, even for root, who could write it
// anyway. Otherwise the OS decides, which accounts for owner and group.
func fileWritable(path string, info fs.FileInfo) bool {
	const wOK = 0x2 // W_OK
	return info.Mode().Perm()&0o200 != 0 && syscall.Access(path, wOK) == nil
}

// isWriteProtected reports whether err comes from a read-only filesystem,
// like a write-protected USB stick.
func isWriteProtected(err error) bool {
	return errors.Is(err, syscall.EROFS)
}

// newFileMode is what a normal program gives a new file: 0666 minus the
// process umask, as os.Create does.
func newFileMode() fs.FileMode {
	return 0o666 &^ umask()
}

// umask reads the process umask from /proc (Linux 4.7+), without changing
// it. syscall.Umask can only read it by setting it, which races with other
// goroutines creating files. Falls back to the usual 022.
func umask() fs.FileMode {
	const fallback = 0o022
	status, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return fallback
	}
	for line := range strings.Lines(string(status)) {
		if v, ok := strings.CutPrefix(line, "Umask:"); ok {
			m, err := strconv.ParseUint(strings.TrimSpace(v), 8, 32)
			if err != nil {
				return fallback
			}
			return fs.FileMode(m) & fs.ModePerm
		}
	}
	return fallback
}
