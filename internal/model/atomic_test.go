package model

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

var errWriteFailed = errors.New("disk full (simulated)")

// writeString returns a write callback that writes s.
func writeString(s string) func(io.Writer) error {
	return func(w io.Writer) error {
		_, err := io.WriteString(w, s)
		return err
	}
}

// failAfter returns a write callback that writes part of a file and then
// fails, like a full disk or a pulled USB stick.
func failAfter(partial string) func(io.Writer) error {
	return func(w io.Writer) error {
		if _, err := io.WriteString(w, partial); err != nil {
			return err
		}
		return errWriteFailed
	}
}

// assertOnly checks that dir holds exactly the given files (no temp files
// left behind).
func assertOnly(t *testing.T, dir string, want ...string) {
	t.Helper()
	got := dirEntries(t, dir)
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("folder holds %q, want %q", got, want)
	}
}

func assertFile(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Errorf("%s = %q, want %q", filepath.Base(path), got, want)
	}
}

func assertMode(t *testing.T, path string, want fs.FileMode) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return // Windows only has a read-only flag
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Errorf("%s mode = %#o, want %#o", filepath.Base(path), got, want)
	}
}

func TestWriteFileAtomicReplaces(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "book.xlsx")
	if err := os.WriteFile(path, []byte("old"), 0o640); err != nil {
		t.Fatal(err)
	}

	err := writeFileAtomic(path, func(w io.Writer) error {
		// The data goes to a separate temp file in the same folder; the
		// original is untouched until the rename.
		f, ok := w.(*os.File)
		if !ok {
			t.Fatalf("writer is %T, want *os.File", w)
		}
		if filepath.Dir(f.Name()) != dir || f.Name() == path {
			t.Errorf("temp file %q is not a separate file in %q", f.Name(), dir)
		}
		assertFile(t, path, "old")
		_, err := io.WriteString(w, "new contents")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	assertFile(t, path, "new contents")
	assertMode(t, path, 0o640) // the original's permissions, not the temp file's 0600
	assertOnly(t, dir, "book.xlsx")
}

func TestWriteFileAtomicNewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "new.xlsx")
	if err := writeFileAtomic(path, writeString("hello")); err != nil {
		t.Fatal(err)
	}
	assertFile(t, path, "hello")
	assertMode(t, path, newFileMode())
	if m := newFileMode(); m&0o111 != 0 || m&0o600 != 0o600 {
		t.Errorf("new file mode %#o: want owner read/write and never executable", m)
	}
	assertOnly(t, dir, "new.xlsx")
}

// TestWriteFileAtomicFailedWrite is the key safety check: if writing fails
// partway, the original keeps its exact bytes and permissions and no temp
// file is left behind.
func TestWriteFileAtomicFailedWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "book.xlsx")
	original, err := os.ReadFile(filepath.Join(testdataDir, "plain.xlsx"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, original, 0o640); err != nil {
		t.Fatal(err)
	}

	err = writeFileAtomic(path, failAfter("PK\x03\x04 half a zip"))
	if !errors.Is(err, errWriteFailed) {
		t.Fatalf("err = %v, want the write error", err)
	}
	assertFile(t, path, string(original))
	assertMode(t, path, 0o640)
	assertOnly(t, dir, "book.xlsx")

	// And the original still opens as a workbook.
	wb, err := Open(path)
	if err != nil {
		t.Fatalf("original no longer opens: %v", err)
	}
	if got := wb.Value("Sheet1", 0, 0); got != "Name" {
		t.Errorf("A1 = %q after failed save", got)
	}
	if err := wb.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestWriteFileAtomicFailedWriteNewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "new.xlsx")
	if err := writeFileAtomic(path, failAfter("partial")); !errors.Is(err, errWriteFailed) {
		t.Fatalf("err = %v, want the write error", err)
	}
	assertOnly(t, dir) // no file created, no temp left
}

func TestWriteFileAtomicNotARegularFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "folder.xlsx")
	if err := os.Mkdir(path, 0o750); err != nil {
		t.Fatal(err)
	}
	called := false
	err := writeFileAtomic(path, func(io.Writer) error { called = true; return nil })
	if err == nil {
		t.Fatal("want error when the target is a folder")
	}
	if called {
		t.Error("write was called for a target that can't be replaced")
	}
	assertOnly(t, dir, "folder.xlsx")
}

// TestWriteFileAtomicSymlink: saving through a link writes the link's target
// and keeps the link.
func TestWriteFileAtomicSymlink(t *testing.T) {
	dir := t.TempDir()
	realDir := filepath.Join(dir, "real")
	if err := os.Mkdir(realDir, 0o750); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(realDir, "book.xlsx")
	if err := os.WriteFile(target, []byte("old"), 0o640); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.xlsx")
	if err := os.Symlink(filepath.Join("real", "book.xlsx"), link); err != nil {
		t.Skipf("can't make symlinks here: %v", err)
	}

	if err := writeFileAtomic(link, writeString("new")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&fs.ModeSymlink == 0 {
		t.Fatal("the link was replaced by a regular file")
	}
	if got, err := os.Readlink(link); err != nil || got != filepath.Join("real", "book.xlsx") {
		t.Errorf("link now points to %q (%v)", got, err)
	}
	assertFile(t, target, "new")
	assertMode(t, target, 0o640)
	assertOnly(t, dir, "link.xlsx", "real")
	assertOnly(t, realDir, "book.xlsx") // the temp file was made beside the target
}

func TestWriteFileAtomicBrokenSymlink(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "link.xlsx")
	if err := os.Symlink(filepath.Join(dir, "missing.xlsx"), link); err != nil {
		t.Skipf("can't make symlinks here: %v", err)
	}
	if err := writeFileAtomic(link, writeString("new")); err == nil {
		t.Fatal("want error for a link whose target is missing")
	}
	assertOnly(t, dir, "link.xlsx")
}

func TestWriteFileAtomicReadOnlyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "book.xlsx")
	if err := os.WriteFile(path, []byte("old"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) }) // so TempDir can be removed on Windows

	called := false
	err := writeFileAtomic(path, func(io.Writer) error { called = true; return nil })
	if !errors.Is(err, ErrReadOnly) {
		t.Fatalf("err = %v, want ErrReadOnly", err)
	}
	if called {
		t.Error("write was called for a read-only file")
	}
	assertFile(t, path, "old")
	assertMode(t, path, 0o444)
	assertOnly(t, dir, "book.xlsx")
}

// TestWriteFileAtomicReadOnlyFolder: a folder we can't write (like a
// write-protected USB stick) also reports ErrReadOnly.
func TestWriteFileAtomicReadOnlyFolder(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("folder permissions work differently on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores folder permissions")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "book.xlsx")
	if err := os.WriteFile(path, []byte("old"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	err := writeFileAtomic(path, writeString("new"))
	if !errors.Is(err, ErrReadOnly) {
		t.Fatalf("err = %v, want ErrReadOnly", err)
	}
	assertFile(t, path, "old")
}

func TestWriteFileAtomicMissingFolder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "no-such-folder", "book.xlsx")
	if err := writeFileAtomic(path, writeString("x")); err == nil {
		t.Fatal("want error")
	}
}
