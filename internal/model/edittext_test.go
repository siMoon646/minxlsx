package model

import (
	"testing"

	"github.com/xuri/excelize/v2"
)

// editTextCell is a cell set up with excelize directly, the way a file from
// Excel would hold it, and the formula-bar text expected for it.
type editTextCell struct {
	cell  string
	set   func(f *excelize.File, cell string) error
	numFm string // custom number format, or ""
	fmtID int    // built-in number format, or 0
	want  string
}

func num(v float64) func(*excelize.File, string) error {
	return func(f *excelize.File, cell string) error { return f.SetCellFloat("Sheet1", cell, v, -1, 64) }
}

func text(s string) func(*excelize.File, string) error {
	return func(f *excelize.File, cell string) error { return f.SetCellStr("Sheet1", cell, s) }
}

var editTextCells = []editTextCell{
	{cell: "A1", set: num(0.123456), fmtID: 2, want: "0.123456"}, // shown as 0.12
	{cell: "A2", set: num(45366), fmtID: 14, want: "2024-03-15"},
	{cell: "A3", set: num(45366 + (13*60+45)/1440.0), numFm: "yyyy-mm-dd hh:mm", want: "2024-03-15 13:45"},
	{cell: "A4", set: num(45366 + (13*3600+45*60+30)/86400.0), numFm: "dd/mm/yyyy hh:mm:ss", want: "2024-03-15 13:45:30"},
	{cell: "A5", set: num(45366.5), numFm: "dd/mm/yyyy", want: "2024-03-15 12:00"}, // the format hides the time
	{cell: "A6", set: num(0.5), numFm: "h:mm", want: "0.5"},                        // time only: no date to type
	{cell: "A7", set: num(-3), want: "-3"},
	{cell: "A8", set: num(1234.5), fmtID: 4, want: "1234.5"}, // shown as 1,234.50
	{cell: "A9", set: text("123"), want: "'123"},
	{cell: "A10", set: text("=not a formula"), want: "'=not a formula"},
	{cell: "A11", set: text("TRUE"), want: "'TRUE"},
	{cell: "A12", set: text("'abc"), want: "''abc"},
	{cell: "A13", set: text("2024-03-15"), want: "'2024-03-15"},
	{cell: "A14", set: text("007"), want: "007"},
	{cell: "A15", set: text("hello"), want: "hello"},
	{cell: "A16", set: text("="), want: "="},
	{cell: "A17", set: text("+1"), want: "+1"},
	{cell: "A18", set: func(f *excelize.File, cell string) error { return f.SetCellBool("Sheet1", cell, true) }, want: "TRUE"},
	{cell: "A19", set: func(f *excelize.File, cell string) error { return f.SetCellFormula("Sheet1", cell, "A7*2") }, want: "=A7*2"},
	{cell: "A20", set: func(*excelize.File, string) error { return nil }, numFm: "0.00", want: ""}, // styled, empty
	{cell: "A21", set: num(1e20), want: "1E+20"},                                                   // stored as 100000000000000000000
	{cell: "A22", set: num(-1.5e-20), want: "-0.000000000000000000015"},                            // retypes as is
}

// editTextBook builds a workbook holding editTextCells.
func editTextBook(t *testing.T, date1904 bool) *Workbook {
	t.Helper()
	return newWorkbook(t, func(f *excelize.File) {
		if date1904 {
			on := true
			if err := f.SetWorkbookProps(&excelize.WorkbookPropsOptions{Date1904: &on}); err != nil {
				t.Fatal(err)
			}
		}
		for _, c := range editTextCells {
			if err := c.set(f, c.cell); err != nil {
				t.Fatal(err)
			}
			if c.numFm == "" && c.fmtID == 0 {
				continue
			}
			style := &excelize.Style{NumFmt: c.fmtID}
			if c.numFm != "" {
				style.CustomNumFmt = &c.numFm
			}
			id, err := f.NewStyle(style)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.SetCellStyle("Sheet1", c.cell, c.cell, id); err != nil {
				t.Fatal(err)
			}
		}
	})
}

func TestEditText(t *testing.T) {
	wb := editTextBook(t, false)
	for _, c := range editTextCells {
		row, col := cellIndex(t, c.cell)
		got, err := wb.EditText("Sheet1", row, col)
		if err != nil || got != c.want {
			t.Errorf("EditText(%s) = %q (%v), want %q", c.cell, got, err, c.want)
		}
	}
}

