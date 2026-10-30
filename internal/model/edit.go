package model

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"
)

// ReadOnlyReason says why the workbook can't be edited or saved, or "" if
// it can.
func (wb *Workbook) ReadOnlyReason() string { return wb.readOnly }

// Edited reports whether any cell was changed since the file was opened.
func (wb *Workbook) Edited() bool { return wb.edited }

// EditTarget returns the cell an edit of (row, col) really changes: the
// top-left cell of a merged range it is in, or the cell itself.
func (wb *Workbook) EditTarget(sheet string, row, col int) (int, int) {
	if wb.closed {
		return row, col
	}
	merges, err := wb.file.GetMergeCells(sheet)
	if err != nil {
		return row, col
	}
	for _, m := range merges {
		r1, c1, err1 := CellIndex(m.GetStartAxis())
		r2, c2, err2 := CellIndex(m.GetEndAxis())
		if err1 == nil && err2 == nil && row >= r1 && row <= r2 && col >= c1 && col <= c2 {
			return r1, c1
		}
	}
	return row, col
}

// SetInput sets a cell from typed text (see parseInput). An edit inside a
// merged range goes to its top-left cell. The cell keeps its style, except
// that a date gets a date format if it has none.
//
// Input equal to the cell's EditText changes nothing (so committing the
// formula bar unchanged keeps a formula's cached result, and a number with
// more digits than can be typed) and doesn't mark the workbook edited.
func (wb *Workbook) SetInput(sheet string, row, col int, input string) error {
	row, col = wb.EditTarget(sheet, row, col)
	if _, err := wb.editableCell(sheet, row, col); err != nil {
		return err
	}
	if current, err := wb.EditText(sheet, row, col); err == nil && current == input {
		return nil
	}
	return wb.setInput(sheet, row, col, input)
}

// setInput is SetInput without the unchanged-input shortcut.
func (wb *Workbook) setInput(sheet string, row, col int, input string) error {
	row, col = wb.EditTarget(sheet, row, col)
	cell, err := wb.editableCell(sheet, row, col)
	if err != nil {
		return err
	}
	in := parseInput(input)
	if wb.csv != nil { // a CSV stores exactly what was typed
		in = cellInput{kind: inputText, text: input}
		if input == "" {
			in = cellInput{kind: inputClear}
		}
	}
	if in.kind == inputText {
		if err := checkTextLength(in.text); err != nil {
			return err // refused before anything changed
		}
	}
	return wb.change(sheet, cell, row, func() (bool, error) { return wb.apply(sheet, cell, in) })
}

// change runs an edit of one cell; edit reports whether it wrote to the
// cell. It keeps a shared-formula group intact around it (see unshare),
// even if the edit fails partway. If the edit fails before writing (a
// refused text, a full table of cell formats), the cell is left exactly as
// it was. If it fails after writing, the cell is put back as far as
// restoreCell can (rich-text runs and a formula's cached value aren't in a
// snapshot). The sheet's cached values are dropped and the workbook counts
// as edited either way.
func (wb *Workbook) change(sheet, cell string, row int, edit func() (bool, error)) error {
	before, err := wb.snapshotCell(sheet, cell)
	if err != nil {
		return err
	}
	restoreGroup, err := wb.unshare(sheet, cell)
	if err != nil {
		return err
	}
	wrote, err := edit()
	if err == nil && wb.faultAfterEdit != nil {
		wrote, err = true, wb.faultAfterEdit()
	}
	if err != nil && wrote {
		if _, rerr := wb.restoreCell(sheet, cell, before); rerr != nil {
			err = errors.Join(err, fmt.Errorf("putting the cell back also failed: %w", rerr))
		}
	}
	if gerr := restoreGroup(); gerr != nil {
		err = errors.Join(err, gerr)
	}
	wb.markEdited(sheet, row)
	if err != nil {
		return fmt.Errorf("%s!%s: %w", sheet, cell, err)
	}
	return nil
}

