package model

import (
	"errors"
	"fmt"
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
func (wb *Workbook) SetInput(sheet string, row, col int, input string) error {
	row, col = wb.EditTarget(sheet, row, col)
	cell, err := wb.editableCell(sheet, row, col)
	if err != nil {
		return err
	}
	restoreGroup, err := wb.unshare(sheet, cell)
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
	if err := wb.apply(sheet, cell, in); err != nil {
		return fmt.Errorf("%s!%s: %w", sheet, cell, err)
	}
	if err := restoreGroup(); err != nil {
		return err
	}
	wb.edited = true
	wb.refreshSheet(sheet)
	return nil
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
	cell, err := wb.editableCell(sheet, row, col)
	if err != nil {
		return CellSnapshot{}, err
	}
	var s CellSnapshot
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
	restoreGroup, err := wb.unshare(sheet, cell)
	if err != nil {
		return err
	}
	f := wb.file
	err = f.SetCellDefault(sheet, cell, "")
	switch {
	case err != nil:
	case s.formula != "":
		err = f.SetCellFormula(sheet, cell, s.formula)
	case s.raw == "":
	case s.typ == excelize.CellTypeBool:
		err = f.SetCellBool(sheet, cell, s.raw == "1" || strings.EqualFold(s.raw, "TRUE"))
	case s.typ == excelize.CellTypeSharedString || s.typ == excelize.CellTypeInlineString:
		err = f.SetCellStr(sheet, cell, s.raw)
	default: // numbers and dates are stored as their raw number
		err = f.SetCellDefault(sheet, cell, s.raw)
	}
	if err == nil {
		err = f.SetCellStyle(sheet, cell, cell, s.style)
	}
	if err != nil {
		return fmt.Errorf("%s!%s: %w", sheet, cell, err)
	}
	if err := restoreGroup(); err != nil {
		return err
	}
	wb.edited = true
	wb.refreshSheet(sheet)
	return nil
}

// editableCell checks an edit is allowed and returns the cell's name.
func (wb *Workbook) editableCell(sheet string, row, col int) (string, error) {
	switch {
	case wb.closed:
		return "", errors.New("workbook is closed")
	case wb.readOnly != "":
		return "", errors.New(wb.readOnly)
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

// apply stores a parsed input in a cell, keeping its style.
func (wb *Workbook) apply(sheet, cell string, in cellInput) error {
	f := wb.file
	switch in.kind {
	case inputClear:
		return f.SetCellDefault(sheet, cell, "")
	case inputText:
		return f.SetCellStr(sheet, cell, in.text)
	case inputFormula:
		// Clear first, so the old value isn't kept as the formula's
		// cached result.
		if err := f.SetCellDefault(sheet, cell, ""); err != nil {
			return err
		}
		return f.SetCellFormula(sheet, cell, in.text)
	case inputBool:
		return f.SetCellBool(sheet, cell, in.b)
	case inputNumber:
		return f.SetCellFloat(sheet, cell, in.num, -1, 64)
	case inputDate:
		return wb.applyDate(sheet, cell, in)
	}
	return fmt.Errorf("unknown input kind %d", in.kind)
}

// applyDate stores a date as Excel's serial number. excelize's own
// SetCellValue(time.Time) always replaces the cell's number format, so the
// serial is computed here and the format set only if the cell has no date
// format yet.
func (wb *Workbook) applyDate(sheet, cell string, in cellInput) error {
	f := wb.file
	id, err := f.GetCellStyle(sheet, cell)
	if err != nil {
		return err
	}
	style, err := f.GetStyle(id)
	if err != nil {
		return err
	}
	if !isDateFormat(style) {
		numFmt := in.numFmt
		style.NumFmt, style.CustomNumFmt = 0, &numFmt
		if id, err = f.NewStyle(style); err != nil {
			return err
		}
		if err := f.SetCellStyle(sheet, cell, cell, id); err != nil {
			return err
		}
	}
	props, err := f.GetWorkbookProps()
	if err != nil {
		return err
	}
	date1904 := props.Date1904 != nil && *props.Date1904
	return f.SetCellFloat(sheet, cell, excelSerial(in.t, date1904), -1, 64)
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
// changed. excelize deletes the formula from every cell of the group when
// the master is overwritten, so this reads each other cell's formula first;
// the returned function sets them back as plain formulas after the edit.
// Their cached values are left as they were.
func (wb *Workbook) unshare(sheet, cell string) (restore func() error, err error) {
	ref, ok := wb.scan[sheet].masters[cell]
	if !ok {
		return func() error { return nil }, nil
	}
	first, last, ok := strings.Cut(ref, ":")
	if !ok {
		last = first
	}
	r1, c1, err := CellIndex(first)
	if err != nil {
		return nil, err
	}
	r2, c2, err := CellIndex(last)
	if err != nil {
		return nil, err
	}
	members := map[string]string{}
	for r := r1; r <= r2; r++ {
		for c := c1; c <= c2; c++ {
			name := CellName(r, c)
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
	}
	return func() error {
		for name, formula := range members {
			if err := wb.file.SetCellFormula(sheet, name, formula); err != nil {
				return fmt.Errorf("%s!%s: %w", sheet, name, err)
			}
		}
		delete(wb.scan[sheet].masters, cell)
		return nil
	}, nil
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
