//go:build windows

package model

import "io/fs"

// syncDir does nothing on Windows: folders can't be opened for syncing, and
// NTFS records the rename in its journal.
func syncDir(string) error { return nil }

// newFileMode is what a normal new file gets. Windows only uses the owner
// write bit (the read-only flag).
func newFileMode() fs.FileMode { return 0o666 }
