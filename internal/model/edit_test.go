package model

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"
)

// writeZip writes parts to a new zip file in a temp folder.
func writeZip(t *testing.T, name string, parts map[string][]byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for _, part := range sortedKeys(parts) {
		w, err := zw.Create(part)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(parts[part]); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

// cellState is what a cell holds, read straight from excelize.
type cellState struct {
	typ     excelize.CellType
	raw     string
	formula string
	style   int
}

func readCell(t *testing.T, wb *Workbook, sheet, cell string) cellState {
	t.Helper()
	typ, err := wb.file.GetCellType(sheet, cell)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := wb.file.GetCellValue(sheet, cell, excelize.Options{RawCellValue: true})
	if err != nil {
		t.Fatal(err)
	}
	formula, err := wb.file.GetCellFormula(sheet, cell)
	if err != nil {
		t.Fatal(err)
	}
	style, err := wb.file.GetCellStyle(sheet, cell)
	if err != nil {
		t.Fatal(err)
	}
	return cellState{typ: typ, raw: raw, formula: formula, style: style}
}

// formatSerial formats a number the way excelize stores it.
func formatSerial(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// setInput edits a cell by name and fails the test on error.
func setInput(t *testing.T, wb *Workbook, sheet, cell, input string) {
	t.Helper()
	row, col := cellIndex(t, cell)
	if err := wb.SetInput(sheet, row, col, input); err != nil {
		t.Fatalf("SetInput(%s!%s, %q): %v", sheet, cell, input, err)
	}
}

func TestScanFindsSharedFormulaMasters(t *testing.T) {
	tests := []struct {
		file, sheet, master, ref string
	}{
		{"excel-shared-formulas.xlsx", "Label", "A2", "A2:A41"},
		{"onlyoffice-bankbook.xlsx", "Sheet1", "H3", "H3:H6"},
	}
	for _, tt := range tests {
		wb := openWorkbook(t, tt.file)
		if got := wb.scan[tt.sheet].masters[tt.master]; got != tt.ref {
			t.Errorf("%s: master %s ref = %q, want %q (masters %v)", tt.file, tt.master, got, tt.ref, wb.scan[tt.sheet].masters)
		}
		if wb.ReadOnlyReason() != "" {
			t.Errorf("%s: ReadOnlyReason = %q", tt.file, wb.ReadOnlyReason())
		}
	}
	for _, fx := range fixtures() {
		wb := openWorkbook(t, fx.name)
		if wb.ReadOnlyReason() != "" {
			t.Errorf("%s: ReadOnlyReason = %q", fx.name, wb.ReadOnlyReason())
		}
		for sheet, s := range wb.scan {
			if len(s.masters) != 0 {
				t.Errorf("%s!%s: unexpected shared formulas %v", fx.name, sheet, s.masters)
			}
		}
	}
}

func TestBrokenSheetOpensReadOnly(t *testing.T) {
	parts := zipParts(t, "multisheet.xlsx")
	parts["xl/worksheets/sheet2.xml"] = []byte("<worksheet><sheetData><row>not xml")
	path := writeZip(t, "broken.xlsx", parts)

	wb, err := Open(path)
	if err != nil {
		t.Fatalf("a broken sheet should still open for viewing: %v", err)
	}
	t.Cleanup(func() { _ = wb.Close() })
	reason := wb.ReadOnlyReason()
	if !strings.Contains(reason, "Data 2024") {
		t.Errorf("ReadOnlyReason = %q, want it to name the broken sheet", reason)
	}
	if got := wb.Value("Summary", 0, 0); got != "Total" {
		t.Errorf("other sheets still readable: Summary A1 = %q", got)
	}
	if err := wb.SetInput("Summary", 0, 0, "x"); err == nil {
		t.Error("SetInput on a read-only workbook: want error")
	}
	if wb.Edited() {
		t.Error("refused edit marked the workbook edited")
	}
}

func TestSetInputKinds(t *testing.T) {
	wb := openWorkbook(t, "plain.xlsx")
	tests := []struct {
		cell, input string
		typ         excelize.CellType
		raw, shown  string
		formula     string
	}{
		{"F1", "hello", excelize.CellTypeSharedString, "hello", "hello", ""},
		{"F2", "007", excelize.CellTypeSharedString, "007", "007", ""},
		{"F3", "'TRUE", excelize.CellTypeSharedString, "TRUE", "TRUE", ""},
		{"F4", "1234.5", excelize.CellTypeUnset, "1234.5", "1234.5", ""},
		{"F5", "-3", excelize.CellTypeUnset, "-3", "-3", ""},
		{"F6", "true", excelize.CellTypeBool, "1", "TRUE", ""},
		{"F7", "=B2*2", excelize.CellTypeFormula, "", "", "B2*2"},
		{"A1", "Product", excelize.CellTypeSharedString, "Product", "Product", ""}, // overwrite text
	}
	for _, tt := range tests {
		setInput(t, wb, "Sheet1", tt.cell, tt.input)
		got := readCell(t, wb, "Sheet1", tt.cell)
		if got.typ != tt.typ || got.raw != tt.raw || got.formula != tt.formula {
			t.Errorf("%s := %q: got %+v, want type %v raw %q formula %q", tt.cell, tt.input, got, tt.typ, tt.raw, tt.formula)
		}
		row, col := cellIndex(t, tt.cell)
		if shown := wb.Value("Sheet1", row, col); shown != tt.shown {
			t.Errorf("%s := %q: grid shows %q, want %q", tt.cell, tt.input, shown, tt.shown)
		}
	}
	if !wb.Edited() {
		t.Error("Edited() = false after edits")
	}
	// The data extent grows to cover the new column.
	if _, cols, err := wb.Size("Sheet1"); err != nil || cols != 6 {
		t.Errorf("cols = %d (%v), want 6", cols, err)
	}
}

func TestSetInputFormulaReplacesCachedValue(t *testing.T) {
	wb := openWorkbook(t, "excel-budget.xlsx")
	const sheet = "Simple Monthly Budget"
	// E4 is a formula with a cached value (2336).
	setInput(t, wb, sheet, "E4", "=1+1")
	got := readCell(t, wb, sheet, "E4")
	if got.formula != "1+1" || got.raw != "" {
		t.Errorf("E4 = %+v: the old cached value must not be shown for a new formula", got)
	}
	// Text over a formula removes the formula.
	setInput(t, wb, sheet, "E4", "plain")
	if got := readCell(t, wb, sheet, "E4"); got.formula != "" || got.raw != "plain" {
		t.Errorf("E4 = %+v after typing text over a formula", got)
	}
}

func TestSetInputKeepsStyle(t *testing.T) {
	wb := openWorkbook(t, "styled.xlsx")
	before := readCell(t, wb, "Sheet1", "B1") // #,##0.00
	setInput(t, wb, "Sheet1", "B1", "5000")
	after := readCell(t, wb, "Sheet1", "B1")
	if after.style != before.style {
		t.Errorf("style changed from %d to %d", before.style, after.style)
	}
	if got := wb.Value("Sheet1", 0, 1); got != "5,000.00" {
		t.Errorf("B1 shows %q, want 5,000.00 (number format kept)", got)
	}

	bold := readCell(t, wb, "Sheet1", "A1")
	setInput(t, wb, "Sheet1", "A1", "")
	cleared := readCell(t, wb, "Sheet1", "A1")
	if cleared.raw != "" || cleared.style != bold.style {
		t.Errorf("clearing A1: %+v, want empty with style %d", cleared, bold.style)
	}
}

func TestSetInputDates(t *testing.T) {
	auckland, err := time.LoadLocation("Pacific/Auckland")
	if err == nil {
		old := time.Local
		time.Local = auckland // a timezone must not shift a typed date
		t.Cleanup(func() { time.Local = old })
	}

	t.Run("new date format on an unstyled cell", func(t *testing.T) {
		wb := openWorkbook(t, "plain.xlsx")
		setInput(t, wb, "Sheet1", "F1", "2024-03-15 13:45")
		got := readCell(t, wb, "Sheet1", "F1")
		// Excel's serial for 2024-03-15 13:45: day 45366 plus 13:45 as a
		// fraction of a day.
		want := 45366 + (13*60+45)/1440.0
		if got.raw != formatSerial(want) {
			t.Errorf("raw = %q, want %q", got.raw, formatSerial(want))
		}
		if shown := wb.Value("Sheet1", 0, 5); shown != "2024-03-15 13:45" {
			t.Errorf("shows %q, want 2024-03-15 13:45", shown)
		}
		setInput(t, wb, "Sheet1", "F2", "2024-03-15")
		if shown := wb.Value("Sheet1", 1, 5); shown != "2024-03-15" {
			t.Errorf("date-only shows %q", shown)
		}
		if raw := readCell(t, wb, "Sheet1", "F2").raw; raw != "45366" {
			t.Errorf("date-only raw = %q, want 45366", raw)
		}
	})

	t.Run("existing date format is kept", func(t *testing.T) {
		wb := openWorkbook(t, "dates.xlsx")
		before := readCell(t, wb, "Sheet1", "B3") // dd/mm/yyyy
		setInput(t, wb, "Sheet1", "B3", "2024-12-25")
		if got := readCell(t, wb, "Sheet1", "B3"); got.style != before.style {
			t.Errorf("style changed: %d -> %d", before.style, got.style)
		}
		if shown := wb.Value("Sheet1", 2, 1); shown != "25/12/2024" {
			t.Errorf("shows %q, want 25/12/2024", shown)
		}
	})

	t.Run("other style parts survive", func(t *testing.T) {
		wb := openWorkbook(t, "styled.xlsx")
		setInput(t, wb, "Sheet1", "A1", "2024-03-15") // A1 is bold
		id := readCell(t, wb, "Sheet1", "A1").style
		style, err := wb.file.GetStyle(id)
		if err != nil {
			t.Fatal(err)
		}
		if style.Font == nil || !style.Font.Bold {
			t.Errorf("bold lost: %+v", style.Font)
		}
		if style.CustomNumFmt == nil || *style.CustomNumFmt != "yyyy-mm-dd" {
			t.Errorf("number format = %v, want yyyy-mm-dd", style.CustomNumFmt)
		}
		if shown := wb.Value("Sheet1", 0, 0); shown != "2024-03-15" {
			t.Errorf("shows %q", shown)
		}
	})
}

// TestSetInputSharedFormulaMaster: excelize deletes a whole shared-formula
// group when its master cell is overwritten. The other cells must keep
// their formulas and values.
func TestSetInputSharedFormulaMaster(t *testing.T) {
	for _, input := range []string{"replaced", "=B2&\"!\"", ""} {
		t.Run(input, func(t *testing.T) {
			wb := openWorkbook(t, "excel-shared-formulas.xlsx")
			before := map[string]cellState{}
			for r := 3; r <= 41; r++ {
				cell := "A" + strconv.Itoa(r)
				before[cell] = readCell(t, wb, "Label", cell)
			}
			setInput(t, wb, "Label", "A2", input)
			for r := 3; r <= 41; r++ {
				cell := "A" + strconv.Itoa(r)
				got := readCell(t, wb, "Label", cell)
				if got.formula != "B"+strconv.Itoa(r) || got.raw != before[cell].raw {
					t.Errorf("%s = %+v, want formula B%d and value %q", cell, got, r, before[cell].raw)
				}
			}
			if _, ok := wb.scan["Label"].masters["A2"]; ok {
				t.Error("A2 is still recorded as a shared master")
			}
		})
	}
}

func TestSetInputSharedFormulaMember(t *testing.T) {
	wb := openWorkbook(t, "excel-shared-formulas.xlsx")
	setInput(t, wb, "Label", "A5", "member")
	for cell, want := range map[string]string{"A2": "B2", "A4": "B4", "A6": "B6", "A41": "B41"} {
		if got := readCell(t, wb, "Label", cell).formula; got != want {
			t.Errorf("%s formula = %q, want %q", cell, got, want)
		}
	}
	if got := readCell(t, wb, "Label", "A5"); got.formula != "" || got.raw != "member" {
		t.Errorf("A5 = %+v", got)
	}
}

func TestSetInputMergedCell(t *testing.T) {
	wb := openWorkbook(t, "merged.xlsx")
	if r, c := wb.EditTarget("Sheet1", 0, 2); r != 0 || c != 0 { // C1 is inside A1:D1
		t.Errorf("EditTarget(C1) = (%d, %d), want A1", r, c)
	}
	if r, c := wb.EditTarget("Sheet1", 4, 4); r != 2 || c != 2 { // E5 is inside C3:E5
		t.Errorf("EditTarget(E5) = (%d, %d), want C3", r, c)
	}
	if r, c := wb.EditTarget("Sheet1", 7, 7); r != 7 || c != 7 {
		t.Errorf("EditTarget(H8) = (%d, %d), want itself", r, c)
	}
	setInput(t, wb, "Sheet1", "C1", "New title")
	if got := readCell(t, wb, "Sheet1", "A1").raw; got != "New title" {
		t.Errorf("A1 = %q, want the edit redirected there", got)
	}
	// GetCellValue reports a merge's value for every cell in it, so check
	// C1's own storage through GetRows, which doesn't.
	rows, err := wb.file.GetRows("Sheet1")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows[0]) > 2 && rows[0][2] != "" {
		t.Errorf("C1 = %q, want empty (hidden by the merge)", rows[0][2])
	}
}

// TestReadingNeverRoundsStoredNumbers guards against an excelize v2.11.0
// trap: a formatted GetCellValue rounds the stored number to 15 digits, and
// the rounded value would be saved. Nothing the UI reads may change a value.
func TestReadingNeverRoundsStoredNumbers(t *testing.T) {
	wb := openWorkbook(t, "dates.xlsx")
	raw := func() []string {
		var out []string
		for r := 2; r <= 7; r++ {
			out = append(out, readCell(t, wb, "Sheet1", "B"+strconv.Itoa(r)).raw)
		}
		return out
	}
	before := raw()
	if before[0] != "45366.57326388889" {
		t.Fatalf("B2 raw = %q; fixture changed?", before[0])
	}
	readAll := func() {
		rows, cols, err := wb.Size("Sheet1")
		if err != nil {
			t.Fatal(err)
		}
		for r := range rows + 1 {
			for c := range cols + 1 {
				_ = wb.Value("Sheet1", r, c)
				if _, err := wb.Formula("Sheet1", r, c); err != nil {
					t.Fatal(err)
				}
				if _, err := wb.Snapshot("Sheet1", r, c); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	readAll()
	setInput(t, wb, "Sheet1", "D1", "edited") // loads the sheet into excelize
	readAll()
	if got := raw(); strings.Join(got, "|") != strings.Join(before, "|") {
		t.Errorf("stored numbers changed by reading:\n got %q\nwant %q", got, before)
	}
}

func TestSetInputErrors(t *testing.T) {
	wb := openWorkbook(t, "multisheet.xlsx")
	tests := []struct {
		name     string
		sheet    string
		row, col int
	}{
		{"unknown sheet", "Nope", 0, 0},
		{"negative row", "Summary", -1, 0},
		{"past the last column", "Summary", 0, maxCols},
	}
	for _, tt := range tests {
		if err := wb.SetInput(tt.sheet, tt.row, tt.col, "x"); err == nil {
			t.Errorf("%s: want error", tt.name)
		}
	}
	if wb.Edited() {
		t.Error("failed edits marked the workbook edited")
	}
	if err := wb.Close(); err != nil {
		t.Fatal(err)
	}
	if err := wb.SetInput("Summary", 0, 0, "x"); err == nil {
		t.Error("SetInput after Close: want error")
	}
}

// TestSnapshotRestore: every kind of cell comes back exactly after an edit
// is undone (cached formula results excepted).
func TestSnapshotRestore(t *testing.T) {
	cells := []struct{ file, sheet, cell string }{
		{"plain.xlsx", "Sheet1", "A1"},  // text
		{"plain.xlsx", "Sheet1", "B2"},  // number
		{"plain.xlsx", "Sheet1", "D2"},  // boolean
		{"plain.xlsx", "Sheet1", "A5"},  // empty, but the row exists
		{"plain.xlsx", "Sheet1", "Z99"}, // never existed
		{"formulas.xlsx", "Sheet1", "A4"},
		{"dates.xlsx", "Sheet1", "B2"},
		{"styled.xlsx", "Sheet1", "B1"},
		{"styled.xlsx", "Sheet1", "A3"},
		{"unicode.xlsx", "Ünïcödé 表", "A4"},
		{"excel-shared-formulas.xlsx", "Label", "A2"}, // shared master
		{"excel-shared-formulas.xlsx", "Label", "A7"}, // shared member
	}
	for _, c := range cells {
		t.Run(c.file+"!"+c.cell, func(t *testing.T) {
			wb := openWorkbook(t, c.file)
			row, col := cellIndex(t, c.cell)
			before := readCell(t, wb, c.sheet, c.cell)
			shown := wb.Value(c.sheet, row, col)
			snap, err := wb.Snapshot(c.sheet, row, col)
			if err != nil {
				t.Fatal(err)
			}
			setInput(t, wb, c.sheet, c.cell, "2024-01-02") // changes value and style
			if err := wb.Restore(c.sheet, row, col, snap); err != nil {
				t.Fatal(err)
			}
			after := readCell(t, wb, c.sheet, c.cell)
			if before.formula != "" {
				// A restored formula has no cached result.
				before.raw, before.typ = "", after.typ
			} else if got := wb.Value(c.sheet, row, col); got != shown {
				t.Errorf("grid shows %q after restore, want %q", got, shown)
			}
			if after != before {
				t.Errorf("after restore %+v, want %+v", after, before)
			}
		})
	}
}
