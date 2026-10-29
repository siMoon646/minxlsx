//go:build !windows

package model

import (
	"errors"
	"io/fs"
	"os"
	"strconv"
	"strings"
)

// syncDir flushes a folder's entries (e.g. a rename) to disk.
func syncDir(dir string) error {
	d, err := os.Open(dir) // #nosec G304 -- the folder of the file being saved
	if err != nil {
		return err
	}
	return errors.Join(d.Sync(), d.Close())
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
