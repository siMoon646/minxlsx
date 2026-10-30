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

// Workbook is an open .xlsx, .xlsm or .csv file. A CSV is held as a
// one-sheet workbook whose cells are all text.
//
// Rows and columns are 0-based everywhere in this API; A1 is (0, 0).
// A Workbook is not safe for concurrent use; the UI calls it from its own
// goroutine only.
type Workbook struct {
	path     string
	file     *excelize.File
	text     *textStore           // stores text in file's cells
	sheets   map[string]sheetData // display values, loaded on first use
	scan     map[string]sheetScan // from the open-time scan
	csv      *csvFile             // set while the file is a CSV
	readOnly string               // why it can't be edited or saved; "" if it can
	edited   bool                 // a cell changed since open
	closed   bool

	// faultAfterEdit, set only by tests, makes an edit fail after it has
	// written to the cell.
	faultAfterEdit func() error
}

// sheetData is one sheet's display values. Rows have different lengths,
// because excelize drops trailing empty cells.
type sheetData struct {
	rows [][]string
	cols int // length of the longest row
}

// openOptions makes excelize keep every part of the file in memory.
//
// By default excelize unpacks any worksheet or shared-strings part over
// 16 MiB into a temp file in the OS temp folder, removed only by Close. A
// crash or kill would leave it there, and it breaks the portable rule of
// never writing outside the executable's folder. The viewer reads whole
// sheets into memory anyway, so this costs little. Paged loading for big
// files (milestone 6) should revisit this.
//
// tmpDir is only set by tests, to check that no temp files are made.
func openOptions(tmpDir string) excelize.Options {
	return excelize.Options{
		UnzipSizeLimit:    excelize.UnzipSizeLimit,
		UnzipXMLSizeLimit: excelize.UnzipSizeLimit,
		TmpDir:            tmpDir,
	}
}

// Open opens an .xlsx, .xlsm or .csv file. Other extensions are rejected.
func Open(path string) (*Workbook, error) {
	return openWith(path, openOptions(""))
}

func openWith(path string, opts excelize.Options) (*Workbook, error) {
	switch ext := strings.ToLower(filepath.Ext(path)); ext {
	case ".xlsx", ".xlsm":
	case ".csv":
		return openCSV(path)
	case "":
		return nil, fmt.Errorf("open %s: unsupported file type (want .xlsx, .xlsm or .csv)", path)
	default:
		return nil, fmt.Errorf("open %s: unsupported file type %s (want .xlsx, .xlsm or .csv)", path, ext)
	}
	f, err := excelize.OpenFile(path, opts)
	if err != nil {
		// excelize can return a half-made file with the error; close it so
		// any temp files it made are removed.
		if f != nil {
			err = errors.Join(err, f.Close())
		}
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	scan, readOnly := scanSheets(f)
	return &Workbook{path: path, file: f, text: newTextStore(f), sheets: map[string]sheetData{}, scan: scan, readOnly: readOnly}, nil
}

// Close releases the file and any temp files excelize made for it. Calling
// it again is a no-op.
func (wb *Workbook) Close() error {
	if wb == nil || wb.closed {
		return nil
	}
	wb.closed = true
	wb.sheets = nil
	if err := wb.file.Close(); err != nil {
		return fmt.Errorf("close %s: %w", wb.path, err)
	}
	return nil
}

// Closed reports whether Close has been called.
func (wb *Workbook) Closed() bool { return wb.closed }

// Path returns the path the workbook was opened from.
func (wb *Workbook) Path() string { return wb.path }

// Sheets returns the visible sheets in workbook order. Hidden sheets are
// left out but stay in the file. A sheet whose visibility can't be read is
// shown, so no data is hidden from the user by mistake.
func (wb *Workbook) Sheets() []string {
	if wb.closed {
		return nil
	}
	var out []string
	for _, name := range wb.file.GetSheetList() {
		if visible, err := wb.file.GetSheetVisible(name); err != nil || visible {
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
	return len(data.rows), data.cols, nil
}

// Value returns the cell's display value (with number formats applied), as
// the grid shows it. Cells outside the data, negative indexes and unknown
// sheets all give "".
func (wb *Workbook) Value(sheet string, row, col int) string {
	data, err := wb.load(sheet)
	if err != nil || row < 0 || col < 0 || row >= len(data.rows) {
		return ""
	}
	// Rows have different lengths, so check the column in this row.
	if r := data.rows[row]; col < len(r) {
		return r[col]
	}
	return ""
}

// load returns the sheet's display values, reading them on first use.
func (wb *Workbook) load(sheet string) (sheetData, error) {
	if wb.closed {
		return sheetData{}, errors.New("workbook is closed")
	}
	if data, ok := wb.sheets[sheet]; ok {
		return data, nil
	}
	rows, err := wb.file.GetRows(sheet)
	if err != nil {
		return sheetData{}, fmt.Errorf("read sheet %q: %w", sheet, err)
	}
	data := sheetData{rows: rows}
	for _, r := range rows {
		data.cols = max(data.cols, len(r))
	}
	wb.sheets[sheet] = data
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
