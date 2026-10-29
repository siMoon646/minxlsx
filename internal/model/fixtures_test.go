package model

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"
)

// update regenerates the fixture files in testdata/ at the repo root:
//
//	go test ./internal/model -run TestGenerateFixtures -update
var update = flag.Bool("update", false, "regenerate fixture files in testdata/")

// testdataDir is the repo-root testdata folder, relative to this package.
var testdataDir = filepath.Join("..", "..", "testdata")

// largeRows is the number of data rows (excluding the header) in large.xlsx.
const largeRows = 5000

// fixedTime is used for every date and for document properties so the
// generated files are reproducible.
var fixedTime = time.Date(2024, time.March, 15, 13, 45, 30, 0, time.UTC)

// fixture describes one generated file: how to build it and what a reader
// should find in it.
type fixture struct {
	name   string
	build  func(f *excelize.File) error
	sheets []string          // expected sheet names, in order
	cells  map[string]string // "Sheet!A1" -> expected formatted value (GetCellValue)
	forms  map[string]string // "Sheet!A1" -> expected formula (without leading '=')
	merges map[string]string // sheet -> expected merged ranges, comma separated, e.g. "A1:C1,A3:A5"
}

func fixtures() []fixture {
	return []fixture{
		{
			name:   "plain.xlsx",
			build:  buildPlain,
			sheets: []string{"Sheet1"},
			cells: map[string]string{
				"Sheet1!A1": "Name", "Sheet1!B1": "Qty", "Sheet1!C1": "Price", "Sheet1!D1": "In stock",
				"Sheet1!A2": "Apple", "Sheet1!B2": "10", "Sheet1!C2": "0.5", "Sheet1!D2": "TRUE",
				"Sheet1!A4": "Cherry", "Sheet1!B4": "-3", "Sheet1!C4": "1234567.89", "Sheet1!D4": "FALSE",
				"Sheet1!A5": "", "Sheet1!B5": "0",
				"Sheet1!A6": "  leading and trailing spaces  ",
				"Sheet1!A7": "line one\nline two",
				"Sheet1!A8": "007",
			},
		},
		{
			name:   "formulas.xlsx",
			build:  buildFormulas,
			sheets: []string{"Sheet1"},
			cells: map[string]string{
				"Sheet1!A1": "1", "Sheet1!A2": "2", "Sheet1!A3": "3",
			},
			forms: map[string]string{
				"Sheet1!B1": "A1*2",
				"Sheet1!A4": "SUM(A1:A3)",
				"Sheet1!C1": "IF(A1>1,\"big\",\"small\")",
				"Sheet1!C2": "CONCATENATE(\"x\",A2)",
				"Sheet1!C3": "$A$1+A3",
				"Sheet1!D1": "1/0",
				"Sheet1!D2": "Sheet1!A1+A2",
			},
		},
		{
			name:   "dates.xlsx",
			build:  buildDates,
			sheets: []string{"Sheet1"},
			cells: map[string]string{
				"Sheet1!A1": "Format", "Sheet1!B1": "Value",
				"Sheet1!B2": "2024-03-15",
				"Sheet1!B3": "15/03/2024",
				"Sheet1!B4": "13:45:30",
				"Sheet1!B5": "2024-03-15 13:45",
				"Sheet1!B6": "1900-01-01",
				"Sheet1!B7": "2099-12-31",
			},
		},
		{
			name:   "merged.xlsx",
			build:  buildMerged,
			sheets: []string{"Sheet1"},
			cells: map[string]string{
				"Sheet1!A1": "Title across A1:D1",
				"Sheet1!A3": "Vertical A3:A6",
				"Sheet1!C3": "Block C3:E5",
				"Sheet1!B3": "",
			},
			merges: map[string]string{"Sheet1": "A1:D1,A3:A6,C3:E5"},
		},
		{
			name:   "styled.xlsx",
			build:  buildStyled,
			sheets: []string{"Sheet1"},
			cells: map[string]string{
				"Sheet1!A1": "Bold", "Sheet1!A2": "Italic red", "Sheet1!A3": "Yellow fill",
				"Sheet1!A4": "Bordered", "Sheet1!A5": "Centered wrap",
				"Sheet1!B1": "1,234.50",
				"Sheet1!B2": "12.50%",
				"Sheet1!B3": "$1,234.50",
				"Sheet1!B4": "0012",
				"Sheet1!B5": "1.23E+06",
				"Sheet1!B6": "1234.5",
			},
		},
		{
			name:   "multisheet.xlsx",
			build:  buildMultiSheet,
			sheets: []string{"Summary", "Data 2024", "Empty", "Hidden"},
			cells: map[string]string{
				"Summary!A1":   "Total",
				"Data 2024!A1": "Month", "Data 2024!B1": "Amount",
				"Data 2024!A2": "Jan", "Data 2024!B2": "100",
				"Hidden!A1": "secret",
			},
			forms: map[string]string{
				"Summary!B1": "SUM('Data 2024'!B2:B4)",
			},
		},
		{
			name:   "unicode.xlsx",
			build:  buildUnicode,
			sheets: []string{"Ünïcödé 表"},
			cells: map[string]string{
				"Ünïcödé 表!A1": "Grüße aus Köln",
				"Ünïcödé 表!A2": "日本語のテキスト",
				"Ünïcödé 表!A3": "مرحبا بالعالم",
				"Ünïcödé 表!A4": "😀🎉 emoji",
				"Ünïcödé 表!A5": "Ελληνικά & <xml> \"quotes\"",
				"Ünïcödé 表!A6": "Zero\u200bwidth",
			},
		},
		{
			name:   "empty.xlsx",
			build:  func(*excelize.File) error { return nil },
			sheets: []string{"Sheet1"},
			cells:  map[string]string{"Sheet1!A1": ""},
		},
		{
			name:   "large.xlsx",
			build:  buildLarge,
			sheets: []string{"Sheet1"},
			cells: map[string]string{
				"Sheet1!A1": "ID", "Sheet1!B1": "Label", "Sheet1!C1": "Value",
				"Sheet1!A2": "1", "Sheet1!B2": "row 1", "Sheet1!C2": "7",
				fmt.Sprintf("Sheet1!A%d", largeRows+1): fmt.Sprint(largeRows),
				fmt.Sprintf("Sheet1!B%d", largeRows+1): fmt.Sprintf("row %d", largeRows),
				fmt.Sprintf("Sheet1!C%d", largeRows+1): fmt.Sprint(largeRows * 7 % 1000),
			},
		},
		{
			name:   "macro.xlsm",
			build:  buildMacro,
			sheets: []string{"Sheet1"},
			cells: map[string]string{
				"Sheet1!A1": "Macro fixture", "Sheet1!B1": "42",
			},
			forms: map[string]string{
				"Sheet1!B2": "B1*2",
			},
		},
		{
			name:   "chart.xlsx",
			build:  buildChart,
			sheets: []string{"Sheet1"},
			cells: map[string]string{
				"Sheet1!A1": "Month", "Sheet1!B1": "Sales",
				"Sheet1!A2": "Jan", "Sheet1!B2": "10",
				"Sheet1!A4": "Mar", "Sheet1!B4": "25",
			},
		},
	}
}

