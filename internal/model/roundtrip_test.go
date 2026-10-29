package model

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

// allFixtures lists every spreadsheet fixture (generated and real).
func allFixtures() []string {
	var names []string
	for _, fx := range fixtures() {
		names = append(names, fx.name)
	}
	for _, fx := range realFixtures() {
		names = append(names, fx.name)
	}
	return names
}

// copyFixture copies a fixture into a temp folder so it can be saved over.
func copyFixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(testdataDir, name))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// readLikeUI reads every visible cell the way the app does (display value,
// formula bar text, undo snapshot), so a save test also catches reads that
// change stored data.
func readLikeUI(t *testing.T, wb *Workbook) {
	t.Helper()
	for _, sheet := range wb.Sheets() {
		rows, cols, err := wb.Size(sheet)
		if err != nil {
			t.Fatal(err)
		}
		for r := range rows {
			for c := range cols {
				_ = wb.Value(sheet, r, c)
				if _, err := wb.Formula(sheet, r, c); err != nil {
					t.Fatal(err)
				}
				if wb.ReadOnlyReason() == "" {
					if _, err := wb.Snapshot(sheet, r, c); err != nil {
						t.Fatal(err)
					}
				}
			}
		}
	}
}

// openRaw opens a file with excelize directly, for comparing.
func openRaw(t *testing.T, path string) *excelize.File {
	t.Helper()
	f, err := excelize.OpenFile(path, openOptions(""))
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	t.Cleanup(func() { closeFile(t, f) })
	return f
}

// compareOptions says which cells a comparison skips (because a test
// edited them).
type compareOptions struct {
	skip map[string]bool // "Sheet!A1"
}

// compareWorkbooks checks that saved holds everything orig does: sheets,
// every cell's raw value, formula, type and full style, merges, names,
// column widths, row heights, sheet properties, validations, conditional
// formats, comments, tables, and every zip part.
func compareWorkbooks(t *testing.T, origPath, savedPath string, opts compareOptions) {
	t.Helper()
	orig, saved := openRaw(t, origPath), openRaw(t, savedPath)
	raw := excelize.Options{RawCellValue: true}

	sheets := orig.GetSheetList()
	if got := saved.GetSheetList(); !slices.Equal(got, sheets) {
		t.Fatalf("sheets = %q, want %q", got, sheets)
	}
	check := func(what string, got, want any) {
		t.Helper()
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s:\n got %+v\nwant %+v", what, got, want)
		}
	}
	both := func(what string, read func(*excelize.File) (any, error)) {
		t.Helper()
		want, err1 := read(orig)
		got, err2 := read(saved)
		if err1 != nil || err2 != nil {
			t.Errorf("%s: %v / %v", what, err1, err2)
			return
		}
		check(what, got, want)
	}

	for _, sheet := range sheets {
		both(sheet+" visible", func(f *excelize.File) (any, error) { return f.GetSheetVisible(sheet) })
		both(sheet+" props", func(f *excelize.File) (any, error) { return f.GetSheetProps(sheet) })
		both(sheet+" merges", func(f *excelize.File) (any, error) {
			merges, err := f.GetMergeCells(sheet)
			var out []string
			for _, m := range merges {
				out = append(out, m.GetStartAxis()+":"+m.GetEndAxis())
			}
			return out, err
		})
		both(sheet+" data validations", func(f *excelize.File) (any, error) { return f.GetDataValidations(sheet) })
		both(sheet+" conditional formats", func(f *excelize.File) (any, error) { return f.GetConditionalFormats(sheet) })
		both(sheet+" comments", func(f *excelize.File) (any, error) { return f.GetComments(sheet) })

		rowsO, err := orig.GetRows(sheet, raw)
		if err != nil {
			t.Errorf("%s rows: %v", sheet, err)
			continue
		}
		rowsS, err := saved.GetRows(sheet, raw)
		if err != nil {
			t.Errorf("%s rows: %v", sheet, err)
			continue
		}
		nRows, nCols := max(len(rowsO), len(rowsS)), 0
		for _, r := range slices.Concat(rowsO, rowsS) {
			nCols = max(nCols, len(r))
		}
		for c := 1; c <= nCols; c++ {
			col := ColumnName(c - 1)
			both(sheet+"!"+col+" width", func(f *excelize.File) (any, error) { return f.GetColWidth(sheet, col) })
		}
		errs := 0
		for r := 1; r <= nRows; r++ {
			both(fmt.Sprintf("%s row %d height", sheet, r), func(f *excelize.File) (any, error) { return f.GetRowHeight(sheet, r) })
			for c := 1; c <= nCols && errs < 10; c++ {
				cell := CellName(r-1, c-1)
				if opts.skip[sheet+"!"+cell] {
					continue
				}
				if !sameCell(t, orig, saved, sheet, cell) {
					errs++
				}
			}
		}
	}
	both("defined names", func(f *excelize.File) (any, error) { return f.GetDefinedName(), nil })

	// Raw zip parts: nothing dropped, the VBA project byte-identical, and
	// every table's displayName and range kept (ONLYOFFICE writes only
	// displayName, which excelize's GetTables doesn't report).
	partsO, partsS := zipPartsAt(t, origPath), zipPartsAt(t, savedPath)
	for name := range partsO {
		if _, ok := partsS[name]; !ok {
			t.Errorf("part %s was dropped", name)
		}
	}
	if vba, ok := partsO["xl/vbaProject.bin"]; ok && !bytes.Equal(vba, partsS["xl/vbaProject.bin"]) {
		t.Error("xl/vbaProject.bin changed")
	}
	check("tables", tableIDs(t, partsS), tableIDs(t, partsO))
}

