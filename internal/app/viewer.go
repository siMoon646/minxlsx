// Package app is the controller between the UI and the model. It turns user
// actions into model calls; the UI never touches files or excelize.
package app

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/siMoon646/minxlsx/internal/model"
)

// Viewer holds the open workbook, the current sheet and the selected cell.
// It is read-only for now; editing and the undo stack come in milestones 3
// and 4. Rows and columns are 0-based.
type Viewer struct {
	// OnChange is called after the open file or the current sheet changes,
	// so the UI can refresh. It is not called for selection changes.
	OnChange func()

	wb       *model.Workbook
	sheets   []string
	sheet    int
	row, col int
}

// NewViewer returns a viewer with no file open.
func NewViewer() *Viewer { return &Viewer{} }

// Open opens a file and shows its first visible sheet with A1 selected. If
// it fails, the current file stays open.
func (v *Viewer) Open(path string) error {
	wb, err := model.Open(path)
	if err != nil {
		return err
	}
	sheets := wb.Sheets()
	if len(sheets) == 0 {
		return errors.Join(fmt.Errorf("open %s: no visible sheets", path), wb.Close())
	}
	// Read the first sheet now, so a broken sheet fails here and not later
	// while the grid is drawing.
	if _, _, err := wb.Size(sheets[0]); err != nil {
		return errors.Join(err, wb.Close())
	}

	old := v.wb
	v.wb, v.sheets, v.sheet, v.row, v.col = wb, sheets, 0, 0, 0
	v.changed()
	// The new file is already showing, so a failed close of the old one
	// is only reported.
	if err := old.Close(); err != nil {
		return fmt.Errorf("opened %s, but closing the previous file failed: %w", path, err)
	}
	return nil
}

// Close closes the open file, if any. Calling it again is a no-op.
func (v *Viewer) Close() error {
	if v.wb == nil {
		return nil
	}
	err := v.wb.Close()
	v.wb, v.sheets, v.sheet, v.row, v.col = nil, nil, 0, 0, 0
	v.changed()
	return err
}

// HasFile reports whether a file is open.
func (v *Viewer) HasFile() bool { return v.wb != nil }

// FileName returns the open file's name without its folder, or "".
func (v *Viewer) FileName() string {
	if v.wb == nil {
		return ""
	}
	return filepath.Base(v.wb.Path())
}

// Sheets returns the visible sheet names.
func (v *Viewer) Sheets() []string { return v.sheets }

// CurrentSheet returns the index (into Sheets) of the sheet being shown.
func (v *Viewer) CurrentSheet() int { return v.sheet }

// SelectSheet shows another sheet and selects its A1.
func (v *Viewer) SelectSheet(i int) error {
	if i < 0 || i >= len(v.sheets) {
		return fmt.Errorf("no sheet %d", i)
	}
	if _, _, err := v.wb.Size(v.sheets[i]); err != nil {
		return err
	}
	v.sheet, v.row, v.col = i, 0, 0
	v.changed()
	return nil
}

// Size returns the current sheet's data extent.
func (v *Viewer) Size() (rows, cols int) {
	if v.wb == nil {
		return 0, 0
	}
	rows, cols, err := v.wb.Size(v.sheets[v.sheet])
	if err != nil {
		return 0, 0 // SelectSheet and Open already checked the sheet loads
	}
	return rows, cols
}

// Value returns a cell's display value on the current sheet.
func (v *Viewer) Value(row, col int) string {
	if v.wb == nil {
		return ""
	}
	return v.wb.Value(v.sheets[v.sheet], row, col)
}

// Select selects a cell. Cells outside the sheet are ignored.
func (v *Viewer) Select(row, col int) {
	if model.CellName(row, col) == "" {
		return
	}
	v.row, v.col = row, col
}

// Selected returns the selected cell's address (e.g. "B4") and the text for
// the formula bar (model.EditText: the formula with a leading "=", or the
// value as it would be typed).
func (v *Viewer) Selected() (address, text string) {
	address = model.CellName(v.row, v.col)
	if v.wb == nil {
		return address, ""
	}
	text, err := v.wb.EditText(v.sheets[v.sheet], v.row, v.col)
	if err != nil {
		return address, ""
	}
	return address, text
}

// ColumnName returns the letters of a 0-based column, e.g. 26 is "AA".
func (v *Viewer) ColumnName(col int) string { return model.ColumnName(col) }

func (v *Viewer) changed() {
	if v.OnChange != nil {
		v.OnChange()
	}
}