func buildPlain(f *excelize.File) error {
	rows := [][]any{
		{"Name", "Qty", "Price", "In stock"},
		{"Apple", 10, 0.5, true},
		{"Banana", 25, 0.25, true},
		{"Cherry", -3, 1234567.89, false},
		{nil, 0},
		{"  leading and trailing spaces  "},
		{"line one\nline two"},
		{"007"}, // numeric-looking text must stay text
	}
	for i, r := range rows {
		if err := f.SetSheetRow("Sheet1", fmt.Sprintf("A%d", i+1), &r); err != nil {
			return err
		}
	}
	return nil
}

func buildFormulas(f *excelize.File) error {
	for i, v := range []int{1, 2, 3} {
		if err := f.SetCellValue("Sheet1", fmt.Sprintf("A%d", i+1), v); err != nil {
			return err
		}
	}
	formulas := [][2]string{
		{"B1", "A1*2"},
		{"A4", "SUM(A1:A3)"},
		{"C1", `IF(A1>1,"big","small")`},
		{"C2", `CONCATENATE("x",A2)`},
		{"C3", "$A$1+A3"},
		{"D1", "1/0"},
		{"D2", "Sheet1!A1+A2"},
	}
	for _, fm := range formulas {
		if err := f.SetCellFormula("Sheet1", fm[0], fm[1]); err != nil {
			return err
		}
	}
	return nil
}

