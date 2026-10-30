package model

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

// withSheetXML opens plain.xlsx with Sheet1 replaced by the given XML rows.
func withSheetXML(t *testing.T, rows string) *Workbook {
	t.Helper()
	parts := zipParts(t, "plain.xlsx")
	parts["xl/worksheets/sheet1.xml"] = []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>` +
		rows + `</sheetData></worksheet>`)
	return openAt(t, writeZip(t, "custom.xlsx", parts))
}

// newWorkbook builds a workbook with excelize, saves it and opens it.
func newWorkbook(t *testing.T, build func(f *excelize.File)) *Workbook {
	t.Helper()
	f := excelize.NewFile()
	build(f)
	path := filepath.Join(t.TempDir(), "new.xlsx")
	if err := f.SaveAs(path); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return openAt(t, path)
}

func openAt(t *testing.T, path string) *Workbook {
	t.Helper()
	wb, err := Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	t.Cleanup(func() { _ = wb.Close() })
	return wb
}

// reopen saves the workbook to a new path and opens the result.
func reopen(t *testing.T, wb *Workbook) *Workbook {
	t.Helper()
	path := filepath.Join(t.TempDir(), "saved"+filepath.Ext(wb.Path()))
	if err := wb.SaveAs(path); err != nil {
		t.Fatal(err)
	}
	return openAt(t, path)
}

// TestSetInputEscapeLikeText: the file format reads "_x0041_" as "A", so
// typed text that looks like that must be stored escaped and come back as
// typed, in MinXLSX and after a save.
func TestSetInputEscapeLikeText(t *testing.T) {
	inputs := map[string]string{
		"F1": "_x0041_",
		"F2": "a_x0041_b_x005F_c",
		"F3": "_x00_", // not an escape: kept as is
		"F4": "'_x0041_",
		"F5": "_x0041_x0042_", // overlapping: the second escape starts at the first's last "_"
		"F6": "__x0041_",
	}
	want := map[string]string{
		"F1": "_x0041_", "F2": "a_x0041_b_x005F_c", "F3": "_x00_", "F4": "_x0041_",
		"F5": "_x0041_x0042_", "F6": "__x0041_",
	}
	wb := openWorkbook(t, "plain.xlsx")
	for cell, in := range inputs {
		setInput(t, wb, "Sheet1", cell, in)
	}
	check := func(wb *Workbook, when string) {
		t.Helper()
		for cell, w := range want {
			row, col := cellIndex(t, cell)
			if got := wb.Value("Sheet1", row, col); got != w {
				t.Errorf("%s: %s shows %q, want %q", when, cell, got, w)
			}
		}
	}
	check(wb, "after edit")

	// Undo keeps the text literal too.
	row, col := cellIndex(t, "F1")
	snap, err := wb.Snapshot("Sheet1", row, col)
	if err != nil {
		t.Fatal(err)
	}
	setInput(t, wb, "Sheet1", "F1", "other")
	if err := wb.Restore("Sheet1", row, col, snap); err != nil {
		t.Fatal(err)
	}
	check(wb, "after restore")
	check(reopen(t, wb), "after save")
}

func TestSetInputTextLimit(t *testing.T) {
	wb := openWorkbook(t, "plain.xlsx")
	setInput(t, wb, "Sheet1", "F1", strings.Repeat("a", maxCellChars))
	wb.edited = false
	tooLong := map[string]string{
		"ascii": strings.Repeat("a", maxCellChars+1),
		"emoji": strings.Repeat("😀", maxCellChars/2+1), // two UTF-16 units each
	}
	for name, s := range tooLong {
		if err := wb.SetInput("Sheet1", 0, 6, s); err == nil {
			t.Errorf("%s: %d-unit text accepted, want error", name, utf16Len(s))
		}
	}
	if wb.Edited() {
		t.Error("refused edit marked the workbook edited")
	}
}

// TestSetInputArrayFormulaLocked: one cell of an array formula can't be
// changed on its own (Excel refuses too); other cells still can.
func TestSetInputArrayFormulaLocked(t *testing.T) {
	wb := newWorkbook(t, func(f *excelize.File) {
		typ, ref := "array", "C1:C2"
		for cell, v := range map[string]int{"A1": 1, "A2": 2, "B1": 3, "B2": 4} {
			if err := f.SetCellInt("Sheet1", cell, int64(v)); err != nil {
				t.Fatal(err)
			}
		}
		if err := f.SetCellFormula("Sheet1", "C1", "A1:A2*B1:B2", excelize.FormulaOpts{Type: &typ, Ref: &ref}); err != nil {
			t.Fatal(err)
		}
	})
	for _, cell := range []string{"C1", "C2"} {
		row, col := cellIndex(t, cell)
		err := wb.SetInput("Sheet1", row, col, "x")
		if err == nil || !strings.Contains(err.Error(), "C1:C2") {
			t.Errorf("edit %s: err = %v, want refusal naming C1:C2", cell, err)
		}
	}
	if wb.Edited() {
		t.Error("refused edit marked the workbook edited")
	}
	setInput(t, wb, "Sheet1", "D1", "fine")
	if got, err := wb.Formula("Sheet1", 0, 2); err != nil || got != "=A1:A2*B1:B2" {
		t.Errorf("C1 formula = %q (%v), want it unchanged", got, err)
	}
}

// TestSetInputSharedGroupOutsideRef: excelize clears every cell with the
// master's group id, even one outside the master's range. Such a cell must
// keep its formula when the master is edited.
func TestSetInputSharedGroupOutsideRef(t *testing.T) {
	wb := withSheetXML(t,
		`<row r="1"><c r="A1"><f t="shared" ref="A1:A2" si="0">B1*2</f><v>2</v></c><c r="B1"><v>1</v></c></row>`+
			`<row r="2"><c r="A2"><f t="shared" si="0"/><v>4</v></c><c r="B2"><v>2</v></c></row>`+
			`<row r="3"><c r="A3"><f t="shared" si="0"/><v>6</v></c><c r="B3"><v>3</v></c></row>`)
	want := map[string]string{"A2": "B2*2", "A3": "B3*2"}
	for cell, f := range want {
		if got := readCell(t, wb, "Sheet1", cell).formula; got != f {
			t.Fatalf("before edit %s = %q, want %q (excelize reads shared formulas differently?)", cell, got, f)
		}
	}
	setInput(t, wb, "Sheet1", "A1", "master edited")
	for _, w := range []*Workbook{wb, reopen(t, wb)} {
		for cell, f := range want {
			got := readCell(t, w, "Sheet1", cell)
			if got.formula != f {
				t.Errorf("%s = %+v, want formula %q", cell, got, f)
			}
		}
	}
}

// TestRestoreISODate: a date stored as ISO text (t="d") is put back as the
// same date, and non-date t="d" content comes back as text.
func TestRestoreISODate(t *testing.T) {
	wb := withSheetXML(t, `<row r="1">`+
		`<c r="A1" t="d"><v>2024-03-15T00:00:00Z</v></c>`+
		`<c r="B1" t="d"><v>2024-03-15T13:45:00</v></c>`+
		`<c r="C1" t="d"><v>not a date</v></c></row>`)
	want := map[string]string{
		"A1": "45366",
		"B1": formatSerial(45366 + (13*60+45)/1440.0),
		"C1": "not a date",
	}
	for cell, w := range want {
		row, col := cellIndex(t, cell)
		snap, err := wb.Snapshot("Sheet1", row, col)
		if err != nil {
			t.Fatal(err)
		}
		setInput(t, wb, "Sheet1", cell, "x")
		if err := wb.Restore("Sheet1", row, col, snap); err != nil {
			t.Fatalf("restore %s: %v", cell, err)
		}
		if got := readCell(t, wb, "Sheet1", cell).raw; got != w {
			t.Errorf("%s raw = %q after restore, want %q", cell, got, w)
		}
	}
}

// TestSetInputDate1904: in a workbook using the 1904 date system, dates get
// that system's serial numbers, and a date before 1904 stays text.
func TestSetInputDate1904(t *testing.T) {
	wb := newWorkbook(t, func(f *excelize.File) {
		on := true
		if err := f.SetWorkbookProps(&excelize.WorkbookPropsOptions{Date1904: &on}); err != nil {
			t.Fatal(err)
		}
	})
	setInput(t, wb, "Sheet1", "A1", "2024-03-15")
	if got := readCell(t, wb, "Sheet1", "A1").raw; got != "43904" { // 45366 - 1462
		t.Errorf("A1 raw = %q, want 43904", got)
	}
	setInput(t, wb, "Sheet1", "A2", "1903-12-31")
	got := readCell(t, wb, "Sheet1", "A2")
	if got.raw != "1903-12-31" || got.typ != excelize.CellTypeSharedString {
		t.Errorf("A2 = %+v, want the text 1903-12-31", got)
	}
}

// TestCSVEscapeLikeText: a CSV field like "_x0041_" is text, shown and
// saved exactly as written.
func TestCSVEscapeLikeText(t *testing.T) {
	content := "_x0041_,b,_x0041_x0042_,__x0041_\nc,d\n"
	path := writeCSVFile(t, "escape.csv", content)
	wb := openCSVAt(t, path)
	for col, want := range map[int]string{0: "_x0041_", 2: "_x0041_x0042_", 3: "__x0041_"} {
		if got := wb.Value(wb.file.GetSheetList()[0], 0, col); got != want {
			t.Errorf("column %d shows %q, want %q", col, got, want)
		}
	}
	if err := wb.Save(); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); got != content {
		t.Errorf("unedited save changed the file: %q", got)
	}
	setInput(t, wb, wb.file.GetSheetList()[0], "B1", "e") // re-encodes row 1
	if err := wb.Save(); err != nil {
		t.Fatal(err)
	}
	if got, w := readFile(t, path), "_x0041_,e,_x0041_x0042_,__x0041_\nc,d\n"; got != w {
		t.Errorf("saved %q, want %q", got, w)
	}
}

func TestCSVFieldTooLong(t *testing.T) {
	path := writeCSVFile(t, "long.csv", "a,"+strings.Repeat("x", maxCellChars+1)+"\n")
	if wb, err := Open(path); err == nil {
		_ = wb.Close()
		t.Fatal("want error for a field over Excel's cell limit")
	}
}