// markEdited records that a cell in row changed: the workbook is edited,
// the sheet's cached values are dropped, and in a CSV the row is no longer
// copied from the file on save.
func (wb *Workbook) markEdited(sheet string, row int) {
	wb.edited = true
	if wb.csv != nil {
		wb.csv.edited[row] = true
	}
	wb.refreshSheet(sheet)
}

// EditText returns the text the formula bar shows for a cell (the edit
// target, as for SetInput): typing it back in recreates the cell.
//
//   - a formula: "=" and the formula
//   - a number: its stored value, not the formatted one ("0.123456", not
//     "0.12")
//   - a date or time (a number with a date format): yyyy-mm-dd, plus hh:mm
//     or hh:mm:ss if it has a time, when that gives back the exact same
//     number; otherwise the number
//   - a boolean: TRUE or FALSE
//   - text: the text, with a leading ' if it would otherwise be read as
//     something else ("'123", "'=x", "'TRUE", "”quoted")
//   - in a CSV: the text as stored
//
// Known limit: a number that needs more than 15 significant digits even in
// its shortest form (like 0.30000000000000004) would come back as text if
// retyped, and so would "-0" for a cell stored as negative zero; SetInput's
// unchanged-input shortcut keeps them as they are when the text isn't
// changed. Callers must not commit the formula bar when
// EditText returns an error: the cell's text isn't known.
func (wb *Workbook) EditText(sheet string, row, col int) (string, error) {
	s, err := wb.Snapshot(sheet, row, col)
	if err != nil {
		return "", err
	}
	switch {
	case s.formula != "":
		return "=" + s.formula, nil
	case wb.csv != nil, s.raw == "":
		return s.raw, nil
	case s.typ == excelize.CellTypeBool:
		if s.raw == "1" || strings.EqualFold(s.raw, "TRUE") {
			return "TRUE", nil
		}
		return "FALSE", nil
	case isTextType(s.typ), s.typ == excelize.CellTypeError:
		return typedText(s.raw), nil
	case s.typ == excelize.CellTypeDate: // ISO date stored as text (t="d")
		if t, ok := parseISODate(s.raw); ok {
			if text, ok := dateText(t); ok {
				return text, nil
			}
		}
		return typedText(s.raw), nil
	}
	return wb.numberText(s.raw, s.style)
}

// typedText returns what to type to get the text s: s itself, or s with a
// leading ' if parseInput would read it as something else.
func typedText(s string) string {
	if in := parseInput(s); in.kind == inputText && in.text == s {
		return s
	}
	return "'" + s
}

// numberText returns the text for a stored number: an ISO date if the
// cell has a date format and the date retypes to the same number, or
// otherwise the number (see plainNumber). If the cell's style can't be
// read, it is treated as having no date format.
func (wb *Workbook) numberText(raw string, styleID int) (string, error) {
	n, perr := strconv.ParseFloat(raw, 64)
	if perr != nil || math.IsNaN(n) || math.IsInf(n, 0) {
		return raw, nil // not a number after all: show it as stored
	}
	style, serr := wb.file.GetStyle(styleID)
	if serr != nil || !isDateFormat(style) {
		return plainNumber(raw, n), nil
	}
	date1904, err := wb.date1904()
	if err != nil {
		return "", err
	}
	text, ok := dateText(serialDate(n, date1904))
	if !ok {
		return plainNumber(raw, n), nil
	}
	// Only use the date if typing it gives back exactly this number.
	in := parseInput(text)
	if in.kind != inputDate || excelSerial(in.t, date1904) != n ||
		(date1904 && in.t.Before(time.Date(1904, 1, 1, 0, 0, 0, 0, time.UTC))) {
		return plainNumber(raw, n), nil
	}
	return text, nil
}

// plainNumber returns the text for the number n, stored as raw: raw if it
// retypes as n, or else n's shortest form if that does (excelize stores
// 1e20 as "100000000000000000000", which has too many digits to type, so
// it shows as "1E+20"). Otherwise raw, which would retype as text.
func plainNumber(raw string, n float64) string {
	if v, ok := parseNumber(raw); ok && v == n {
		return raw
	}
	short := strings.ToUpper(strconv.FormatFloat(n, 'g', -1, 64))
	if v, ok := parseNumber(short); ok && v == n {
		return short
	}
	return raw
}

