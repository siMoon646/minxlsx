//go:build windows

package model

import (
	"errors"
	"io/fs"
	"syscall"
)

// syncDir does nothing on Windows: folders can't be opened for syncing, and
// NTFS records the rename in its journal.
func syncDir(string) error { return nil }

// newFileMode is what a normal new file gets. Windows only uses the owner
// write bit (the read-only flag).
func newFileMode() fs.FileMode { return 0o666 }

// fileWritable checks the read-only flag, which Go reports as a missing
// owner-write bit. Other access rules (ACLs) show up as a permission error
// when the save runs.
func fileWritable(_ string, info fs.FileInfo) bool {
	return info.Mode().Perm()&0o200 != 0
}

// errorWriteProtect is ERROR_WRITE_PROTECT: the media is write-protected.
const errorWriteProtect = syscall.Errno(19)

// isWriteProtected reports whether err comes from write-protected media,
// like a USB stick with its lock switch on.
func isWriteProtected(err error) bool {
	return errors.Is(err, errorWriteProtect)
}