// TestEditTextUnreadableStyle: a number whose style can't be read (here a
// style id past the end of the table) shows its stored value, so the
// formula bar isn't empty and committing it doesn't clear the cell.
func TestEditTextUnreadableStyle(t *testing.T) {
	wb := withSheetXML(t, `<row r="1"><c r="A1" s="99"><v>5</v></c></row>`)
	if got, err := wb.EditText("Sheet1", 0, 0); err != nil || got != "5" {
		t.Fatalf("EditText = %q (%v), want 5", got, err)
	}
	setInput(t, wb, "Sheet1", "A1", "5")
	if got := wb.Value("Sheet1", 0, 0); got != "5" || wb.Edited() {
		t.Errorf("A1 = %q, edited %v: want 5, unedited", got, wb.Edited())
	}
}

// TestEditTextRoundTrip: typing a cell's EditText back in (forced, so the
// unchanged-text shortcut doesn't hide anything) leaves the cell as it was.
func TestEditTextRoundTrip(t *testing.T) {
	for _, date1904 := range []bool{false, true} {
		wb := editTextBook(t, date1904)
		for _, c := range editTextCells {
			row, col := cellIndex(t, c.cell)
			before, err := wb.Snapshot("Sheet1", row, col)
			if err != nil {
				t.Fatal(err)
			}
			et, err := wb.EditText("Sheet1", row, col)
			if err != nil {
				t.Fatal(err)
			}
			if err := wb.setInput("Sheet1", row, col, et); err != nil {
				t.Fatalf("setInput(%s, %q): %v", c.cell, et, err)
			}
			after, err := wb.Snapshot("Sheet1", row, col)
			if err != nil {
				t.Fatal(err)
			}
			if before.formula != "" { // a retyped formula has no cached result
				before.raw, before.typ = after.raw, after.typ
			}
			if after != before {
				t.Errorf("1904=%v %s: EditText %q gave %+v, want %+v", date1904, c.cell, et, after, before)
			}
		}
	}
}

// TestSetInputUnchangedIsNoOp: committing the formula-bar text unchanged
// changes nothing, not even a formula's cached result or a number with more
// digits than can be typed, and doesn't mark the workbook edited.
func TestSetInputUnchangedIsNoOp(t *testing.T) {
	files := []struct{ file, sheet string }{
		{"dates.xlsx", "Sheet1"},
		{"formulas.xlsx", "Sheet1"},
		{"styled.xlsx", "Sheet1"},
		{"plain.xlsx", "Sheet1"},
		{"excel-shared-formulas.xlsx", "Label"},
		{"plain.csv", "plain"},
	}
	for _, fx := range files {
		t.Run(fx.file, func(t *testing.T) {
			wb := openWorkbook(t, fx.file)
			rows, cols, err := wb.Size(fx.sheet)
			if err != nil {
				t.Fatal(err)
			}
			for r := range min(rows, 50) {
				for c := range cols {
					before, err := wb.Snapshot(fx.sheet, r, c)
					if err != nil {
						t.Fatal(err)
					}
					et, err := wb.EditText(fx.sheet, r, c)
					if err != nil {
						t.Fatal(err)
					}
					if err := wb.SetInput(fx.sheet, r, c, et); err != nil {
						t.Fatalf("SetInput(%s, %q): %v", CellName(r, c), et, err)
					}
					after, err := wb.Snapshot(fx.sheet, r, c)
					if err != nil {
						t.Fatal(err)
					}
					if after != before {
						t.Errorf("%s changed: %+v -> %+v", CellName(r, c), before, after)
					}
				}
			}
			if wb.Edited() {
				t.Error("unchanged input marked the workbook edited")
			}
		})
	}
	// A number with more digits than Excel takes as typed input.
	wb := withSheetXML(t, `<row r="1"><c r="A1"><v>0.30000000000000004</v></c></row>`)
	et, err := wb.EditText("Sheet1", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	setInput(t, wb, "Sheet1", "A1", et)
	if got := readCell(t, wb, "Sheet1", "A1"); got.raw != "0.30000000000000004" || wb.Edited() {
		t.Errorf("A1 = %+v, edited %v: want the number untouched", got, wb.Edited())
	}
}
