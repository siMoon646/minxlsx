package model

import (
	"errors"
	"testing"
	"unicode/utf8"

	"github.com/xuri/excelize/v2"
)

// FuzzSetText: any text typed into a cell (as '-prefixed text, so it isn't
// read as a number or formula) reads back exactly, in MinXLSX and after a
// save, or is refused with ErrTextNotExact and the cell left as it was. Two
// texts go into the same workbook, before and after a reopen, so excelize's
// shared-string table can mix them up, both with entries made in this
// session and with entries loaded from the file.
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
		collidingPair,
	}
	for _, s := range seeds {
		f.Add(s[0], s[1])
	}
	f.Fuzz(func(t *testing.T, a, b string) {
		if !utf8.ValidString(a) || !utf8.ValidString(b) ||
			checkTextLength(a) != nil || checkTextLength(b) != nil {
			t.Skip()
		}
		var want []string // what each row should show; "" for a refused edit
		set := func(w *Workbook, text string) {
			t.Helper()
			row := len(want)
			err := w.SetInput("Sheet1", row, 0, "'"+text)
			switch {
			case err == nil:
				want = append(want, text)
			case errors.Is(err, ErrTextNotExact):
				want = append(want, "") // the cell was empty
			default:
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
		wb := newWorkbook(t, func(*excelize.File) {})
		for _, text := range []string{a, b, a} {
			set(wb, text)
		}
		check(wb, "in session")
		saved := reopen(t, wb)
		check(saved, "after save")
		set(saved, b)
		set(saved, a)
		check(saved, "after reopen")
		check(reopen(t, saved), "after second save")
	})
}

// FuzzSharedStringKey: sharedStringKey is the key excelize files a new
// shared string under. Checked by excelize's own behaviour: after storing
// v, storing its key (when that differs from v) gets v's entry.
func FuzzSharedStringKey(f *testing.F) {
	for _, s := range []string{
		"plain", "_x0041_", "_x005F_x0041_", "_x005F_", "_x005F_x", "_x005F_xD800_",
		"_xD800_", "_x0041_x0042_", "_x0001_x0002_", "a_x00e9_b_X0041_", "_x005f_x0041_",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, v string) {
		key := sharedStringKey(v)
		if key == v || !utf8.ValidString(v) || len(v) > 1000 {
			t.Skip()
		}
		x := excelize.NewFile()
		defer func() { _ = x.Close() }()
		if err := x.SetCellStr("Sheet1", "A1", v); err != nil {
			t.Fatal(err)
		}
		if err := x.SetCellStr("Sheet1", "A2", key); err != nil {
			t.Fatal(err)
		}
		a1, err := x.GetCellValue("Sheet1", "A1", excelize.Options{RawCellValue: true})
		if err != nil {
			t.Fatal(err)
		}
		a2, err := x.GetCellValue("Sheet1", "A2", excelize.Options{RawCellValue: true})
		if err != nil {
			t.Fatal(err)
		}
		if a1 != a2 {
			t.Errorf("v %q, key %q: A2 got its own entry (%q, not %q), so the key is wrong", v, key, a2, a1)
		}
	})
}
