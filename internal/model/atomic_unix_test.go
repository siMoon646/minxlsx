//go:build !windows

package model

import (
	"errors"
	"io/fs"
	"syscall"
	"testing"
)

// TestReadOnlyFilesystem: a write-protected disk (EROFS) is reported as
// read-only, so the UI offers Save As.
func TestReadOnlyFilesystem(t *testing.T) {
	err := readOnlyIfDenied(&fs.PathError{Op: "open", Path: "/media/usb/x", Err: syscall.EROFS})
	if !errors.Is(err, ErrReadOnly) {
		t.Errorf("err = %v, want ErrReadOnly", err)
	}
	if err := readOnlyIfDenied(syscall.ENOSPC); errors.Is(err, ErrReadOnly) {
		t.Errorf("a full disk is not read-only: %v", err)
	}
}