// dateText formats a date the way parseInput reads it, with as much of the
// time as it has. It fails for a time with fractions of a second.
func dateText(t time.Time) (string, bool) {
	switch {
	case t.Nanosecond() != 0:
		return "", false
	case t.Hour() == 0 && t.Minute() == 0 && t.Second() == 0:
		return t.Format("2006-01-02"), true
	case t.Second() == 0:
		return t.Format("2006-01-02 15:04"), true
	}
	return t.Format("2006-01-02 15:04:05"), true
}

// serialDate converts Excel's serial number to a UTC date, rounded to the
// second: the reverse of excelSerial. Serials before 60 (1 March 1900) use
// the day-later epoch excelSerial uses for them.
func serialDate(n float64, date1904 bool) time.Time {
	var epoch time.Time
	switch {
	case date1904:
		epoch = time.Date(1904, 1, 1, 0, 0, 0, 0, time.UTC)
	case n < 60:
		epoch = time.Date(1899, 12, 31, 0, 0, 0, 0, time.UTC)
	default:
		epoch = time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)
	}
	return time.Unix(epoch.Unix()+int64(math.Round(n*86400)), 0).UTC()
}

// parseISODate reads a date stored as ISO text (t="d").
func parseISODate(raw string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999", "2006-01-02"} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

// date1904 reports whether the workbook counts dates from 1904.
func (wb *Workbook) date1904() (bool, error) {
	props, err := wb.file.GetWorkbookProps()
	if err != nil {
		return false, err
	}
	return props.Date1904 != nil && *props.Date1904, nil
}

// CellSnapshot is a cell's content and style, for undo.
type CellSnapshot struct {
	formula string
	typ     excelize.CellType
	raw     string
	style   int
}

// Snapshot captures a cell (the edit target, as for SetInput) so Restore can
// put it back. A formula's cached result and rich-text runs are not kept.
func (wb *Workbook) Snapshot(sheet string, row, col int) (CellSnapshot, error) {
	row, col = wb.EditTarget(sheet, row, col)
	cell, err := wb.existingCell(sheet, row, col)
	if err != nil {
		return CellSnapshot{}, err
	}
	return wb.snapshotCell(sheet, cell)
}

// snapshotCell is Snapshot for a cell given by name.
func (wb *Workbook) snapshotCell(sheet, cell string) (CellSnapshot, error) {
	var s CellSnapshot
	var err error
	if s.formula, err = wb.file.GetCellFormula(sheet, cell); err != nil {
		return CellSnapshot{}, err
	}
	if s.typ, err = wb.file.GetCellType(sheet, cell); err != nil {
		return CellSnapshot{}, err
	}
	if s.raw, err = wb.file.GetCellValue(sheet, cell, excelize.Options{RawCellValue: true}); err != nil {
		return CellSnapshot{}, err
	}
	if s.style, err = wb.file.GetCellStyle(sheet, cell); err != nil {
		return CellSnapshot{}, err
	}
	return s, nil
}

// Restore puts a cell back the way Snapshot found it.
func (wb *Workbook) Restore(sheet string, row, col int, s CellSnapshot) error {
	row, col = wb.EditTarget(sheet, row, col)
	cell, err := wb.editableCell(sheet, row, col)
	if err != nil {
		return err
	}
	return wb.change(sheet, cell, row, func() (bool, error) { return wb.restoreCell(sheet, cell, s) })
}

// restoreCell puts a snapshot's content and style into a cell. It reports
// whether it wrote to the cell: text that can't be stored exactly is
// refused before anything is written.
func (wb *Workbook) restoreCell(sheet, cell string, s CellSnapshot) (wrote bool, err error) {
	wrote, err = wb.restoreContent(sheet, cell, s)
	if !wrote {
		return false, err
	}
	// The style goes back even if the content failed.
	return true, errors.Join(err, wb.file.SetCellStyle(sheet, cell, cell, s.style))
}

// restoreContent puts a snapshot's content into a cell (see restoreCell).
func (wb *Workbook) restoreContent(sheet, cell string, s CellSnapshot) (wrote bool, err error) {
	f := wb.file
	if s.formula == "" && s.raw != "" {
		// These replace the cell's whole content (formula included) by
		// themselves, so a refused text leaves the cell untouched.
		switch {
		case isTextType(s.typ):
			return wb.setText(sheet, cell, s.raw)
		case s.typ == excelize.CellTypeDate:
			// An ISO date stored as text (t="d") comes back as the same
			// date, stored as Excel's serial number.
			return wb.restoreISODate(sheet, cell, s.raw)
		}
	}
	if err := f.SetCellDefault(sheet, cell, ""); err != nil {
		return true, err
	}
	switch {
	case s.formula != "":
		return true, f.SetCellFormula(sheet, cell, s.formula)
	case s.raw == "":
		return true, nil
	case s.typ == excelize.CellTypeBool:
		return true, f.SetCellBool(sheet, cell, s.raw == "1" || strings.EqualFold(s.raw, "TRUE"))
	}
	// Numbers (and dates stored as numbers) go back as their exact raw
	// value. Known limit: an error value typed as a constant (not from a
	// formula), like #N/A, comes back as text.
	return true, f.SetCellDefault(sheet, cell, s.raw)
}

// isTextType reports whether a cell type holds text when the cell has no
// formula: shared and inline strings, and t="str" (normally a formula's
// text result, but some files use it for plain text).
func isTextType(t excelize.CellType) bool {
	return t == excelize.CellTypeSharedString || t == excelize.CellTypeInlineString || t == excelize.CellTypeFormula
}

// existingCell checks the workbook is open and the cell is on an existing
// sheet, and returns the cell's name.
func (wb *Workbook) existingCell(sheet string, row, col int) (string, error) {
	if wb.closed {
		return "", errors.New("workbook is closed")
	}
	if idx, err := wb.file.GetSheetIndex(sheet); err != nil || idx < 0 {
		return "", fmt.Errorf("no sheet %q", sheet)
	}
	cell := CellName(row, col)
	if cell == "" {
		return "", fmt.Errorf("cell (%d, %d) is outside the sheet", row, col)
	}
	return cell, nil
}

// editableCell checks an edit is allowed and returns the cell's name.
func (wb *Workbook) editableCell(sheet string, row, col int) (string, error) {
	if wb.readOnly != "" && !wb.closed {
		return "", errors.New(wb.readOnly)
	}
	cell, err := wb.existingCell(sheet, row, col)
	if err != nil {
		return "", err
	}
	if ref := wb.lockedRange(sheet, row, col); ref != "" {
		return "", fmt.Errorf("%s is part of the array formula in %s, which can only be changed as a whole", cell, ref)
	}
	return cell, nil
}

// apply stores a parsed input in a cell, keeping its style. It reports
// whether it wrote to the cell.
func (wb *Workbook) apply(sheet, cell string, in cellInput) (wrote bool, err error) {
	f := wb.file
	switch in.kind {
	case inputClear:
		return true, f.SetCellDefault(sheet, cell, "")
	case inputText:
		return wb.setText(sheet, cell, in.text)
	case inputFormula:
		// Clear first, so the old value isn't kept as the formula's
		// cached result.
		if err := f.SetCellDefault(sheet, cell, ""); err != nil {
			return true, err
		}
		return true, f.SetCellFormula(sheet, cell, in.text)
	case inputBool:
		return true, f.SetCellBool(sheet, cell, in.b)
	case inputNumber:
		return true, f.SetCellFloat(sheet, cell, in.num, -1, 64)
	case inputDate:
		return wb.applyDate(sheet, cell, in)
	}
	return false, fmt.Errorf("unknown input kind %d", in.kind)
}

// applyDate stores a date as Excel's serial number. excelize's own
// SetCellValue(time.Time) always replaces the cell's number format, so the
// serial is computed here and the format set only if the cell has no date
// format yet. It reports whether it wrote to the cell.
//
// Known limit: if NewStyle fails (e.g. the workbook's table of cell formats
// is full), the cell is untouched, but excelize may already have added the
// new number format (and a font or fill) to the style sheet. Those unused
// entries are saved with the file; they change nothing anyone sees.
func (wb *Workbook) applyDate(sheet, cell string, in cellInput) (wrote bool, err error) {
	f := wb.file
	date1904, err := wb.date1904()
	if err != nil {
		return false, err
	}
	if date1904 && in.t.Before(time.Date(1904, 1, 1, 0, 0, 0, 0, time.UTC)) {
		// Before this workbook's first date: as a number it would be
		// negative, which Excel shows as ####. Keep what was typed.
		return wb.setText(sheet, cell, in.text)
	}
	id, err := f.GetCellStyle(sheet, cell)
	if err != nil {
		return false, err
	}
	style, err := f.GetStyle(id)
	if err != nil {
		return false, err
	}
	if !isDateFormat(style) {
		numFmt := in.numFmt
		style.NumFmt, style.CustomNumFmt = 0, &numFmt
		if id, err = f.NewStyle(style); err != nil {
			return false, err
		}
		if err := f.SetCellStyle(sheet, cell, cell, id); err != nil {
			return true, err
		}
	}
	return true, f.SetCellFloat(sheet, cell, excelSerial(in.t, date1904), -1, 64)
}

// restoreISODate puts back a date that was stored as ISO text (t="d"), as
// Excel's serial number. If the text isn't a date it goes back as text. It
// reports whether it wrote to the cell.
func (wb *Workbook) restoreISODate(sheet, cell, raw string) (wrote bool, err error) {
	t, ok := parseISODate(raw)
	if !ok {
		return wb.setText(sheet, cell, raw)
	}
	date1904, err := wb.date1904()
	if err != nil {
		return false, err
	}
	return true, wb.file.SetCellFloat(sheet, cell, excelSerial(t, date1904), -1, 64)
}

// ErrTextNotExact is returned when text can't be stored so that it reads
// back exactly as given. The cell is left as it was.
var ErrTextNotExact = errors.New("this text can't be stored exactly")

// maxCellChars is Excel's limit on text in one cell, in UTF-16 units (an
// emoji counts as two).
const maxCellChars = 32767

// setText stores text in a cell exactly (see textStore.store).
func (wb *Workbook) setText(sheet, cell, text string) (wrote bool, err error) {
	return wb.text.store(sheet, cell, text)
}

// textStore stores text in the cells of one excelize file so it reads back
// exactly as given.
//
// excelize keeps one shared-string entry per text and looks a new text up
// in a map before adding an entry. Entries loaded from the file are filed
// under their own stored text, so finding one is always right. But an entry
// added in this session is filed under bstrMarshal(stored text), its own
// escaped form, while the lookup uses the stored text as given. When the
// two differ, a later text can equal an earlier text's key, and excelize
// points the cell at the earlier entry: after "_x0041_" (stored as
// "_x005F_x0041_", filed under "_x005F_x005F_x005F_x0041_"), the text
// "_x005F_x0041_" (stored as "_x005F_x005F_x005F_x0041_") would get that
// entry and read back as "_x0041_".
//
// Only SetCellStr adds to that map, and in MinXLSX only store calls it, so
// store keeps its own copy of every key it may have added (keys) and spots
// such a collision before writing anything.
type textStore struct {
	f *excelize.File
	// keys maps each shared-string key that store may have added in this
	// session to the stored texts that may be filed under it. It can hold
	// more than excelize's map (an entry the lookup found in the file adds
	// no key), never less, so a collision is never missed.
	keys map[string]map[string]bool
}

func newTextStore(f *excelize.File) *textStore {
	return &textStore{f: f, keys: map[string]map[string]bool{}}
}

// store stores text in a cell and reports whether it wrote to the cell.
//
//   - escapeText keeps escape-like text literal and escapes characters XML
//     can't hold.
//   - The length limit is checked on the escaped text, because that is what
//     excelize stores, and it cuts text over the limit short without an
//     error.
//   - If excelize's lookup would find another text's entry, the text is
//     stored as a one-run rich-text entry instead, which skips the lookup.
//     A rich-text run goes through excelize's own escaping, which can't
//     hold every text (control characters, overlapping escape-like text),
//     so that is checked on a scratch file first. Text it can't hold is
//     refused with ErrTextNotExact, before anything is written.
//   - Text containing "_x" is read back after it is stored, as a last
//     check. A mismatch there means this model of excelize is wrong; the
//     edit fails after writing and the caller puts the cell back.
func (ts *textStore) store(sheet, cell, text string) (wrote bool, err error) {
	stored := escapeText(text)
	if err := checkStoredLength(stored); err != nil {
		return false, err
	}
	filed := ts.keys[stored]
	for entry := range filed {
		if entry != stored {
			return ts.storeRich(sheet, cell, text)
		}
	}
	if err := ts.f.SetCellStr(sheet, cell, stored); err != nil {
		return true, err
	}
	if len(filed) == 0 { // the lookup may have missed and added an entry
		key := sharedStringKey(stored)
		if ts.keys[key] == nil {
			ts.keys[key] = map[string]bool{}
		}
		ts.keys[key][stored] = true
	}
	return true, ts.checkReadsBack(sheet, cell, text, stored)
}

// storeRich stores text as a one-run rich-text entry (see store).
func (ts *textStore) storeRich(sheet, cell, text string) (wrote bool, err error) {
	if strings.IndexFunc(text, func(r rune) bool { return !xmlChar(r) }) >= 0 || !richTextReadsBack(text) {
		return false, ErrTextNotExact
	}
	// SetCellRichText keeps a formula; clear the cell first.
	if err := ts.f.SetCellDefault(sheet, cell, ""); err != nil {
		return true, err
	}
	if err := ts.f.SetCellRichText(sheet, cell, []excelize.RichTextRun{{Text: text}}); err != nil {
		return true, err
	}
	return true, ts.checkReadsBack(sheet, cell, text, "_x")
}

// checkReadsBack checks that a cell holds exactly text, if stored contains
// "_x" (other text can't be mixed up).
func (ts *textStore) checkReadsBack(sheet, cell, text, stored string) error {
	if !strings.Contains(stored, "_x") {
		return nil
	}
	got, err := ts.f.GetCellValue(sheet, cell, excelize.Options{RawCellValue: true})
	if err != nil {
		return err
	}
	if got != text {
		return fmt.Errorf("%w (excelize stored it differently)", ErrTextNotExact)
	}
	return nil
}

// richTextReadsBack reports whether text stored as a one-run rich-text
// entry reads back exactly. That depends only on the text (excelize finds
// an existing rich-text entry only if it is identical), so it is checked on
// a scratch file.
func richTextReadsBack(text string) bool {
	x := excelize.NewFile()
	err := x.SetCellRichText("Sheet1", "A1", []excelize.RichTextRun{{Text: text}})
	got, gerr := x.GetCellValue("Sheet1", "A1", excelize.Options{RawCellValue: true})
	cerr := x.Close()
	return err == nil && gerr == nil && cerr == nil && got == text
}

// sharedStringKey is the key excelize v2.11.0 files a new shared string
// under: its bstrMarshal, copied from excelize's lib.go (BSD-3-Clause,
// Copyright 2016 - 2025 The excelize Authors). FuzzSharedStringKey checks
// it against excelize's behaviour; recheck it when upgrading excelize.
func sharedStringKey(s string) (result string) {
	matches, l, cursor := bstrExp.FindAllStringSubmatchIndex(s, -1), len(s), 0
	for _, match := range matches {
		result += s[cursor:match[0]]
		subStr := s[match[0]:match[1]]
		if subStr == "_x005F_" {
			cursor = match[1]
			if match[1]+6 <= l && bstrEscapeExp.MatchString(s[match[1]:match[1]+6]) {
				_, err := strconv.Unquote(`"\u` + s[match[1]+1:match[1]+5] + `"`)
				if err == nil {
					result += subStr + "x005F" + subStr
					continue
				}
			}
			result += subStr + "x005F_"
			continue
		}
		if bstrExp.MatchString(subStr) {
			cursor = match[1]
			if _, err := strconv.Unquote(`"\u` + s[match[0]+2:match[1]-1] + `"`); err == nil {
				result += "_x005F" + subStr
				continue
			}
		}
	}
	if cursor < l {
		result += s[cursor:]
	}
	return result
}

// The patterns sharedStringKey uses, as in excelize.
var (
	bstrExp       = regexp.MustCompile(`_x[a-fA-F\d]{4}_`)
	bstrEscapeExp = regexp.MustCompile(`x[a-fA-F\d]{4}_`)
)

// checkTextLength refuses text that won't fit in a cell once stored.
func checkTextLength(text string) error {
	return checkStoredLength(escapeText(text))
}

func checkStoredLength(stored string) error {
	if n := utf16Len(stored); n > maxCellChars {
		return fmt.Errorf("text too long: a cell holds at most %d characters, and this text takes %d as stored", maxCellChars, n)
	}
	return nil
}

// escapeText escapes text for a cell, using the file format's _xHHHH_
// escapes (one UTF-16 unit each):
//
//   - Every "_" followed by "x" and four hex digits becomes "_x005F_",
//     whatever comes next, so the text stays literal. Checking for the
//     closing "_" isn't enough: in "_x0041\x01" the control character
//     becomes "_x0001_", whose "_" would close the escape. Escaping more
//     than needed is safe, since "_x005F_" always reads back as "_".
//   - Characters XML can't hold (control characters other than tab, line
//     feed and carriage return, U+FFFE and U+FFFF) become _xHHHH_, as the
//     spec requires; written raw they would turn into U+FFFD on save.
//
// The result reads back as s: an escape in it can only start at an
// escape this function wrote, because every other "_" is followed by
// something other than "x" and four hex digits.
func escapeText(s string) string {
	var b strings.Builder
	for i, r := range s {
		switch {
		case escapePrefix(s[i:]):
			b.WriteString("_x005F_")
		case !xmlChar(r):
			fmt.Fprintf(&b, "_x%04X_", r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// xmlChar reports whether XML 1.0 can hold r. Surrogates can't occur in a
// Go string's runes, so only control characters and U+FFFE/U+FFFF are out.
func xmlChar(r rune) bool {
	switch {
	case r == '\t', r == '\n', r == '\r':
		return true
	case r < 0x20, r == 0xFFFE, r == 0xFFFF:
		return false
	}
	return true
}

// escapePrefix reports whether s starts with "_x" and four hex digits.
func escapePrefix(s string) bool {
	if len(s) < 6 || s[0] != '_' || s[1] != 'x' {
		return false
	}
	for _, c := range s[2:6] {
		if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
			return false
		}
	}
	return true
}

func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		n++
		if r > 0xFFFF {
			n++
		}
	}
	return n
}

// excelSerial converts a UTC date to Excel's serial number: days since the
// epoch, with the time of day as a fraction. In the 1900 date system Excel
// counts a 29 February 1900 that never existed, so dates from March 1900 on
// are one day further along.
func excelSerial(t time.Time, date1904 bool) float64 {
	var epoch time.Time
	switch {
	case date1904:
		epoch = time.Date(1904, 1, 1, 0, 0, 0, 0, time.UTC)
	case t.Before(time.Date(1900, 3, 1, 0, 0, 0, 0, time.UTC)):
		epoch = time.Date(1899, 12, 31, 0, 0, 0, 0, time.UTC)
	default:
		epoch = time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)
	}
	// Unix seconds, not t.Sub: a time.Duration overflows after 292 years.
	return float64(t.Unix()-epoch.Unix()) / 86400
}

// isDateFormat reports whether a style's number format shows a date or time.
func isDateFormat(s *excelize.Style) bool {
	if s.CustomNumFmt != nil {
		return customFormatIsDate(*s.CustomNumFmt)
	}
	switch id := s.NumFmt; {
	case id >= 14 && id <= 22, id >= 27 && id <= 36, id >= 45 && id <= 47, id >= 50 && id <= 58:
		return true
	}
	return false
}

// customFormatIsDate looks for date/time codes (y, m, d, h, s) outside
// quoted text, escaped characters and [colour]/[condition] blocks.
func customFormatIsDate(format string) bool {
	inQuote, inBracket, escaped := false, false, false
	for _, r := range strings.ToLower(format) {
		switch {
		case escaped:
			escaped = false
		case inQuote:
			inQuote = r != '"'
		case inBracket:
			inBracket = r != ']'
		case r == '"':
			inQuote = true
		case r == '[':
			inBracket = true
		case r == '\\':
			escaped = true
		case strings.ContainsRune("ymdhs", r):
			return true
		}
	}
	return false
}

// unshare protects a shared formula group before its master cell is
// changed. excelize deletes the formula from every cell with the master's
// group id when the master is overwritten, so this reads each of those
// cells' formulas first; the returned function sets them back as plain
// formulas after the edit. Their cached values are left as they were.
func (wb *Workbook) unshare(sheet, cell string) (restore func() error, err error) {
	s := wb.scan[sheet]
	ref, ok := s.masters[cell]
	if !ok {
		return func() error { return nil }, nil
	}
	cells := s.groups[s.masterGroup[cell]]
	if len(cells) == 0 { // no group id recorded: fall back to the range
		cells, err = cellsIn(ref)
		if err != nil {
			return nil, err
		}
	}
	members := map[string]string{}
	for _, name := range cells {
		if name == cell {
			continue
		}
		formula, err := wb.file.GetCellFormula(sheet, name)
		if err != nil {
			return nil, err
		}
		if formula != "" {
			members[name] = formula
		}
	}
	return func() error {
		for name, formula := range members {
			if err := wb.file.SetCellFormula(sheet, name, formula); err != nil {
				return fmt.Errorf("%s!%s: %w", sheet, name, err)
			}
		}
		delete(s.groups, s.masterGroup[cell])
		delete(s.masterGroup, cell)
		delete(s.masters, cell)
		return nil
	}, nil
}

// parseRange turns "A1:B3" (or a single cell) into 0-based corners.
func parseRange(ref string) (r1, c1, r2, c2 int, err error) {
	first, last, ok := strings.Cut(ref, ":")
	if !ok {
		last = first
	}
	if r1, c1, err = CellIndex(first); err != nil {
		return 0, 0, 0, 0, err
	}
	if r2, c2, err = CellIndex(last); err != nil {
		return 0, 0, 0, 0, err
	}
	return r1, c1, r2, c2, nil
}

// cellsIn lists the cells of a range.
func cellsIn(ref string) ([]string, error) {
	r1, c1, r2, c2, err := parseRange(ref)
	if err != nil {
		return nil, err
	}
	var cells []string
	for r := r1; r <= r2; r++ {
		for c := c1; c <= c2; c++ {
			cells = append(cells, CellName(r, c))
		}
	}
	return cells, nil
}

// lockedRange returns the array or data-table formula range a cell is in,
// or "".
func (wb *Workbook) lockedRange(sheet string, row, col int) string {
	for _, ref := range wb.scan[sheet].locked {
		r1, c1, r2, c2, err := parseRange(ref)
		if err == nil && row >= r1 && row <= r2 && col >= c1 && col <= c2 {
			return ref
		}
	}
	return ""
}

// refreshSheet drops a sheet's cached display values after an edit, so the
// next read gets them fresh from GetRows.
//
// It deliberately doesn't refresh just the edited cell with GetCellValue:
// in excelize v2.11.0 a formatted GetCellValue rounds the cell's *stored*
// number to 15 significant digits (getValueFrom assigns c.V), and the
// rounded value would then be saved. GetRows formats a copy and is safe.
// Never call GetCellValue without RawCellValue on a workbook that may be
// saved.
func (wb *Workbook) refreshSheet(sheet string) {
	delete(wb.sheets, sheet)
}