func buildDates(f *excelize.File) error {
	type row struct {
		label, numFmt string
		value         time.Time
	}
	rows := []row{
		{"yyyy-mm-dd", "yyyy-mm-dd", fixedTime},
		{"dd/mm/yyyy", "dd/mm/yyyy", fixedTime},
		{"hh:mm:ss", "hh:mm:ss", fixedTime},
		{"yyyy-mm-dd hh:mm", "yyyy-mm-dd hh:mm", fixedTime},
		{"epoch-ish", "yyyy-mm-dd", time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC)},
		{"far future", "yyyy-mm-dd", time.Date(2099, 12, 31, 0, 0, 0, 0, time.UTC)},
	}
	if err := f.SetSheetRow("Sheet1", "A1", &[]any{"Format", "Value"}); err != nil {
		return err
	}
	for i, r := range rows {
		cell := fmt.Sprintf("B%d", i+2)
		if err := f.SetCellValue("Sheet1", fmt.Sprintf("A%d", i+2), r.label); err != nil {
			return err
		}
		if err := f.SetCellValue("Sheet1", cell, r.value); err != nil {
			return err
		}
		fmtStr := r.numFmt
		style, err := f.NewStyle(&excelize.Style{CustomNumFmt: &fmtStr})
		if err != nil {
			return err
		}
		if err := f.SetCellStyle("Sheet1", cell, cell, style); err != nil {
			return err
		}
	}
	return nil
}

func buildMerged(f *excelize.File) error {
	vals := [][2]string{
		{"A1", "Title across A1:D1"},
		{"A3", "Vertical A3:A6"},
		{"C3", "Block C3:E5"},
	}
	for _, v := range vals {
		if err := f.SetCellValue("Sheet1", v[0], v[1]); err != nil {
			return err
		}
	}
	for _, m := range [][2]string{{"A1", "D1"}, {"A3", "A6"}, {"C3", "E5"}} {
		if err := f.MergeCell("Sheet1", m[0], m[1]); err != nil {
			return err
		}
	}
	return nil
}

// styledCells lists the styles applied in styled.xlsx; it is shared with the
// check so the expected style is defined once.
func styledCells() map[string]*excelize.Style {
	custom := func(s string) *string { return &s }
	border := []excelize.Border{
		{Type: "left", Color: "000000", Style: 1},
		{Type: "top", Color: "000000", Style: 1},
		{Type: "right", Color: "000000", Style: 1},
		{Type: "bottom", Color: "000000", Style: 1},
	}
	return map[string]*excelize.Style{
		"A1": {Font: &excelize.Font{Bold: true}},
		"A2": {Font: &excelize.Font{Italic: true, Color: "FF0000"}},
		"A3": {Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"FFFF00"}}},
		"A4": {Border: border},
		"A5": {Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center", WrapText: true}},
		"B1": {NumFmt: 4},                         // #,##0.00
		"B2": {NumFmt: 10},                        // 0.00%
		"B3": {CustomNumFmt: custom("$#,##0.00")}, // currency
		"B4": {CustomNumFmt: custom("0000")},      // leading zeros
		"B5": {NumFmt: 11},                        // 0.00E+00
	}
}