// sameCell compares one cell's raw value, formula, type and full style.
func sameCell(t *testing.T, orig, saved *excelize.File, sheet, cell string) bool {
	t.Helper()
	type state struct {
		Raw, Formula string
		Type         excelize.CellType
		Style        *excelize.Style
	}
	read := func(f *excelize.File) state {
		var s state
		var err error
		s.Raw, err = f.GetCellValue(sheet, cell, excelize.Options{RawCellValue: true})
		if err == nil {
			s.Formula, err = f.GetCellFormula(sheet, cell)
		}
		if err == nil {
			s.Type, err = f.GetCellType(sheet, cell)
		}
		var id int
		if err == nil {
			id, err = f.GetCellStyle(sheet, cell)
		}
		if err == nil {
			s.Style, err = f.GetStyle(id)
		}
		if err != nil {
			t.Errorf("%s!%s: %v", sheet, cell, err)
		}
		return s
	}
	want, got := read(orig), read(saved)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s!%s:\n got %+v\nwant %+v", sheet, cell, got, want)
		return false
	}
	return true
}

func zipPartsAt(t *testing.T, path string) map[string][]byte {
	t.Helper()
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = zr.Close() }()
	parts := map[string][]byte{}
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
		parts[zf.Name] = data
	}
	return parts
}

// tableIDs returns "displayName ref" for every table part, sorted.
func tableIDs(t *testing.T, parts map[string][]byte) []string {
	t.Helper()
	var ids []string
	for name, data := range parts {
		if !strings.HasPrefix(name, "xl/tables/") || !strings.HasSuffix(name, ".xml") {
			continue
		}
		var tbl struct {
			DisplayName string `xml:"displayName,attr"`
			Ref         string `xml:"ref,attr"`
		}
		if err := xml.Unmarshal(data, &tbl); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		ids = append(ids, tbl.DisplayName+" "+tbl.Ref)
	}
	slices.Sort(ids)
	return ids
}

