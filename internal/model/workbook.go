package model

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/xuri/excelize/v2"
)

// Excel's sheet limits (0-based maximums are one less).
const (
	maxRows = 1048576
	maxCols = 16384
)

// Workbook is an open .xlsx or .xlsm file. For now it is read-only.
//
// Rows and columns are 0-based everywhere in this API; A1 is (0, 0).
// A Workbook is not safe for concurrent use; the UI calls it from its own
// goroutine only.
type Workbook struct {
	path   string
	file   *excelize.File
	rows   map[string][][]string // sheet -> display values, loaded on first use
	closed bool
}

// Open opens an .xlsx or .xlsm file. Other extensions (including .csv, which
// comes in milestone 3) are rejected.
func Open(path string) (*Workbook, error) {
	switch ext := strings.ToLower(filepath.Ext(path)); ext {
	case ".xlsx", ".xlsm":
	case "":
		return nil, fmt.Errorf("open %s: unsupported file type (want .xlsx or .xlsm)", path)
	default:
		return nil, fmt.Errorf("open %s: unsupported file type %s (want .xlsx or .xlsm)", path, ext)
	}
	f, err := excelize.OpenFile(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	return &Workbook{path: path, file: f, rows: map[string][][]string{}}, nil
}

// Close releases the file and any temp files excelize made for it. Calling
// it again is a no-op.
func (wb *Workbook) Close() error {
	if wb == nil || wb.closed {
		return nil
	}
	wb.closed = true
	wb.rows = nil
	if err := wb.file.Close(); err != nil {
		return fmt.Errorf("close %s: %w", wb.path, err)
	}
	return nil
}

// Path returns the path the workbook was opened from.
func (wb *Workbook) Path() string { return wb.path }

// Sheets returns the visible sheets in workbook order. Hidden sheets are
// left out but stay in the file.
func (wb *Workbook) Sheets() []string {
	var out []string
	for _, name := range wb.file.GetSheetList() {
		if visible, err := wb.file.GetSheetVisible(name); err == nil && visible {
			out = append(out, name)
		}
	}
	return out
}

// Size returns the sheet's data extent: the number of rows, and the length
// of the longest row.
func (wb *Workbook) Size(sheet string) (rows, cols int, err error) {
	data, err := wb.load(sheet)
	if err != nil {
		return 0, 0, err
	}
	for _, r := range data {
		cols = max(cols, len(r))
	}
	return len(data), cols, nil
}

// Value returns the cell's display value (with number formats applied), as
// the grid shows it. Cells outside the data, negative indexes and unknown
// sheets all give "".
func (wb *Workbook) Value(sheet string, row, col int) string {
	data, err := wb.load(sheet)
	if err != nil || row < 0 || col < 0 || row >= len(data) {
		return ""
	}
	// GetRows drops trailing empty cells, so rows have different lengths.
	if r := data[row]; col < len(r) {
		return r[col]
	}
	return ""
}

// Formula returns what the formula bar shows: "=" plus the formula if the
// cell has one, otherwise the cell's display value.
func (wb *Workbook) Formula(sheet string, row, col int) (string, error) {
	cell := CellName(row, col)
	if cell == "" {
		return "", fmt.Errorf("cell (%d, %d) is outside the sheet", row, col)
	}
	if wb.closed {
		return "", errors.New("workbook is closed")
	}
	formula, err := wb.file.GetCellFormula(sheet, cell)
	if err != nil {
		return "", fmt.Errorf("%s!%s: %w", sheet, cell, err)
	}
	if formula != "" {
		return "=" + formula, nil
	}
	return wb.Value(sheet, row, col), nil
}

// load returns the sheet's display values, reading them on first use.
func (wb *Workbook) load(sheet string) ([][]string, error) {
	if wb.closed {
		return nil, errors.New("workbook is closed")
	}
	if data, ok := wb.rows[sheet]; ok {
		return data, nil
	}
	data, err := wb.file.GetRows(sheet)
	if err != nil {
		return nil, fmt.Errorf("read sheet %q: %w", sheet, err)
	}
	wb.rows[sheet] = data
	return data, nil
}

// CellName returns the A1-style name of a 0-based cell, e.g. (3, 1) is "B4".
// It returns "" if the cell is outside Excel's limits.
func CellName(row, col int) string {
	if row < 0 || row >= maxRows || col < 0 || col >= maxCols {
		return ""
	}
	name, err := excelize.CoordinatesToCellName(col+1, row+1)
	if err != nil {
		return ""
	}
	return name
}

// ColumnName returns the letters of a 0-based column, e.g. 26 is "AA".
// It returns "" if the column is outside Excel's limits.
func ColumnName(col int) string {
	if col < 0 || col >= maxCols {
		return ""
	}
	name, err := excelize.ColumnNumberToName(col + 1)
	if err != nil {
		return ""
	}
	return name
}

// CellIndex turns an A1-style name into a 0-based row and column.
func CellIndex(cell string) (row, col int, err error) {
	c, r, err := excelize.CellNameToCoordinates(cell)
	if err != nil {
		return 0, 0, fmt.Errorf("cell %q: %w", cell, err)
	}
	return r - 1, c - 1, nil
}