func buildStyled(f *excelize.File) error {
	values := map[string]any{
		"A1": "Bold", "A2": "Italic red", "A3": "Yellow fill", "A4": "Bordered", "A5": "Centered wrap",
		"B1": 1234.5, "B2": 0.125, "B3": 1234.5, "B4": 12, "B5": 1234567.0,
		"B6": 1234.5, // unstyled control
	}
	for _, cell := range sortedKeys(values) { // sorted: keeps sharedStrings order stable
		if err := f.SetCellValue("Sheet1", cell, values[cell]); err != nil {
			return err
		}
	}
	for _, cell := range sortedKeys(styledCells()) {
		id, err := f.NewStyle(styledCells()[cell])
		if err != nil {
			return err
		}
		if err := f.SetCellStyle("Sheet1", cell, cell, id); err != nil {
			return err
		}
	}
	return f.SetColWidth("Sheet1", "A", "A", 20)
}

func buildMultiSheet(f *excelize.File) error {
	if err := f.SetSheetName("Sheet1", "Summary"); err != nil {
		return err
	}
	for _, name := range []string{"Data 2024", "Empty", "Hidden"} {
		if _, err := f.NewSheet(name); err != nil {
			return err
		}
	}
	rows := [][]any{{"Month", "Amount"}, {"Jan", 100}, {"Feb", 200}, {"Mar", 300}}
	for i, r := range rows {
		if err := f.SetSheetRow("Data 2024", fmt.Sprintf("A%d", i+1), &r); err != nil {
			return err
		}
	}
	if err := f.SetCellValue("Summary", "A1", "Total"); err != nil {
		return err
	}
	if err := f.SetCellFormula("Summary", "B1", "SUM('Data 2024'!B2:B4)"); err != nil {
		return err
	}
	if err := f.SetCellValue("Hidden", "A1", "secret"); err != nil {
		return err
	}
	if err := f.SetSheetVisible("Hidden", false); err != nil {
		return err
	}
	f.SetActiveSheet(0)
	return nil
}

func buildUnicode(f *excelize.File) error {
	const sheet = "Ünïcödé 表"
	if err := f.SetSheetName("Sheet1", sheet); err != nil {
		return err
	}
	vals := []string{
		"Grüße aus Köln",
		"日本語のテキスト",
		"مرحبا بالعالم",
		"😀🎉 emoji",
		"Ελληνικά & <xml> \"quotes\"",
		"Zero\u200bwidth",
	}
	for i, v := range vals {
		if err := f.SetCellValue(sheet, fmt.Sprintf("A%d", i+1), v); err != nil {
			return err
		}
	}
	return nil
}

func buildLarge(f *excelize.File) error {
	sw, err := f.NewStreamWriter("Sheet1")
	if err != nil {
		return err
	}
	if err := sw.SetRow("A1", []any{"ID", "Label", "Value"}); err != nil {
		return err
	}
	for i := 1; i <= largeRows; i++ {
		row := []any{i, fmt.Sprintf("row %d", i), i * 7 % 1000}
		if err := sw.SetRow(fmt.Sprintf("A%d", i+1), row); err != nil {
			return err
		}
	}
	return sw.Flush()
}

func buildChart(f *excelize.File) error {
	rows := [][]any{{"Month", "Sales"}, {"Jan", 10}, {"Feb", 15}, {"Mar", 25}}
	for i, r := range rows {
		if err := f.SetSheetRow("Sheet1", fmt.Sprintf("A%d", i+1), &r); err != nil {
			return err
		}
	}
	return f.AddChart("Sheet1", "D2", &excelize.Chart{
		Type: excelize.Col,
		Series: []excelize.ChartSeries{{
			Name:       "Sheet1!$B$1",
			Categories: "Sheet1!$A$2:$A$4",
			Values:     "Sheet1!$B$2:$B$4",
		}},
		Title: excelize.ChartTitle{Paragraph: []excelize.RichTextRun{{Text: "Sales"}}},
	})
}