// TestRoundTrip is the safety net: every fixture survives open → read
// everything → save → reopen without losing anything.
func TestRoundTrip(t *testing.T) {
	for _, name := range allFixtures() {
		t.Run(name, func(t *testing.T) {
			orig := filepath.Join(testdataDir, name)
			wb := openWorkbook(t, name)
			readLikeUI(t, wb)
			saved := filepath.Join(t.TempDir(), name)
			if err := wb.SaveAs(saved); err != nil {
				t.Fatal(err)
			}
			if wb.Path() != saved {
				t.Errorf("Path() = %q after SaveAs, want %q", wb.Path(), saved)
			}
			compareWorkbooks(t, orig, saved, compareOptions{})
			if calc := calcProps(t, saved); calc.FullCalcOnLoad != nil && *calc.FullCalcOnLoad {
				t.Error("fullCalcOnLoad set although nothing was edited")
			}
		})
	}
}

func calcProps(t *testing.T, path string) excelize.CalcPropsOptions {
	t.Helper()
	props, err := openRaw(t, path).GetCalcProps()
	if err != nil {
		t.Fatal(err)
	}
	return props
}

// TestRoundTripTablesNamed: the ONLYOFFICE tables keep their displayName,
// which its structured references (Table1[Item]) depend on.
func TestRoundTripTablesNamed(t *testing.T) {
	path := filepath.Join(testdataDir, "onlyoffice-bankbook.xlsx")
	ids := tableIDs(t, zipPartsAt(t, path))
	if len(ids) != 5 {
		t.Fatalf("tables = %q, want 5", ids)
	}
	for _, id := range ids {
		if !strings.HasPrefix(id, "Table") {
			t.Errorf("table %q has no displayName", id)
		}
	}
}

func TestEditAndSave(t *testing.T) {
	path := copyFixture(t, "plain.xlsx")
	orig := filepath.Join(testdataDir, "plain.xlsx")
	wb, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = wb.Close() })
	readLikeUI(t, wb)

	edits := map[string]string{
		"A2": "Apricot", "B2": "11.5", "D2": "FALSE", "F1": "2024-03-15 13:45", "F2": "=B2*2", "A8": "",
	}
	skip := map[string]bool{}
	for cell, input := range edits {
		setInput(t, wb, "Sheet1", cell, input)
		skip["Sheet1!"+cell] = true
	}
	if err := wb.Save(); err != nil {
		t.Fatal(err)
	}

	compareWorkbooks(t, orig, path, compareOptions{skip: skip})
	f := openRaw(t, path)
	raw := excelize.Options{RawCellValue: true}
	want := map[string]string{
		"A2": "Apricot", "B2": "11.5", "D2": "0", "F1": formatSerial(45366 + (13*60+45)/1440.0), "F2": "", "A8": "",
	}
	for cell, w := range want {
		if got, err := f.GetCellValue("Sheet1", cell, raw); err != nil || got != w {
			t.Errorf("%s = %q (%v), want %q", cell, got, err, w)
		}
	}
	if got, err := f.GetCellFormula("Sheet1", "F2"); err != nil || got != "B2*2" {
		t.Errorf("F2 formula = %q (%v)", got, err)
	}
	if calc := calcProps(t, path); calc.FullCalcOnLoad == nil || !*calc.FullCalcOnLoad {
		t.Error("fullCalcOnLoad not set after an edit")
	}
}

// TestEditSharedMasterAndSave: after editing a shared formula's master and
// saving, the rest of the group still has its formulas.
func TestEditSharedMasterAndSave(t *testing.T) {
	path := copyFixture(t, "excel-shared-formulas.xlsx")
	wb, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = wb.Close() })
	setInput(t, wb, "Label", "A2", "replaced")
	if err := wb.Save(); err != nil {
		t.Fatal(err)
	}
	f := openRaw(t, path)
	for r := 3; r <= 41; r++ {
		cell := "A" + strconv.Itoa(r)
		if got, err := f.GetCellFormula("Label", cell); err != nil || got != "B"+strconv.Itoa(r) {
			t.Errorf("%s formula = %q (%v)", cell, got, err)
		}
	}
	skip := map[string]bool{"Label!A2": true}
	compareWorkbooksFormulasOnly(t, filepath.Join(testdataDir, "excel-shared-formulas.xlsx"), path, skip)
}

