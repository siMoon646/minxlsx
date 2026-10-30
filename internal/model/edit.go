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
	cell, err := wb.existingCell(sheet, row, col)
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
		err = wb.setText(sheet, cell, s.raw)
	case s.typ == excelize.CellTypeDate:
		// An ISO date stored as text (t="d") comes back as the same date,
		// stored as Excel's serial number.
		err = wb.restoreISODate(sheet, cell, s.raw)
	default:
		// Numbers (and dates stored as numbers) go back as their exact raw
		// value. Known limit: an error value typed as a constant (not from
		// a formula), like #N/A, comes back as text.
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

// apply stores a parsed input in a cell, keeping its style.
func (wb *Workbook) apply(sheet, cell string, in cellInput) error {
	f := wb.file
	switch in.kind {
	case inputClear:
		return f.SetCellDefault(sheet, cell, "")
	case inputText:
		return wb.setText(sheet, cell, in.text)
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
	props, err := f.GetWorkbookProps()
	if err != nil {
		return err
	}
	date1904 := props.Date1904 != nil && *props.Date1904
	if date1904 && in.t.Before(time.Date(1904, 1, 1, 0, 0, 0, 0, time.UTC)) {
		// Before this workbook's first date: as a number it would be
		// negative, which Excel shows as ####. Keep what was typed.
		return wb.setText(sheet, cell, in.text)
	}
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
	return f.SetCellFloat(sheet, cell, excelSerial(in.t, date1904), -1, 64)
}

// restoreISODate puts back a date that was stored as ISO text (t="d"), as
// Excel's serial number. If the text isn't a date it goes back as text.
func (wb *Workbook) restoreISODate(sheet, cell, raw string) error {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999", "2006-01-02"} {
		t, err := time.Parse(layout, raw)
		if err != nil {
			continue
		}
		props, err := wb.file.GetWorkbookProps()
		if err != nil {
			return err
		}
		date1904 := props.Date1904 != nil && *props.Date1904
		return wb.file.SetCellFloat(sheet, cell, excelSerial(t.UTC(), date1904), -1, 64)
	}
	return wb.setText(sheet, cell, raw)
}

// maxCellChars is Excel's limit on text in one cell, in UTF-16 units (an
// emoji counts as two).
const maxCellChars = 32767

// setText stores text exactly. excelize writes text as it is, so typed text
// like "_x0041_" would be read back (by Excel and by excelize) as "A"; the
// leading underscore is escaped as "_x005F_" to keep it literal. Text over
// Excel's limit is refused rather than cut short.
func (wb *Workbook) setText(sheet, cell, text string) error {
	if n := utf16Len(text); n > maxCellChars {
		return fmt.Errorf("text is %d characters long; a cell holds at most %d", n, maxCellChars)
	}
	return wb.file.SetCellStr(sheet, cell, escapeText(text))
}

// escapeText escapes every "_" that starts an escape-like sequence
// (_xHHHH_, which the file format reads as one character) as "_x005F_".
// Matches can overlap: in "_x0041_x0042_" the second one starts at the
// first one's last "_", so every position is checked, not just those after
// a previous match.
func escapeText(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if startsEscape(s[i:]) {
			b.WriteString("_x005F_")
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// startsEscape reports whether s starts with _xHHHH_.
func startsEscape(s string) bool {
	if len(s) < 7 || s[0] != '_' || s[1] != 'x' || s[6] != '_' {
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
