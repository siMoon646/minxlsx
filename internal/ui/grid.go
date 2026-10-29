package ui

import (
	"image/color"
	"strconv"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// The grid is always at least this big, so a small or empty sheet still
// looks like a spreadsheet, and it shows this much empty space past the data.
const (
	minGridRows = 100
	minGridCols = 26
	gridMarginR = 20
	gridMarginC = 5

	// Excel's sheet limits.
	maxGridRows = 1048576
	maxGridCols = 16384
)

// Sample texts that set the default column width and the row-number width.
const (
	cellWidthSample = "0000000000000"
	rowHeaderSample = "0000000"
)

func (mw *MainWindow) newGrid() *widget.Table {
	t := widget.NewTableWithHeaders(mw.gridSize, mw.createCell, mw.updateCell)
	t.CreateHeader = mw.createHeader
	t.UpdateHeader = mw.updateHeader
	t.OnSelected = func(id widget.TableCellID) {
		mw.viewer.Select(id.Row, id.Col)
		mw.updateFormulaBar()
	}
	return t
}

// gridSize is the number of rows and columns the table shows.
func (mw *MainWindow) gridSize() (rows, cols int) {
	rows, cols = mw.viewer.Size()
	rows = min(max(rows+gridMarginR, minGridRows), maxGridRows)
	cols = min(max(cols+gridMarginC, minGridCols), maxGridCols)
	return rows, cols
}

// createCell makes a cell: a truncating label over an invisible spacer. The
// spacer sets the default column width, because a truncating label has no
// useful width of its own, and the table sizes columns from this template.
func (mw *MainWindow) createCell() fyne.CanvasObject {
	l := widget.NewLabel("")
	l.Truncation = fyne.TextTruncateEllipsis
	spacer := canvas.NewRectangle(color.Transparent)
	spacer.SetMinSize(fyne.NewSize(
		fyne.MeasureText(cellWidthSample, theme.TextSize(), fyne.TextStyle{}).Width+2*theme.InnerPadding(), 0))
	return container.NewStack(spacer, l)
}

func (mw *MainWindow) updateCell(id widget.TableCellID, o fyne.CanvasObject) {
	cellLabel(o).SetText(mw.viewer.Value(id.Row, id.Col))
}

// cellLabel returns the label inside a cell made by createCell.
func cellLabel(o fyne.CanvasObject) *widget.Label {
	return o.(*fyne.Container).Objects[1].(*widget.Label)
}

// createHeader makes a header label. Its sample text sets the width of the
// row-number column; updateHeader replaces it.
func (mw *MainWindow) createHeader() fyne.CanvasObject {
	l := widget.NewLabel(rowHeaderSample)
	l.Alignment = fyne.TextAlignCenter
	l.TextStyle = fyne.TextStyle{Bold: true}
	return l
}

// updateHeader shows column letters along the top (Row == -1) and 1-based
// row numbers down the side (Col == -1).
func (mw *MainWindow) updateHeader(id widget.TableCellID, o fyne.CanvasObject) {
	l := o.(*widget.Label)
	switch {
	case id.Row < 0:
		l.SetText(mw.viewer.ColumnName(id.Col))
	case id.Col < 0:
		l.SetText(strconv.Itoa(id.Row + 1))
	}
}