// vbaProjectFile is the VBA project in macro.xlsm, copied unchanged from
// excelize's test files (see testdata/README.md). It holds a small
// Worksheet_BeforeDoubleClick handler for Sheet1.
const vbaProjectFile = "vbaProject.bin"

func buildMacro(f *excelize.File) error {
	if err := f.SetSheetRow("Sheet1", "A1", &[]any{"Macro fixture", 42}); err != nil {
		return err
	}
	if err := f.SetCellFormula("Sheet1", "B2", "B1*2"); err != nil {
		return err
	}
	// The VBA code is attached to the sheet by its code name.
	codeName := "Sheet1"
	if err := f.SetSheetProps("Sheet1", &excelize.SheetPropsOptions{CodeName: &codeName}); err != nil {
		return err
	}
	vba, err := os.ReadFile(filepath.Join(testdataDir, vbaProjectFile))
	if err != nil {
		return err
	}
	// Saving with the .xlsm extension sets the macro-enabled content type.
	return f.AddVBAProject(vba)
}

// csvRows is the content of plain.csv and crlf-bom.csv. It includes a quoted
// comma, quotes, a newline inside a field, unicode and an empty field.
var csvRows = [][]string{
	{"Name", "Qty", "Note"},
	{"Apple", "10", "plain"},
	{"Banana", "25", "has, comma"},
	{"Cherry", "-3", `has "quotes"`},
	{"Date", "", "multi\nline"},
	{"Köln", "0.5", "日本語"},
}

