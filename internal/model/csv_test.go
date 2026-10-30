package model

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

func TestParseCSV(t *testing.T) {
	rec := func(raw, eol string, fields ...string) csvRecord {
		return csvRecord{raw: raw, eol: eol, fields: fields}
	}
	tests := []struct {
		name string
		in   string
		want []csvRecord
	}{
		{"simple", "a,b\nc,d\n", []csvRecord{rec("a,b", "\n", "a", "b"), rec("c,d", "\n", "c", "d")}},
		{"crlf", "a,b\r\nc\r\n", []csvRecord{rec("a,b", "\r\n", "a", "b"), rec("c", "\r\n", "c")}},
		{"no final newline", "a\nb", []csvRecord{rec("a", "\n", "a"), rec("b", "", "b")}},
		{"quoted comma", `"a,b",c` + "\n", []csvRecord{rec(`"a,b",c`, "\n", "a,b", "c")}},
		{"escaped quotes", `"say ""hi"""` + "\n", []csvRecord{rec(`"say ""hi"""`, "\n", `say "hi"`)}},
		{"line break in a cell", "\"x\ny\",z\r\n", []csvRecord{rec("\"x\ny\",z", "\r\n", "x\ny", "z")}},
		{"crlf in a cell", "\"x\r\ny\"\n", []csvRecord{rec("\"x\r\ny\"", "\n", "x\r\ny")}},
		{"blank line", "a\n\nb\n", []csvRecord{rec("a", "\n", "a"), rec("", "\n", ""), rec("b", "\n", "b")}},
		{"trailing empty fields", "a,,\n", []csvRecord{rec("a,,", "\n", "a", "", "")}},
		{"mixed line endings", "a\r\nb\nc", []csvRecord{rec("a", "\r\n", "a"), rec("b", "\n", "b"), rec("c", "", "c")}},
		{"lone CR is data", "a\rb\n", []csvRecord{rec("a\rb", "\n", "a\rb")}},
		{"unclosed quote runs to the end", "\"abc\n", []csvRecord{rec("\"abc\n", "", "abc\n")}},
		{"text after a closing quote", `"ab"c,d` + "\n", []csvRecord{rec(`"ab"c,d`, "\n", "abc", "d")}},
		{"empty input", "", nil},
	}
	for _, tt := range tests {
		got := parseCSV(tt.in)
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s: parseCSV(%q) =\n %+v\nwant\n %+v", tt.name, tt.in, got, tt.want)
		}
	}
}

func TestEncodeCSVField(t *testing.T) {
	tests := map[string]string{
		"":         "",
		"plain":    "plain",
		"a,b":      `"a,b"`,
		`say "hi"`: `"say ""hi"""`,
		"x\ny":     "\"x\ny\"",
		" lead":    `" lead"`,
		`\.`:       `"\."`,
		"日本語":      "日本語",
	}
	for in, want := range tests {
		if got := encodeCSVField(in); got != want {
			t.Errorf("encodeCSVField(%q) = %q, want %q", in, got, want)
		}
	}
}

// writeCSVFile writes content to a temp .csv file.
func writeCSVFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func openCSVAt(t *testing.T, path string) *Workbook {
	t.Helper()
	wb, err := Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	t.Cleanup(func() { _ = wb.Close() })
	return wb
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestCSVSavesByteIdentical: an unedited CSV saves back exactly, whatever
// its quoting, line endings, BOM, blank lines or trailing fields.
func TestCSVSavesByteIdentical(t *testing.T) {
	cases := map[string]string{
		"plain.csv":    readFile(t, filepath.Join(testdataDir, "plain.csv")),
		"crlf-bom.csv": readFile(t, filepath.Join(testdataDir, "crlf-bom.csv")),
		"messy.csv": "\"quoted\",unneeded,\" lead\", trail \r\n" +
			"\n" + // blank line
			"a,,,\n" + // trailing empty fields
			",,\n" + // only commas
			"\"multi\r\nline\",x\r\n" +
			"last,row", // no final newline
		"blank-lines-at-end.csv": "a,b\n\n\n",
		"one-field.csv":          "x",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			path := writeCSVFile(t, name, content)
			wb := openCSVAt(t, path)
			readLikeUI(t, wb)
			if err := wb.Save(); err != nil {
				t.Fatal(err)
			}
			if got := readFile(t, path); got != content {
				t.Errorf("saved bytes differ:\n got %q\nwant %q", got, content)
			}
		})
	}
}

func TestCSVValuesAreText(t *testing.T) {
	path := writeCSVFile(t, "values.csv", "007,1.50,=A1,TRUE,2024-03-15\n")
	wb := openCSVAt(t, path)
	want := []string{"007", "1.50", "=A1", "TRUE", "2024-03-15"}
	sheet := wb.Sheets()[0]
	for c, w := range want {
		if got := wb.Value(sheet, 0, c); got != w {
			t.Errorf("col %d shows %q, want %q", c, got, w)
		}
		if got, err := wb.EditText(sheet, 0, c); err != nil || got != w {
			t.Errorf("col %d formula bar %q (%v), want %q", c, got, err, w)
		}
	}
	// Typed input is stored exactly as typed in a CSV.
	setInput(t, wb, sheet, "A2", "=B1*2")
	setInput(t, wb, sheet, "B2", "0042")
	if got := wb.Value(sheet, 1, 0); got != "=B1*2" {
		t.Errorf("typed formula in a CSV shows %q, want the text", got)
	}
	if got := wb.Value(sheet, 1, 1); got != "0042" {
		t.Errorf("typed number shows %q", got)
	}
}

