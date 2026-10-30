package model

import (
	"testing"
	"time"
)

func TestParseInput(t *testing.T) {
	date := func(y int, mo time.Month, d, h, mi, s int) time.Time {
		return time.Date(y, mo, d, h, mi, s, 0, time.UTC)
	}
	tests := []struct {
		in   string
		want cellInput
	}{
		// Clearing and forced text.
		{"", cellInput{kind: inputClear}},
		{"'007", cellInput{kind: inputText, text: "007"}},
		{"'=A1", cellInput{kind: inputText, text: "=A1"}},
		{"'", cellInput{kind: inputText, text: ""}},

		// Formulas.
		{"=SUM(A1:A3)", cellInput{kind: inputFormula, text: "SUM(A1:A3)"}},
		{"=", cellInput{kind: inputText, text: "="}},

		// Booleans.
		{"TRUE", cellInput{kind: inputBool, b: true}},
		{"false", cellInput{kind: inputBool, b: false}},
		{"True ", cellInput{kind: inputText, text: "True "}},

		// Numbers.
		{"0", cellInput{kind: inputNumber, num: 0}},
		{"-3", cellInput{kind: inputNumber, num: -3}},
		{"1234.5", cellInput{kind: inputNumber, num: 1234.5}},
		{".5", cellInput{kind: inputNumber, num: 0.5}},
		{"0.25", cellInput{kind: inputNumber, num: 0.25}},
		{"1e5", cellInput{kind: inputNumber, num: 1e5}},
		{"-2.5E-3", cellInput{kind: inputNumber, num: -2.5e-3}},
		{"123456789012345", cellInput{kind: inputNumber, num: 123456789012345}},

		// Numbers that would change if stored as numbers stay text.
		{"007", cellInput{kind: inputText, text: "007"}},
		{"-007", cellInput{kind: inputText, text: "-007"}},
		{"00", cellInput{kind: inputText, text: "00"}},
		{"-0", cellInput{kind: inputText, text: "-0"}},
		{"+5", cellInput{kind: inputText, text: "+5"}},
		{"1234567890123456", cellInput{kind: inputText, text: "1234567890123456"}}, // 16 digits
		{"0.1234567890123456", cellInput{kind: inputText, text: "0.1234567890123456"}},
		{"1e400", cellInput{kind: inputText, text: "1e400"}},
		{" 5", cellInput{kind: inputText, text: " 5"}},
		{"1,234", cellInput{kind: inputText, text: "1,234"}},
		{"12%", cellInput{kind: inputText, text: "12%"}},
		{"Inf", cellInput{kind: inputText, text: "Inf"}},
		{"NaN", cellInput{kind: inputText, text: "NaN"}},
		{"0x10", cellInput{kind: inputText, text: "0x10"}},
		{"1_000", cellInput{kind: inputText, text: "1_000"}},
		{"1e-400", cellInput{kind: inputText, text: "1e-400"}}, // underflows to 0
		{"0.0", cellInput{kind: inputNumber, num: 0}},

		// Dates, always UTC.
		{"2024-03-15", cellInput{kind: inputDate, text: "2024-03-15", t: date(2024, 3, 15, 0, 0, 0), numFmt: "yyyy-mm-dd"}},
		{"2024-03-15 13:45", cellInput{kind: inputDate, text: "2024-03-15 13:45", t: date(2024, 3, 15, 13, 45, 0), numFmt: "yyyy-mm-dd hh:mm"}},
		{"2024-03-15 13:45:30", cellInput{kind: inputDate, text: "2024-03-15 13:45:30", t: date(2024, 3, 15, 13, 45, 30), numFmt: "yyyy-mm-dd hh:mm:ss"}},
		{"9999-12-31", cellInput{kind: inputDate, text: "9999-12-31", t: date(9999, 12, 31, 0, 0, 0), numFmt: "yyyy-mm-dd"}},
		{"1899-12-31", cellInput{kind: inputText, text: "1899-12-31"}}, // before Excel's first date
		{"2024-02-30", cellInput{kind: inputText, text: "2024-02-30"}},
		{"15/03/2024", cellInput{kind: inputText, text: "15/03/2024"}}, // day/month order depends on locale
		{"2024-3-15", cellInput{kind: inputText, text: "2024-3-15"}},

		// Everything else.
		{"hello", cellInput{kind: inputText, text: "hello"}},
		{"  spaces  ", cellInput{kind: inputText, text: "  spaces  "}},
		{"line one\nline two", cellInput{kind: inputText, text: "line one\nline two"}},
	}
	for _, tt := range tests {
		got := parseInput(tt.in)
		if got != tt.want {
			t.Errorf("parseInput(%q) = %+v, want %+v", tt.in, got, tt.want)
		}
	}
}

// TestParseInputDatesIgnoreLocalZone: the local timezone must never shift a
// typed date.
func TestParseInputDatesIgnoreLocalZone(t *testing.T) {
	auckland, err := time.LoadLocation("Pacific/Auckland")
	if err != nil {
		t.Skipf("no timezone data: %v", err)
	}
	old := time.Local
	time.Local = auckland
	t.Cleanup(func() { time.Local = old })

	got := parseInput("2024-03-15 13:45")
	want := time.Date(2024, 3, 15, 13, 45, 0, 0, time.UTC)
	if !got.t.Equal(want) || got.t.Location() != time.UTC {
		t.Errorf("parsed %v, want %v", got.t, want)
	}
}