// compareWorkbooksFormulasOnly checks every formula is kept (the member
// cells changed from shared to plain formulas, which is fine).
func compareWorkbooksFormulasOnly(t *testing.T, origPath, savedPath string, skip map[string]bool) {
	t.Helper()
	orig, saved := openRaw(t, origPath), openRaw(t, savedPath)
	for _, sheet := range orig.GetSheetList() {
		rows, err := orig.GetRows(sheet)
		if err != nil {
			t.Fatal(err)
		}
		for r := range rows {
			for c := range rows[r] {
				cell := CellName(r, c)
				if skip[sheet+"!"+cell] {
					continue
				}
				want, err1 := orig.GetCellFormula(sheet, cell)
				got, err2 := saved.GetCellFormula(sheet, cell)
				if err1 != nil || err2 != nil {
					t.Fatalf("%s!%s: %v / %v", sheet, cell, err1, err2)
				}
				if got != want {
					t.Errorf("%s!%s formula = %q, want %q", sheet, cell, got, want)
				}
			}
		}
	}
}

func TestEditMacroAndSave(t *testing.T) {
	path := copyFixture(t, "macro.xlsm")
	wb, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = wb.Close() })
	setInput(t, wb, "Sheet1", "A1", "edited")
	if err := wb.Save(); err != nil {
		t.Fatal(err)
	}
	compareWorkbooks(t, filepath.Join(testdataDir, "macro.xlsm"), path, compareOptions{skip: map[string]bool{"Sheet1!A1": true}})
	ct := string(zipPartsAt(t, path)["[Content_Types].xml"])
	if !strings.Contains(ct, "macroEnabled") {
		t.Error("saved .xlsm lost its macro-enabled content type")
	}
}

func TestSaveAsTypeRules(t *testing.T) {
	tests := []struct {
		from, to string
		ok       bool
	}{
		{"plain.xlsx", "copy.xlsx", true},
		{"plain.xlsx", "COPY.XLSX", true},
		{"plain.xlsx", "copy.xlsm", false},
		{"plain.xlsx", "copy.csv", false},
		{"plain.xlsx", "copy", false},
		{"macro.xlsm", "copy.xlsm", true},
		{"macro.xlsm", "copy.xlsx", false},
	}
	for _, tt := range tests {
		wb := openWorkbook(t, tt.from)
		target := filepath.Join(t.TempDir(), tt.to)
		err := wb.SaveAs(target)
		if (err == nil) != tt.ok {
			t.Errorf("%s → %s: err = %v, want ok=%v", tt.from, tt.to, err, tt.ok)
		}
		if _, statErr := os.Stat(target); (statErr == nil) != tt.ok {
			t.Errorf("%s → %s: file exists = %v", tt.from, tt.to, statErr == nil)
		}
	}
}

func TestSaveRefusedWhenReadOnly(t *testing.T) {
	parts := zipParts(t, "multisheet.xlsx")
	parts["xl/worksheets/sheet2.xml"] = []byte("<worksheet><sheetData><row>not xml")
	path := writeZip(t, "broken.xlsx", parts)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	wb, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = wb.Close() })
	if err := wb.Save(); err == nil {
		t.Error("Save of a damaged file: want error")
	}
	if err := wb.SaveAs(filepath.Join(t.TempDir(), "copy.xlsx")); err == nil {
		t.Error("SaveAs of a damaged file: want error")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("the damaged file was changed")
	}
}

func TestSaveOverReadOnlyFile(t *testing.T) {
	path := copyFixture(t, "plain.xlsx")
	if err := os.Chmod(path, 0o444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
	wb, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = wb.Close() })
	setInput(t, wb, "Sheet1", "A1", "x")
	if err := wb.Save(); !errors.Is(err, ErrReadOnly) {
		t.Errorf("err = %v, want ErrReadOnly", err)
	}
	if wb.Path() != path {
		t.Errorf("Path() changed to %q", wb.Path())
	}
}
