//go:build windows

package model

import (
	"errors"
	"io/fs"
	"testing"
)

// TestReadOnlyFilesystem: write-protected media (ERROR_WRITE_PROTECT) is
// reported as read-only, so the UI offers Save As.
func TestReadOnlyFilesystem(t *testing.T) {
	err := readOnlyIfDenied(&fs.PathError{Op: "open", Path: `E:\x`, Err: errorWriteProtect})
	if !errors.Is(err, ErrReadOnly) {
		t.Errorf("err = %v, want ErrReadOnly", err)
	}
}
