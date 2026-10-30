package model

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// inputKind is what typed text becomes in an .xlsx cell.
type inputKind int

const (
	inputClear inputKind = iota
	inputText
	inputFormula
	inputBool
	inputNumber
	inputDate
)

// cellInput is parsed typed text. Only the field for its kind is set.
type cellInput struct {
	kind   inputKind
	text   string    // inputText; inputFormula (without the leading "="); inputDate: as typed
	b      bool      // inputBool
	num    float64   // inputNumber
	t      time.Time // inputDate, always UTC
	numFmt string    // inputDate: the number format to use if the cell has no date format
}

// numberPattern is a plain decimal number with an optional exponent: no
// leading "+", no spaces, separators, "%", hex, "Inf" or "NaN".
var numberPattern = regexp.MustCompile(`^-?([0-9]+(\.[0-9]*)?|\.[0-9]+)([eE][+-]?[0-9]+)?$`)

// maxSignificantDigits is Excel's number precision. Longer numbers would be
// rounded, so they stay text.
const maxSignificantDigits = 15

// dateLayouts are the accepted date forms (ISO 8601 order, so they can't be
// misread in any locale), with the number format each one gets.
var dateLayouts = []struct{ layout, numFmt string }{
	{"2006-01-02", "yyyy-mm-dd"},
	{"2006-01-02 15:04", "yyyy-mm-dd hh:mm"},
	{"2006-01-02 15:04:05", "yyyy-mm-dd hh:mm:ss"},
}

// parseInput turns text typed into an .xlsx cell into a value. It never
// loses what was typed: anything that would change if stored as a number
// or date stays text. See "How typed text is stored" in docs/DESIGN.md.
func parseInput(s string) cellInput {
	switch {
	case s == "":
		return cellInput{kind: inputClear}
	case strings.HasPrefix(s, "'"):
		return cellInput{kind: inputText, text: s[1:]}
	case len(s) > 1 && s[0] == '=':
		return cellInput{kind: inputFormula, text: s[1:]}
	case strings.EqualFold(s, "TRUE"):
		return cellInput{kind: inputBool, b: true}
	case strings.EqualFold(s, "FALSE"):
		return cellInput{kind: inputBool, b: false}
	}
	if n, ok := parseNumber(s); ok {
		return cellInput{kind: inputNumber, num: n}
	}
	for _, d := range dateLayouts {
		// UTC, never the local zone: a typed date has no timezone, and a
		// zone offset would shift it by hours.
		t, err := time.ParseInLocation(d.layout, s, time.UTC)
		if err == nil && t.Year() >= 1900 {
			return cellInput{kind: inputDate, text: s, t: t, numFmt: d.numFmt}
		}
	}
	return cellInput{kind: inputText, text: s}
}

// parseNumber reports whether s can be stored as a number without changing
// it: a plain decimal number, no leading zeros, "-0" or "+", at most 15
// significant digits, and in range.
func parseNumber(s string) (float64, bool) {
	if !numberPattern.MatchString(s) {
		return 0, false
	}
	unsigned := strings.TrimPrefix(s, "-")
	mantissa, _, _ := strings.Cut(strings.ToLower(unsigned), "e")
	intPart, frac, _ := strings.Cut(mantissa, ".")
	if len(intPart) > 1 && intPart[0] == '0' { // "007" is a code, not 7
		return 0, false
	}
	digits := strings.TrimLeft(intPart+frac, "0")
	if len(digits) > maxSignificantDigits {
		return 0, false
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsInf(n, 0) {
		return 0, false
	}
	if n == 0 && (strings.HasPrefix(s, "-") || digits != "") {
		// "-0" would come back as "0", and a tiny number like 1e-400
		// underflows to 0.
		return 0, false
	}
	return n, true
}
