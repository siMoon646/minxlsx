package model

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// openWorkbook opens a fixture through the Workbook API and closes it when
// the test ends.
func openWorkbook(t *testing.T, name string) *Workbook {
	t.Helper()
	wb, err := Open(filepath.Join(testdataDir, name))
	if err != nil {
		t.Fatalf("open %s: %v", name, err)
	}
	t.Cleanup(func() {
		if err := wb.Close(); err != nil {
			t.Errorf("close %s: %v", name, err)
		}
	})
	return wb
}

// cellIndex turns "B4" into 0-based (row 3, col 1).
func cellIndex(t *testing.T, cell string) (row, col int) {
	t.Helper()
	r, c, err := CellIndex(cell)
	if err != nil {
		t.Fatalf("bad cell %q: %v", cell, err)
	}
	return r, c
}

func TestWorkbookGeneratedFixtures(t *testing.T) {
	for _, fx := range fixtures() {
		t.Run(fx.name, func(t *testing.T) {
			wb := openWorkbook(t, fx.name)

			// The only hidden sheet among generated fixtures is "Hidden".
			want := slices.DeleteFunc(slices.Clone(fx.sheets), func(s string) bool { return s == "Hidden" })
			if got := wb.Sheets(); !slices.Equal(got, want) {
				t.Errorf("Sheets() = %q, want %q", got, want)
			}

			for _, ref := range sortedKeys(fx.cells) {
				sheet, cell := splitRef(t, ref)
				row, col := cellIndex(t, cell)
				if got, want := wb.Value(sheet, row, col), fx.cells[ref]; got != want {
					t.Errorf("Value(%s) = %q, want %q", ref, got, want)
				}
			}
			for _, ref := range sortedKeys(fx.forms) {
				sheet, cell := splitRef(t, ref)
				row, col := cellIndex(t, cell)
				got, err := wb.Formula(sheet, row, col)
				if err != nil {
					t.Errorf("Formula(%s): %v", ref, err)
					continue
				}
				if want := "=" + fx.forms[ref]; got != want {
					t.Errorf("Formula(%s) = %q, want %q", ref, got, want)
				}
			}
		})
	}
}

func TestWorkbookRealWorldFixtures(t *testing.T) {
	for _, fx := range realFixtures() {
		t.Run(fx.name, func(t *testing.T) {
			wb := openWorkbook(t, fx.name)

			want := slices.DeleteFunc(slices.Clone(fx.sheets), func(s string) bool {
				return slices.Contains(fx.hidden, s)
			})
			if got := wb.Sheets(); !slices.Equal(got, want) {
				t.Errorf("Sheets() = %q, want %q", got, want)
			}
			// Every sheet (hidden too) must load without error.
			for _, sheet := range fx.sheets {
				if _, _, err := wb.Size(sheet); err != nil {
					t.Errorf("Size(%s): %v", sheet, err)
				}
			}
			for _, ref := range sortedKeys(fx.forms) {
				sheet, cell := splitRef(t, ref)
				row, col := cellIndex(t, cell)
				got, err := wb.Formula(sheet, row, col)
				if err != nil {
					t.Errorf("Formula(%s): %v", ref, err)
					continue
				}
				if want := "=" + fx.forms[ref]; got != want {
					t.Errorf("Formula(%s) = %q, want %q", ref, got, want)
				}
			}
		})
	}
}

func TestWorkbookSize(t *testing.T) {
	tests := []struct {
		file, sheet        string
		wantRows, wantCols int
	}{
		{"large.xlsx", "Sheet1", largeRows + 1, 3},
		{"empty.xlsx", "Sheet1", 0, 0},
		{"plain.xlsx", "Sheet1", 8, 4},
		{"multisheet.xlsx", "Empty", 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.file+"/"+tt.sheet, func(t *testing.T) {
			wb := openWorkbook(t, tt.file)
			rows, cols, err := wb.Size(tt.sheet)
			if err != nil {
				t.Fatal(err)
			}
			if rows != tt.wantRows || cols != tt.wantCols {
				t.Errorf("Size = %d×%d, want %d×%d", rows, cols, tt.wantRows, tt.wantCols)
			}
		})
	}
}

func TestWorkbookValueOutOfRange(t *testing.T) {
	wb := openWorkbook(t, "plain.xlsx")
	rows, cols, err := wb.Size("Sheet1")
	if err != nil {
		t.Fatal(err)
	}
	// Row 5 is {nil, 0}: GetRows drops the trailing empty cells, so the row
	// is shorter than the header row. Reading past its end must give "".
	if got := wb.Value("Sheet1", 4, 1); got != "0" {
		t.Fatalf("B5 = %q, want %q", got, "0")
	}
	tests := []struct {
		name     string
		sheet    string
		row, col int
	}{
		{"past end of short row (D5)", "Sheet1", 4, 3},
		{"past end of one-cell row (B6)", "Sheet1", 5, 1},
		{"row past Size", "Sheet1", rows, 0},
		{"col past widest row", "Sheet1", 0, cols},
		{"far away", "Sheet1", 1 << 20, 1 << 14},
		{"negative row", "Sheet1", -1, 0},
		{"negative col", "Sheet1", 0, -1},
		{"unknown sheet", "NoSuchSheet", 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := wb.Value(tt.sheet, tt.row, tt.col); got != "" {
				t.Errorf("Value(%s, %d, %d) = %q, want empty", tt.sheet, tt.row, tt.col, got)
			}
		})
	}
	if _, _, err := wb.Size("NoSuchSheet"); err == nil {
		t.Error("Size(unknown sheet): want error")
	}
}