func TestCSVEditChangesOnlyThatRow(t *testing.T) {
	content := readFile(t, filepath.Join(testdataDir, "crlf-bom.csv"))
	path := writeCSVFile(t, "edit.csv", content)
	wb := openCSVAt(t, path)
	sheet := wb.Sheets()[0]
	setInput(t, wb, sheet, "C3", `now "quoted", with comma`) // Banana's note
	if err := wb.Save(); err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(content, "Banana,25,\"has, comma\"\r\n",
		"Banana,25,\"now \"\"quoted\"\", with comma\"\r\n", 1)
	if got := readFile(t, path); got != want {
		t.Errorf("saved:\n got %q\nwant %q", got, want)
	}
}

func TestCSVEditPastTheEnd(t *testing.T) {
	path := writeCSVFile(t, "grow.csv", "a,b\r\nc,d") // no final newline
	wb := openCSVAt(t, path)
	sheet := wb.Sheets()[0]
	setInput(t, wb, sheet, "B4", "new")
	setInput(t, wb, sheet, "A1", "") // clear a field
	if err := wb.Save(); err != nil {
		t.Fatal(err)
	}
	want := ",b\r\nc,d\r\n\r\n,new"
	if got := readFile(t, path); got != want {
		t.Errorf("saved %q, want %q", got, want)
	}
}

func TestCSVOpenErrors(t *testing.T) {
	latin1 := writeCSVFile(t, "latin1.csv", "K\xf6ln,1\n")
	if _, err := Open(latin1); err == nil || !strings.Contains(err.Error(), "UTF-8") {
		t.Errorf("non-UTF-8 CSV: err = %v, want a UTF-8 error", err)
	}
	wide := writeCSVFile(t, "wide.csv", strings.Repeat("x,", maxCols)+"x\n")
	if _, err := Open(wide); err == nil {
		t.Error("CSV wider than Excel allows: want error")
	}
}

func TestCSVSheetName(t *testing.T) {
	tests := map[string]string{
		"sales 2024.csv":                 "sales 2024",
		"a[b]c?.csv":                     "a_b_c_",
		"'quoted'.csv":                   "quoted",
		strings.Repeat("x", 40) + ".csv": strings.Repeat("x", 31),
		".csv":                           "Sheet1",
	}
	for file, want := range tests {
		if got := csvSheetName(filepath.Join("dir", file)); got != want {
			t.Errorf("csvSheetName(%q) = %q, want %q", file, got, want)
		}
	}
}

func TestCSVSaveAs(t *testing.T) {
	path := writeCSVFile(t, "data.csv", "007,x\n")
	wb := openCSVAt(t, path)
	if got := wb.SaveAsTypes(); strings.Join(got, ",") != ".csv,.xlsx" {
		t.Errorf("SaveAsTypes = %q", got)
	}
	if err := wb.SaveAs(filepath.Join(t.TempDir(), "data.xlsm")); err == nil {
		t.Error("CSV → .xlsm: want error")
	}

	xlsx := filepath.Join(t.TempDir(), "data.xlsx")
	if err := wb.SaveAs(xlsx); err != nil {
		t.Fatal(err)
	}
	f := openRaw(t, xlsx)
	if got, err := f.GetCellValue(f.GetSheetList()[0], "A1"); err != nil || got != "007" {
		t.Errorf("A1 in the .xlsx = %q (%v), want text 007", got, err)
	}
	// Now it is an .xlsx workbook: typed input is parsed again.
	if got := wb.SaveAsTypes(); strings.Join(got, ",") != ".xlsx" {
		t.Errorf("after Save As .xlsx, SaveAsTypes = %q", got)
	}
	setInput(t, wb, wb.Sheets()[0], "B1", "42")
	if err := wb.Save(); err != nil {
		t.Fatal(err)
	}
	f2 := openRaw(t, xlsx)
	if typ, err := f2.GetCellType(f2.GetSheetList()[0], "B1"); err != nil || typ == excelize.CellTypeSharedString {
		t.Errorf("B1 typed 42 after converting to .xlsx: type %v (%v), want a number", typ, err)
	}
}

func TestCSVSnapshotRestore(t *testing.T) {
	path := writeCSVFile(t, "undo.csv", "a,b\n")
	wb := openCSVAt(t, path)
	sheet := wb.Sheets()[0]
	snap, err := wb.Snapshot(sheet, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	setInput(t, wb, sheet, "B1", "changed")
	if err := wb.Restore(sheet, 0, 1, snap); err != nil {
		t.Fatal(err)
	}
	if err := wb.Save(); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); got != "a,b\n" {
		t.Errorf("after undo and save: %q", got)
	}
}
