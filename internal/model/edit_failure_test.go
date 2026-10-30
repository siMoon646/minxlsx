package model

import (
	"errors"
	"testing"

	"github.com/xuri/excelize/v2"
)

// escapeThenControl is escape-like text cut short by a control character.
// The control character is stored as an escape that starts with "_", which
// would complete "_x0041" into an escape unless that "_" is escaped too.
const escapeThenControl = "_x0041\x01"

// TestSetInputEscapeThenControl: typing escapeThenControl stores it exactly,
// in MinXLSX and after a save.
func TestSetInputEscapeThenControl(t *testing.T) {
	wb := newWorkbook(t, func(*excelize.File) {})
	setInput(t, wb, "Sheet1", "A1", escapeThenControl)
	for _, w := range []*Workbook{wb, reopen(t, wb)} {
		if got := w.Value("Sheet1", 0, 0); got != escapeThenControl {
			t.Errorf("A1 shows %q, want %q", got, escapeThenControl)
		}
	}
}

// TestRestoreEscapeThenControl: undo puts escapeThenControl back exactly
// when the file held it (correctly escaped).
func TestRestoreEscapeThenControl(t *testing.T) {
	wb := withSheetXML(t, `<row r="1"><c r="A1" t="inlineStr"><is><t>_x005F_x0041_x0001_</t></is></c></row>`)
	if got := wb.Value("Sheet1", 0, 0); got != escapeThenControl {
		t.Fatalf("A1 shows %q, want %q", got, escapeThenControl)
	}
	snap, err := wb.Snapshot("Sheet1", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	setInput(t, wb, "Sheet1", "A1", "hello")
	if err := wb.Restore("Sheet1", 0, 0, snap); err != nil {
		t.Fatal(err)
	}
	for _, w := range []*Workbook{wb, reopen(t, wb)} {
		if got := w.Value("Sheet1", 0, 0); got != escapeThenControl {
			t.Errorf("after undo A1 shows %q, want %q", got, escapeThenControl)
		}
	}
}

// TestCSVEscapeThenControl: a CSV field holding escapeThenControl opens,
// shows as written and saves unchanged.
func TestCSVEscapeThenControl(t *testing.T) {
	content := escapeThenControl + ",ok\n"
	path := writeCSVFile(t, "escape-control.csv", content)
	wb := openCSVAt(t, path)
	sheet := wb.file.GetSheetList()[0]
	if got := wb.Value(sheet, 0, 0); got != escapeThenControl {
		t.Errorf("A1 shows %q, want %q", got, escapeThenControl)
	}
	setInput(t, wb, sheet, "B1", "edited") // re-encodes row 1
	if err := wb.Save(); err != nil {
		t.Fatal(err)
	}
	if got, want := readFile(t, path), escapeThenControl+",edited\n"; got != want {
		t.Errorf("saved %q, want %q", got, want)
	}
}

// fillCellStyles fills the workbook's cell-format table to excelize's limit,
// so the next NewStyle fails before anything is written.
func fillCellStyles(t *testing.T, wb *Workbook) {
	t.Helper()
	if _, err := wb.file.GetStyle(0); err != nil { // loads the style sheet
		t.Fatal(err)
	}
	xfs := wb.file.Styles.CellXfs.Xf
	for len(xfs) < excelize.MaxCellStyles {
		xfs = append(xfs, xfs[0])
	}
	wb.file.Styles.CellXfs.Xf = xfs
	wb.file.Styles.CellXfs.Count = len(xfs)
}

// TestFailedEditBeforeWriteKeepsCell: an edit that fails before writing
// anything (here: typing a date needs a new cell format, and the table is
// full) leaves the cell exactly as it was, including what a snapshot can't
// hold: rich-text runs, a formula's cached result, a t="d" date and an error
// constant.
func TestFailedEditBeforeWriteKeepsCell(t *testing.T) {
	rich := newWorkbook(t, func(f *excelize.File) {
		runs := []excelize.RichTextRun{{Text: "bold", Font: &excelize.Font{Bold: true}}, {Text: " plain"}}
		if err := f.SetCellRichText("Sheet1", "A1", runs); err != nil {
			t.Fatal(err)
		}
	})
	other := withSheetXML(t, `<row r="1">`+
		`<c r="A1"><f>1+1</f><v>2</v></c>`+
		`<c r="B1" t="d"><v>2024-03-15T10:00:00Z</v></c>`+
		`<c r="C1" t="e"><v>#N/A</v></c>`+
		`</row>`)
	cases := []struct {
		wb   *Workbook
		cell string
	}{{rich, "A1"}, {other, "A1"}, {other, "B1"}, {other, "C1"}}
	for _, c := range cases {
		fillCellStyles(t, c.wb)
		before := readCell(t, c.wb, "Sheet1", c.cell)
		runs, err := c.wb.file.GetCellRichText("Sheet1", c.cell)
		if err != nil {
			t.Fatal(err)
		}
		row, col := cellIndex(t, c.cell)
		if err := c.wb.SetInput("Sheet1", row, col, "2024-03-16"); !errors.Is(err, excelize.ErrCellStyles) {
			t.Fatalf("%s: SetInput error = %v, want ErrCellStyles", c.cell, err)
		}
		if after := readCell(t, c.wb, "Sheet1", c.cell); after != before {
			t.Errorf("%s changed: %+v -> %+v", c.cell, before, after)
		}
		after, err := c.wb.file.GetCellRichText("Sheet1", c.cell)
		if err != nil {
			t.Fatal(err)
		}
		if len(after) != len(runs) {
			t.Errorf("%s: %d rich-text runs before, %d after", c.cell, len(runs), len(after))
		}
	}
}

// TestFailedEditAfterWritePutsCellBack: an edit that fails after writing
// (as storeText does when it refuses text) leaves the cell as it was.
func TestFailedEditAfterWritePutsCellBack(t *testing.T) {
	for _, csv := range []bool{false, true} {
		var wb *Workbook
		sheet := "Sheet1"
		if csv {
			wb = openCSVAt(t, writeCSVFile(t, "fault.csv", "old,b\n"))
			sheet = wb.file.GetSheetList()[0]
		} else {
			wb = withSheetXML(t, `<row r="1"><c r="A1" t="inlineStr"><is><t>old</t></is></c></row>`)
		}
		fault := errors.New("injected")
		wb.faultAfterEdit = func() error { return fault }
		if err := wb.SetInput(sheet, 0, 0, "new"); !errors.Is(err, fault) {
			t.Fatalf("csv=%v: SetInput error = %v, want the injected fault", csv, err)
		}
		wb.faultAfterEdit = nil
		if got := wb.Value(sheet, 0, 0); got != "old" {
			t.Errorf("csv=%v: A1 shows %q after a failed edit, want old", csv, got)
		}
		saved := reopen(t, wb)
		if got := saved.Value(saved.file.GetSheetList()[0], 0, 0); got != "old" {
			t.Errorf("csv=%v: A1 saved as %q after a failed edit, want old", csv, got)
		}
	}
}
