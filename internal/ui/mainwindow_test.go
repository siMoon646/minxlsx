package ui

import (
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/siMoon646/minxlsx/internal/app"
)

var testdataDir = filepath.Join("..", "..", "testdata")

// newTestWindow builds a main window on Fyne's headless test driver.
func newTestWindow(t *testing.T) (*MainWindow, *app.Viewer) {
	t.Helper()
	a := test.NewTempApp(t)
	v := app.NewViewer()
	mw := NewMainWindow(a, v)
	mw.Window().Resize(fyne.NewSize(800, 600))
	t.Cleanup(func() {
		if err := v.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})
	return mw, v
}

func openFixture(t *testing.T, mw *MainWindow, name string) {
	t.Helper()
	if err := mw.open(filepath.Join(testdataDir, name)); err != nil {
		t.Fatalf("open %s: %v", name, err)
	}
}

// cellText runs the grid's update function for one cell and returns what
// the cell would show.
func cellText(mw *MainWindow, row, col int) string {
	c := mw.createCell()
	mw.updateCell(widget.TableCellID{Row: row, Col: col}, c)
	return cellLabel(c).Text
}

func headerText(mw *MainWindow, row, col int) string {
	l := mw.createHeader().(*widget.Label)
	mw.updateHeader(widget.TableCellID{Row: row, Col: col}, l)
	return l.Text
}

func tabLabels(mw *MainWindow) []string {
	var out []string
	for _, o := range mw.tabs.Objects {
		out = append(out, o.(*widget.Button).Text)
	}
	return out
}

func TestOpenShowsGrid(t *testing.T) {
	mw, _ := newTestWindow(t)
	openFixture(t, mw, "plain.xlsx")

	if got := cellText(mw, 0, 0); got != "Name" {
		t.Errorf("cell A1 = %q, want Name", got)
	}
	if got := cellText(mw, 4, 3); got != "" {
		t.Errorf("cell D5 (past a short row) = %q, want empty", got)
	}
	if got := headerText(mw, -1, 0); got != "A" {
		t.Errorf("column header 0 = %q, want A", got)
	}
	if got := headerText(mw, -1, 27); got != "AB" {
		t.Errorf("column header 27 = %q, want AB", got)
	}
	if got := headerText(mw, 0, -1); got != "1" {
		t.Errorf("row header 0 = %q, want 1", got)
	}
	if got := mw.fileLabel.Text; got != "plain.xlsx" {
		t.Errorf("file label = %q", got)
	}
	if got := mw.Window().Title(); got != "plain.xlsx - MinXLSX" {
		t.Errorf("title = %q", got)
	}
	// The grid is at least spreadsheet-sized even for small data.
	if rows, cols := mw.gridSize(); rows < minGridRows || cols < minGridCols {
		t.Errorf("grid = %d×%d, want at least %d×%d", rows, cols, minGridRows, minGridCols)
	}
	// Columns get a usable default width from the cell template.
	if w := mw.createCell().MinSize().Width; w < 80 {
		t.Errorf("default column width = %v, too narrow", w)
	}
	if !mw.formula.Disabled() {
		t.Error("formula bar should be read-only in the viewer")
	}
}

func TestGridGrowsForLargeSheet(t *testing.T) {
	mw, _ := newTestWindow(t)
	openFixture(t, mw, "large.xlsx")
	if rows, _ := mw.gridSize(); rows <= 5001 {
		t.Errorf("grid rows = %d, want more than the 5001 data rows", rows)
	}
	if got := cellText(mw, 5000, 1); got != "row 5000" {
		t.Errorf("last row = %q", got)
	}
}

func TestSelectUpdatesFormulaBar(t *testing.T) {
	mw, _ := newTestWindow(t)
	openFixture(t, mw, "plain.xlsx")

	if mw.address.Text != "A1" || mw.formula.Text != "Name" {
		t.Errorf("after open: %q %q, want A1 Name", mw.address.Text, mw.formula.Text)
	}
	mw.table.Select(widget.TableCellID{Row: 1, Col: 1})
	if mw.address.Text != "B2" || mw.formula.Text != "10" {
		t.Errorf("after selecting B2: %q %q", mw.address.Text, mw.formula.Text)
	}
}

func TestFormulaBarShowsFormula(t *testing.T) {
	mw, _ := newTestWindow(t)
	openFixture(t, mw, "formulas.xlsx")
	mw.table.Select(widget.TableCellID{Row: 3, Col: 0})
	if mw.address.Text != "A4" || mw.formula.Text != "=SUM(A1:A3)" {
		t.Errorf("A4: %q %q", mw.address.Text, mw.formula.Text)
	}
}

func TestSheetTabs(t *testing.T) {
	mw, v := newTestWindow(t)
	openFixture(t, mw, "multisheet.xlsx")

	want := []string{"Summary", "Data 2024", "Empty"}
	if got := tabLabels(mw); len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("tabs = %q, want %q (Hidden must be skipped)", got, want)
	}
	if got := cellText(mw, 0, 0); got != "Total" {
		t.Errorf("Summary A1 = %q", got)
	}

	mw.table.Select(widget.TableCellID{Row: 2, Col: 2})
	test.Tap(mw.tabs.Objects[1].(*widget.Button))
	if v.CurrentSheet() != 1 {
		t.Fatalf("current sheet = %d, want 1", v.CurrentSheet())
	}
	if got := cellText(mw, 1, 0); got != "Jan" {
		t.Errorf("Data 2024 A2 = %q, want Jan", got)
	}
	if mw.address.Text != "A1" || mw.formula.Text != "Month" {
		t.Errorf("after switching sheet: %q %q, want A1 Month", mw.address.Text, mw.formula.Text)
	}
	for i, o := range mw.tabs.Objects {
		b := o.(*widget.Button)
		if current := i == 1; (b.Importance == widget.HighImportance) != current {
			t.Errorf("tab %q importance = %v", b.Text, b.Importance)
		}
	}
}

func TestEmptyWindow(t *testing.T) {
	mw, _ := newTestWindow(t)
	// Render it once to make sure nothing panics without a file.
	test.NewTempWindow(t, mw.Window().Content())
	if got := cellText(mw, 0, 0); got != "" {
		t.Errorf("cell with no file = %q", got)
	}
	if mw.fileLabel.Text != noFileText || len(mw.tabs.Objects) != 0 {
		t.Errorf("empty state: label %q, %d tabs", mw.fileLabel.Text, len(mw.tabs.Objects))
	}
	if mw.Window().Title() != appTitle {
		t.Errorf("title = %q", mw.Window().Title())
	}
}

func TestFailedOpenKeepsFile(t *testing.T) {
	mw, v := newTestWindow(t)
	openFixture(t, mw, "plain.xlsx")
	if err := mw.open(filepath.Join(testdataDir, "missing.xlsx")); err == nil {
		t.Fatal("want error")
	}
	if v.FileName() != "plain.xlsx" || mw.fileLabel.Text != "plain.xlsx" {
		t.Errorf("after failed open: viewer %q, label %q", v.FileName(), mw.fileLabel.Text)
	}
}

func TestClosingWindowClosesFile(t *testing.T) {
	mw, v := newTestWindow(t)
	openFixture(t, mw, "plain.xlsx")
	mw.Window().Close()
	if v.HasFile() || v.FileName() != "" {
		t.Errorf("file still open after closing the window: %q", v.FileName())
	}
}