func TestWorkbookFormulaPlainCell(t *testing.T) {
	wb := openWorkbook(t, "plain.xlsx")
	tests := []struct {
		cell, want string
	}{
		{"A1", "Name"},
		{"A8", "007"},
		{"D5", ""}, // past the end of a short row
		{"Z99", ""},
	}
	for _, tt := range tests {
		row, col := cellIndex(t, tt.cell)
		got, err := wb.Formula("Sheet1", row, col)
		if err != nil {
			t.Errorf("Formula(%s): %v", tt.cell, err)
			continue
		}
		if got != tt.want {
			t.Errorf("Formula(%s) = %q, want %q", tt.cell, got, tt.want)
		}
	}
	if _, err := wb.Formula("Sheet1", -1, 0); err == nil {
		t.Error("Formula(negative row): want error")
	}
}

func TestWorkbookMergedTopLeftOnly(t *testing.T) {
	wb := openWorkbook(t, "merged.xlsx")
	if got := wb.Value("Sheet1", 0, 0); got != "Title across A1:D1" {
		t.Errorf("A1 = %q", got)
	}
	for _, cell := range []string{"B1", "D1", "A4", "D4", "E5"} {
		row, col := cellIndex(t, cell)
		if got := wb.Value("Sheet1", row, col); got != "" {
			t.Errorf("%s = %q, want empty (inside a merge)", cell, got)
		}
	}
}

func TestOpenErrors(t *testing.T) {
	dir := t.TempDir()
	notZip := filepath.Join(dir, "not-a-zip.xlsx")
	if err := os.WriteFile(notZip, []byte("this is not a zip file"), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, path, wantInErr string
	}{
		{"missing file", filepath.Join(dir, "missing.xlsx"), "missing.xlsx"},
		{"csv not supported yet", filepath.Join(testdataDir, "plain.csv"), ".csv"},
		{"no extension", filepath.Join(dir, "noext"), "noext"},
		{"not a zip", notZip, "not-a-zip.xlsx"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wb, err := Open(tt.path)
			if err == nil {
				_ = wb.Close()
				t.Fatal("want error")
			}
			if !strings.Contains(err.Error(), tt.wantInErr) {
				t.Errorf("error %q does not mention %q", err, tt.wantInErr)
			}
		})
	}
}

func TestOpenExtensionCaseInsensitive(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(testdataDir, "plain.xlsx"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"UPPER.XLSX", "macro.xlsm", "Mixed.XlSm"} {
		path := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		wb, err := Open(path)
		if err != nil {
			t.Errorf("Open(%s): %v", name, err)
			continue
		}
		if wb.Path() != path {
			t.Errorf("Path() = %q, want %q", wb.Path(), path)
		}
		if err := wb.Close(); err != nil {
			t.Errorf("close %s: %v", name, err)
		}
	}
}

func TestWorkbookCloseTwice(t *testing.T) {
	wb, err := Open(filepath.Join(testdataDir, "plain.xlsx"))
	if err != nil {
		t.Fatal(err)
	}
	if wb.Closed() {
		t.Fatal("Closed() = true before Close")
	}
	if err := wb.Close(); err != nil {
		t.Fatal(err)
	}
	if !wb.Closed() {
		t.Error("Closed() = false after Close")
	}
	if err := wb.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
	if got := wb.Value("Sheet1", 0, 0); got != "" {
		t.Errorf("Value after Close = %q, want empty", got)
	}
}

func TestCellNames(t *testing.T) {
	if got := CellName(3, 1); got != "B4" {
		t.Errorf("CellName(3, 1) = %q, want B4", got)
	}
	if got := CellName(0, 0); got != "A1" {
		t.Errorf("CellName(0, 0) = %q, want A1", got)
	}
	cols := map[int]string{0: "A", 25: "Z", 26: "AA", 701: "ZZ", 702: "AAA", 16383: "XFD"}
	for col, want := range cols {
		if got := ColumnName(col); got != want {
			t.Errorf("ColumnName(%d) = %q, want %q", col, got, want)
		}
	}
	for _, bad := range []int{-1, 16384} {
		if got := ColumnName(bad); got != "" {
			t.Errorf("ColumnName(%d) = %q, want empty", bad, got)
		}
	}
	if got := CellName(-1, 0); got != "" {
		t.Errorf("CellName(-1, 0) = %q, want empty", got)
	}
	r, c, err := CellIndex("XFD1048576")
	if err != nil || r != 1048575 || c != 16383 {
		t.Errorf("CellIndex(XFD1048576) = %d, %d, %v", r, c, err)
	}
}
