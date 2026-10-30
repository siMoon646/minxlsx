package model

import (
	"testing"
	"unicode/utf8"

	"github.com/xuri/excelize/v2"
)

// FuzzSetText: any text typed into a cell (as '-prefixed text, so it isn't
// read as a number or formula) reads back exactly, in MinXLSX and after a
// save. Two texts go into the same workbook, before and after a reopen, so
// excelize's shared-string table can mix them up, both with entries made in
// this session and with entries loaded from the file.
func FuzzSetText(f *testing.F) {
	seeds := [][2]string{
		{escapeThenControl, ""},
		{"_x0041\x01", "_x005F_x0041_x0001_"},
		{"_x0041_", "_x005F_x0041_"},
		{"_x005F_x0041_", "_x0041_"},
		{"_x0041_x0042_", "__x0041_"},
		{"a_x0041_x0042_", "a_x005F_x0041_x0042_"},
		{"\x01", "_x0001_"},
		{"_x0001_", "\x01"},
		{"_x\x01", "x0041_\x01_x005F"},
		{"cr\r\n", "￾￿\x00"},
	}
	for _, s := range seeds {
		f.Add(s[0], s[1])
	}
	f.Fuzz(func(t *testing.T, a, b string) {
		if !utf8.ValidString(a) || !utf8.ValidString(b) ||
			checkTextLength(a) != nil || checkTextLength(b) != nil {
			t.Skip()
		}
		wb := newWorkbook(t, func(*excelize.File) {})
		want := []string{a, b, a}
		for row, text := range want {
			if err := wb.SetInput("Sheet1", row, 0, "'"+text); err != nil {
				t.Fatalf("SetInput(%q): %v", text, err)
			}
		}
		check := func(w *Workbook, when string) {
			t.Helper()
			for row, text := range want {
				if got := w.Value("Sheet1", row, 0); got != text {
					t.Fatalf("%s: row %d shows %q, want %q", when, row+1, got, text)
				}
			}
		}
		check(wb, "in session")
		saved := reopen(t, wb)
		check(saved, "after save")
		want = append(want, b, a)
		for row := 3; row < len(want); row++ {
			if err := saved.SetInput("Sheet1", row, 0, "'"+want[row]); err != nil {
				t.Fatalf("after reopen: SetInput(%q): %v", want[row], err)
			}
		}
		check(saved, "after reopen")
		check(reopen(t, saved), "after second save")
	})
}
