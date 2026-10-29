package model

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/xuri/excelize/v2"
)

// Save writes the workbook back to its file (see SaveAs).
func (wb *Workbook) Save() error {
	return wb.SaveAs(wb.path)
}

// SaveAs writes the workbook to path atomically (writeFileAtomic) and makes
// path the workbook's file. It refuses a damaged workbook (ReadOnlyReason)
// and a change of file type (see SaveAsTypes). A read-only target gives an
// error matching ErrReadOnly.
//
// If any cell was edited, the workbook is marked for full recalculation on
// load, because cached formula results may be stale (MinXLSX has no
// calculation engine yet).
func (wb *Workbook) SaveAs(path string) error {
	switch {
	case wb.closed:
		return errors.New("workbook is closed")
	case wb.readOnly != "":
		return fmt.Errorf("can't save: %s", wb.readOnly)
	}
	if err := checkSaveType(wb.path, path); err != nil {
		return err
	}
	if wb.edited {
		on := true
		if err := wb.file.SetCalcProps(&excelize.CalcPropsOptions{FullCalcOnLoad: &on}); err != nil {
			return fmt.Errorf("save %s: %w", path, err)
		}
	}
	// excelize picks the content type (.xlsx or .xlsm) from f.Path.
	oldPath := wb.file.Path
	wb.file.Path = path
	err := writeFileAtomic(path, func(w io.Writer) error {
		return wb.file.Write(w)
	})
	if err != nil {
		wb.file.Path = oldPath
		return err
	}
	wb.path = path
	return nil
}

// SaveAsTypes returns the extensions the workbook may be saved as. Save As
// keeps the file type: switching between .xlsx and .xlsm would change or
// drop the macro part.
func (wb *Workbook) SaveAsTypes() []string {
	return []string{strings.ToLower(filepath.Ext(wb.path))}
}

// checkSaveType refuses to save to a different file type.
func checkSaveType(from, to string) error {
	want := strings.ToLower(filepath.Ext(from))
	if got := strings.ToLower(filepath.Ext(to)); got != want {
		return fmt.Errorf("save %s: can't save a %s file as %q (use a %s name)", to, want, filepath.Ext(to), want)
	}
	return nil
}