func writeCSV(path string) error {
	var b strings.Builder
	w := csv.NewWriter(&b)
	if err := w.WriteAll(csvRows); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

// utf8BOM is the byte-order mark Excel puts at the start of a
// "CSV UTF-8" file (U+FEFF, written as bytes so no editor turns it into an
// invisible literal character).
const utf8BOM = "\xef\xbb\xbf"

// crlfBOMCSV is csvRows written the way Excel's "CSV UTF-8" does: a UTF-8
// BOM, CRLF after every row (including the last), and a bare LF for a line
// break inside a cell. It is spelled out byte for byte, because Go's
// csv.Writer with UseCRLF would also turn the in-cell LF into CRLF.
const crlfBOMCSV = utf8BOM +
	"Name,Qty,Note\r\n" +
	"Apple,10,plain\r\n" +
	"Banana,25,\"has, comma\"\r\n" +
	"Cherry,-3,\"has \"\"quotes\"\"\"\r\n" +
	"Date,,\"multi\nline\"\r\n" +
	"Köln,0.5,日本語\r\n"

// TestGenerateFixtures writes all fixtures into testdata/. It only runs with
// -update so normal test runs never modify the tree.
func TestGenerateFixtures(t *testing.T) {
	if !*update {
		t.Skip("run with -update to regenerate fixtures")
	}
	if err := os.MkdirAll(testdataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, fx := range fixtures() {
		t.Run(fx.name, func(t *testing.T) {
			f := excelize.NewFile()
			defer closeFile(t, f)
			if err := f.SetDocProps(&excelize.DocProperties{
				Creator:        "MinXLSX fixture generator",
				LastModifiedBy: "MinXLSX fixture generator",
				Created:        fixedTime.Format(time.RFC3339),
				Modified:       fixedTime.Format(time.RFC3339),
				Title:          fx.name,
			}); err != nil {
				t.Fatal(err)
			}
			if err := fx.build(f); err != nil {
				t.Fatalf("build: %v", err)
			}
			if err := f.SaveAs(filepath.Join(testdataDir, fx.name)); err != nil {
				t.Fatalf("save: %v", err)
			}
		})
	}
	if err := writeCSV(filepath.Join(testdataDir, "plain.csv")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(testdataDir, "crlf-bom.csv"), []byte(crlfBOMCSV), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestFixtures checks that every committed fixture opens and contains what
// the generator put in it.
func TestFixtures(t *testing.T) {
	for _, fx := range fixtures() {
		t.Run(fx.name, func(t *testing.T) {
			f, err := excelize.OpenFile(filepath.Join(testdataDir, fx.name))
			if err != nil {
				t.Fatalf("open: %v (regenerate with -update?)", err)
			}
			defer closeFile(t, f)

			if got := f.GetSheetList(); strings.Join(got, "|") != strings.Join(fx.sheets, "|") {
				t.Errorf("sheets = %q, want %q", got, fx.sheets)
			}
			for _, ref := range sortedKeys(fx.cells) {
				sheet, cell := splitRef(t, ref)
				got, err := f.GetCellValue(sheet, cell)
				if err != nil {
					t.Errorf("%s: %v", ref, err)
					continue
				}
				if want := fx.cells[ref]; got != want {
					t.Errorf("%s = %q, want %q", ref, got, want)
				}
			}
			for _, ref := range sortedKeys(fx.forms) {
				sheet, cell := splitRef(t, ref)
				got, err := f.GetCellFormula(sheet, cell)
				if err != nil {
					t.Errorf("%s formula: %v", ref, err)
					continue
				}
				if want := fx.forms[ref]; got != want {
					t.Errorf("%s formula = %q, want %q", ref, got, want)
				}
			}
			for _, sheet := range fx.sheets {
				merges, err := f.GetMergeCells(sheet)
				if err != nil {
					t.Fatalf("%s merges: %v", sheet, err)
				}
				var got []string
				for _, m := range merges {
					got = append(got, m.GetStartAxis()+":"+m.GetEndAxis())
				}
				if want := fx.merges[sheet]; strings.Join(got, ",") != want {
					t.Errorf("%s merges = %q, want %q", sheet, strings.Join(got, ","), want)
				}
			}
		})
	}
}

func TestFixtureDetails(t *testing.T) {
	t.Run("styled.xlsx styles", func(t *testing.T) {
		f := openFixture(t, "styled.xlsx")
		for _, cell := range sortedKeys(styledCells()) {
			want := styledCells()[cell]
			id, err := f.GetCellStyle("Sheet1", cell)
			if err != nil {
				t.Fatal(err)
			}
			got, err := f.GetStyle(id)
			if err != nil {
				t.Fatal(err)
			}
			switch {
			case want.Font != nil && (got.Font == nil || got.Font.Bold != want.Font.Bold ||
				got.Font.Italic != want.Font.Italic || !strings.EqualFold(got.Font.Color, want.Font.Color)):
				t.Errorf("%s font = %+v, want %+v", cell, got.Font, want.Font)
			case len(want.Fill.Color) > 0 && (len(got.Fill.Color) == 0 || !strings.EqualFold(got.Fill.Color[0], want.Fill.Color[0])):
				t.Errorf("%s fill = %+v, want %+v", cell, got.Fill, want.Fill)
			case len(want.Border) > 0 && !bordersEqual(got.Border, want.Border):
				t.Errorf("%s borders = %+v, want %+v", cell, got.Border, want.Border)
			case want.Alignment != nil && (got.Alignment == nil || *got.Alignment != *want.Alignment):
				t.Errorf("%s alignment = %+v, want %+v", cell, got.Alignment, want.Alignment)
			case want.NumFmt != 0 && got.NumFmt != want.NumFmt:
				t.Errorf("%s numFmt = %d, want %d", cell, got.NumFmt, want.NumFmt)
			case want.CustomNumFmt != nil && (got.CustomNumFmt == nil || *got.CustomNumFmt != *want.CustomNumFmt):
				t.Errorf("%s custom numFmt = %v, want %q", cell, got.CustomNumFmt, *want.CustomNumFmt)
			}
		}
	})

	t.Run("multisheet.xlsx hidden sheet", func(t *testing.T) {
		f := openFixture(t, "multisheet.xlsx")
		for sheet, want := range map[string]bool{"Summary": true, "Data 2024": true, "Empty": true, "Hidden": false} {
			got, err := f.GetSheetVisible(sheet)
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Errorf("%s visible = %v, want %v", sheet, got, want)
			}
		}
		rows, err := f.GetRows("Empty")
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 0 {
			t.Errorf("Empty sheet has %d rows, want 0", len(rows))
		}
	})

	t.Run("empty.xlsx has no rows", func(t *testing.T) {
		f := openFixture(t, "empty.xlsx")
		rows, err := f.GetRows("Sheet1")
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 0 {
			t.Errorf("got %d rows, want 0", len(rows))
		}
	})

	t.Run("large.xlsx row count and size", func(t *testing.T) {
		f := openFixture(t, "large.xlsx")
		rows, err := f.GetRows("Sheet1")
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != largeRows+1 {
			t.Errorf("got %d rows, want %d", len(rows), largeRows+1)
		}
		info, err := os.Stat(filepath.Join(testdataDir, "large.xlsx"))
		if err != nil {
			t.Fatal(err)
		}
		if info.Size() > 512*1024 {
			t.Errorf("large.xlsx is %d bytes; keep fixtures small", info.Size())
		}
	})

	t.Run("chart.xlsx contains a chart part", func(t *testing.T) {
		f := openFixture(t, "chart.xlsx")
		found := false
		f.Pkg.Range(func(k, _ any) bool {
			if name, ok := k.(string); ok && strings.HasPrefix(name, "xl/charts/chart") {
				found = true
				return false
			}
			return true
		})
		if !found {
			t.Error("no xl/charts/chart*.xml part found")
		}
	})

	t.Run("macro.xlsm keeps its VBA project", func(t *testing.T) {
		// Read the zip directly, so this checks the file and not excelize.
		parts := zipParts(t, "macro.xlsm")
		want, err := os.ReadFile(filepath.Join(testdataDir, vbaProjectFile))
		if err != nil {
			t.Fatal(err)
		}
		if got, ok := parts["xl/vbaProject.bin"]; !ok || !bytes.Equal(got, want) {
			t.Errorf("xl/vbaProject.bin missing or not identical to testdata/%s", vbaProjectFile)
		}
		checks := []struct{ part, want string }{
			{"[Content_Types].xml", "application/vnd.ms-excel.sheet.macroEnabled.main+xml"},
			{"[Content_Types].xml", "application/vnd.ms-office.vbaProject"},
			{"xl/_rels/workbook.xml.rels", "relationships/vbaProject"},
			{"xl/worksheets/sheet1.xml", `codeName="Sheet1"`},
		}
		for _, c := range checks {
			if !strings.Contains(string(parts[c.part]), c.want) {
				t.Errorf("%s does not contain %q", c.part, c.want)
			}
		}
	})

	t.Run("crlf-bom.csv is written like Excel", func(t *testing.T) {
		data, err := os.ReadFile(filepath.Join(testdataDir, "crlf-bom.csv"))
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != crlfBOMCSV {
			t.Fatal("crlf-bom.csv differs from crlfBOMCSV (line endings changed by git?)")
		}
		body, ok := bytes.CutPrefix(data, []byte(utf8BOM))
		if !ok {
			t.Fatal("no UTF-8 BOM at the start")
		}
		if n := bytes.Count(body, []byte("\r\n")); n != len(csvRows) {
			t.Errorf("%d CRLF row endings, want %d", n, len(csvRows))
		}
		if n := bytes.Count(body, []byte("\n")) - bytes.Count(body, []byte("\r\n")); n != 1 {
			t.Errorf("%d bare LFs, want 1 (the line break inside a cell)", n)
		}
		// Go's csv reader doesn't strip a BOM; the app's CSV loader must.
		got, err := csv.NewReader(bytes.NewReader(body)).ReadAll()
		if err != nil {
			t.Fatal(err)
		}
		if fmt.Sprint(got) != fmt.Sprint(csvRows) {
			t.Errorf("csv = %q, want %q", got, csvRows)
		}
	})

	t.Run("plain.csv parses", func(t *testing.T) {
		data, err := os.ReadFile(filepath.Join(testdataDir, "plain.csv"))
		if err != nil {
			t.Fatal(err)
		}
		got, err := csv.NewReader(strings.NewReader(string(data))).ReadAll()
		if err != nil {
			t.Fatal(err)
		}
		if fmt.Sprint(got) != fmt.Sprint(csvRows) {
			t.Errorf("csv = %q, want %q", got, csvRows)
		}
	})
}

// bordersEqual reports whether two cells have the same borders: the same
// sides, each with the same line style and colour. Order doesn't matter.
func bordersEqual(got, want []excelize.Border) bool {
	if len(got) != len(want) {
		return false
	}
	for _, w := range want {
		found := slices.ContainsFunc(got, func(g excelize.Border) bool {
			return g.Type == w.Type && g.Style == w.Style && strings.EqualFold(g.Color, w.Color)
		})
		if !found {
			return false
		}
	}
	return true
}

func TestBordersEqual(t *testing.T) {
	thin := []excelize.Border{
		{Type: "left", Color: "000000", Style: 1},
		{Type: "top", Color: "000000", Style: 1},
	}
	tests := []struct {
		name string
		got  []excelize.Border
		want bool
	}{
		{"same", []excelize.Border{{Type: "left", Color: "000000", Style: 1}, {Type: "top", Color: "000000", Style: 1}}, true},
		{"other order, colour case", []excelize.Border{{Type: "top", Color: "000000", Style: 1}, {Type: "left", Color: "000000", Style: 1}}, true},
		{"different style", []excelize.Border{{Type: "left", Color: "000000", Style: 2}, {Type: "top", Color: "000000", Style: 1}}, false},
		{"different colour", []excelize.Border{{Type: "left", Color: "FF0000", Style: 1}, {Type: "top", Color: "000000", Style: 1}}, false},
		{"different side", []excelize.Border{{Type: "right", Color: "000000", Style: 1}, {Type: "top", Color: "000000", Style: 1}}, false},
		{"missing side", thin[:1], false},
	}
	for _, tt := range tests {
		if got := bordersEqual(tt.got, thin); got != tt.want {
			t.Errorf("%s: bordersEqual = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// zipParts reads every part of a fixture's zip package, without excelize.
func zipParts(t *testing.T, name string) map[string][]byte {
	t.Helper()
	zr, err := zip.OpenReader(filepath.Join(testdataDir, name))
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

func openFixture(t *testing.T, name string) *excelize.File {
	t.Helper()
	f, err := excelize.OpenFile(filepath.Join(testdataDir, name))
	if err != nil {
		t.Fatalf("open %s: %v", name, err)
	}
	t.Cleanup(func() { closeFile(t, f) })
	return f
}

func closeFile(t *testing.T, f *excelize.File) {
	t.Helper()
	if err := f.Close(); err != nil {
		t.Errorf("close: %v", err)
	}
}

func splitRef(t *testing.T, ref string) (sheet, cell string) {
	t.Helper()
	i := strings.LastIndex(ref, "!")
	if i < 0 {
		t.Fatalf("bad ref %q", ref)
	}
	return ref[:i], ref[i+1:]
}

func sortedKeys[V any](m map[string]V) []string {
	return slices.Sorted(maps.Keys(m))
}
