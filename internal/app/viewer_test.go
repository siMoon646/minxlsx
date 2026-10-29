package app

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

var testdataDir = filepath.Join("..", "..", "testdata")

func fixture(name string) string { return filepath.Join(testdataDir, name) }

func openViewer(t *testing.T, name string) *Viewer {
	t.Helper()
	v := NewViewer()
	if err := v.Open(fixture(name)); err != nil {
		t.Fatalf("open %s: %v", name, err)
	}
	t.Cleanup(func() {
		if err := v.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})
	return v
}

func TestViewerOpen(t *testing.T) {
	v := openViewer(t, "multisheet.xlsx")
	if got := v.FileName(); got != "multisheet.xlsx" {
		t.Errorf("FileName() = %q", got)
	}
	if !v.HasFile() {
		t.Error("HasFile() = false")
	}
	want := []string{"Summary", "Data 2024", "Empty"}
	if got := v.Sheets(); !slices.Equal(got, want) {
		t.Errorf("Sheets() = %q, want %q", got, want)
	}
	if v.CurrentSheet() != 0 {
		t.Errorf("CurrentSheet() = %d, want 0", v.CurrentSheet())
	}
	if addr, _ := v.Selected(); addr != "A1" {
		t.Errorf("selected %q after open, want A1", addr)
	}
	if got := v.Value(0, 0); got != "Total" {
		t.Errorf("Summary A1 = %q, want Total", got)
	}
}

func TestViewerSelectSheet(t *testing.T) {
	v := openViewer(t, "multisheet.xlsx")
	changes := 0
	v.OnChange = func() { changes++ }

	v.Select(1, 1)
	if err := v.SelectSheet(1); err != nil {
		t.Fatal(err)
	}
	if changes != 1 {
		t.Errorf("OnChange called %d times, want 1", changes)
	}
	if got := v.Value(1, 0); got != "Jan" {
		t.Errorf("Data 2024 A2 = %q, want Jan", got)
	}
	if rows, cols := v.Size(); rows != 4 || cols != 2 {
		t.Errorf("Size() = %d×%d, want 4×2", rows, cols)
	}
	// Switching sheets moves the selection back to A1.
	if addr, text := v.Selected(); addr != "A1" || text != "Month" {
		t.Errorf("Selected() = %q, %q, want A1, Month", addr, text)
	}

	if err := v.SelectSheet(2); err != nil {
		t.Fatal(err)
	}
	if rows, cols := v.Size(); rows != 0 || cols != 0 {
		t.Errorf("Empty sheet Size() = %d×%d", rows, cols)
	}
	for _, bad := range []int{-1, 3} {
		if err := v.SelectSheet(bad); err == nil {
			t.Errorf("SelectSheet(%d): want error", bad)
		}
	}
	if v.CurrentSheet() != 2 {
		t.Errorf("a bad SelectSheet changed the sheet to %d", v.CurrentSheet())
	}
}

func TestViewerSelectedFormula(t *testing.T) {
	v := openViewer(t, "multisheet.xlsx")
	v.Select(0, 1)
	addr, text := v.Selected()
	if addr != "B1" || text != "=SUM('Data 2024'!B2:B4)" {
		t.Errorf("Selected() = %q, %q", addr, text)
	}
	v.Select(-1, 5) // ignored
	if addr, _ := v.Selected(); addr != "B1" {
		t.Errorf("bad Select moved selection to %q", addr)
	}
}

func TestViewerFailedOpenKeepsFile(t *testing.T) {
	v := openViewer(t, "plain.xlsx")
	if err := v.Open(fixture("does-not-exist.xlsx")); err == nil {
		t.Fatal("want error")
	}
	if err := v.Open(fixture("plain.csv")); err == nil {
		t.Fatal("want error for .csv")
	}
	if got := v.FileName(); got != "plain.xlsx" {
		t.Errorf("FileName() = %q after failed open, want plain.xlsx", got)
	}
	if got := v.Value(0, 0); got != "Name" {
		t.Errorf("A1 = %q after failed open", got)
	}
}

// rewriteFixture copies a fixture into a temp folder, passing each zip part
// through edit, and returns the new file's path.
func rewriteFixture(t *testing.T, name string, edit func(part string, data []byte) []byte) string {
	t.Helper()
	zr, err := zip.OpenReader(fixture(name))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = zr.Close() }()

	out := filepath.Join(t.TempDir(), name)
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for _, zf := range zr.File {
		rc, err := zf.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		w, err := zw.Create(zf.Name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(edit(zf.Name, data)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestViewerOpenRejectsBrokenFiles(t *testing.T) {
	allHidden := rewriteFixture(t, "plain.xlsx", func(part string, data []byte) []byte {
		if part == "xl/workbook.xml" {
			return bytes.ReplaceAll(data, []byte(`<sheet `), []byte(`<sheet state="hidden" `))
		}
		return data
	})
	// The other failure path in Open (the first sheet can't be read) isn't
	// reachable from a file: excelize's GetRows returns no rows, not an
	// error, even for a worksheet part full of broken XML.
	v := openViewer(t, "multisheet.xlsx")
	err := v.Open(allHidden)
	if err == nil {
		t.Fatal("want error")
	}
	if !strings.Contains(err.Error(), "no visible sheets") {
		t.Errorf("error %q does not mention no visible sheets", err)
	}
	if v.FileName() != "multisheet.xlsx" || v.Value(0, 0) != "Total" {
		t.Errorf("previous file not kept: %q", v.FileName())
	}
}

func TestViewerOpenSecondFile(t *testing.T) {
	v := openViewer(t, "multisheet.xlsx")
	if err := v.SelectSheet(1); err != nil {
		t.Fatal(err)
	}
	first := v.wb
	if err := v.Open(fixture("plain.xlsx")); err != nil {
		t.Fatal(err)
	}
	if !first.Closed() {
		t.Error("first workbook was not closed")
	}
	if v.CurrentSheet() != 0 || v.FileName() != "plain.xlsx" {
		t.Errorf("after second open: sheet %d, file %q", v.CurrentSheet(), v.FileName())
	}
}

func TestViewerNoFile(t *testing.T) {
	v := NewViewer()
	if v.HasFile() || v.FileName() != "" || len(v.Sheets()) != 0 {
		t.Error("new viewer should be empty")
	}
	if rows, cols := v.Size(); rows != 0 || cols != 0 {
		t.Errorf("Size() = %d×%d", rows, cols)
	}
	if v.Value(0, 0) != "" || v.ColumnName(0) != "A" {
		t.Error("Value/ColumnName with no file")
	}
	v.Select(2, 2)
	if addr, text := v.Selected(); addr != "C3" || text != "" {
		t.Errorf("Selected() = %q, %q", addr, text)
	}
	if err := v.SelectSheet(0); err == nil {
		t.Error("SelectSheet with no file: want error")
	}
	if err := v.Close(); err != nil {
		t.Errorf("Close with no file: %v", err)
	}
}

func TestViewerCloseTwice(t *testing.T) {
	v := NewViewer()
	if err := v.Open(fixture("plain.xlsx")); err != nil {
		t.Fatal(err)
	}
	changes := 0
	v.OnChange = func() { changes++ }
	if err := v.Close(); err != nil {
		t.Fatal(err)
	}
	if err := v.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
	if v.HasFile() || v.FileName() != "" {
		t.Error("file still open after Close")
	}
	if changes != 1 {
		t.Errorf("OnChange called %d times, want 1", changes)
	}
}
