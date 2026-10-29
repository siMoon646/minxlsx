// Package ui builds MinXLSX's window and widgets. It only talks to the
// controller in internal/app and never touches files or excelize.
package ui

import (
	"log"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/siMoon646/minxlsx/internal/app"
)

const (
	appTitle      = "MinXLSX"
	noFileText    = "No file open"
	defaultWidth  = 1024
	defaultHeight = 768
)

// fileExtensions are the files the Open dialog shows.
var fileExtensions = []string{".xlsx", ".xlsm"}

// MainWindow is the app's single window: toolbar, formula bar, grid and
// sheet tabs, top to bottom.
type MainWindow struct {
	win    fyne.Window
	viewer *app.Viewer

	fileLabel *widget.Label
	address   *widget.Label
	formula   *widget.Entry
	table     *widget.Table
	tabs      *fyne.Container // one button per visible sheet
}

// NewMainWindow builds the window around a viewer. Call Window().ShowAndRun
// to show it.
func NewMainWindow(a fyne.App, v *app.Viewer) *MainWindow {
	mw := &MainWindow{
		win:       a.NewWindow(appTitle),
		viewer:    v,
		fileLabel: widget.NewLabel(noFileText),
		address:   widget.NewLabel(""),
		formula:   widget.NewEntry(),
		tabs:      container.NewHBox(),
	}
	mw.formula.Disable() // read-only until editing lands in milestone 3
	mw.address.Alignment = fyne.TextAlignCenter
	mw.address.TextStyle = fyne.TextStyle{Monospace: true}
	mw.table = mw.newGrid()

	toolbar := container.NewHBox(
		widget.NewButtonWithIcon("Open", theme.FolderOpenIcon(), mw.showOpenDialog),
		mw.fileLabel,
	)
	// The address box is wide enough for the longest address, "XFD1048576".
	addressBox := container.NewGridWrap(
		fyne.NewSize(fyne.MeasureText("XFD1048576", theme.TextSize(), mw.address.TextStyle).Width+
			4*theme.InnerPadding(), mw.address.MinSize().Height),
		mw.address,
	)
	formulaBar := container.NewBorder(nil, nil, addressBox, nil, mw.formula)
	tabBar := container.NewHScroll(mw.tabs)

	mw.win.SetContent(container.NewBorder(
		container.NewVBox(toolbar, formulaBar), tabBar, nil, nil, mw.table))
	mw.win.Resize(fyne.NewSize(defaultWidth, defaultHeight))

	mw.win.SetMainMenu(mw.newMainMenu())
	// Closing the window (or File → Quit) closes the file.
	mw.win.SetOnClosed(func() {
		mw.viewer.OnChange = nil // the widgets are going away
		if err := mw.viewer.Close(); err != nil {
			log.Printf("closing file: %v", err)
		}
	})

	v.OnChange = mw.refresh
	mw.refresh()
	return mw
}

// newMainMenu builds the menu bar. Menu-item shortcuts are handled by Fyne
// before any widget sees the key, so Ctrl+O works wherever focus is. Fyne
// adds File → Quit itself. Save and Save As come in milestone 3.
func (mw *MainWindow) newMainMenu() *fyne.MainMenu {
	open := fyne.NewMenuItem("Open…", mw.showOpenDialog)
	open.Shortcut = &desktop.CustomShortcut{KeyName: fyne.KeyO, Modifier: fyne.KeyModifierShortcutDefault}
	return fyne.NewMainMenu(fyne.NewMenu("File", open))
}

// Window returns the Fyne window.
func (mw *MainWindow) Window() fyne.Window { return mw.win }

// OpenFile opens a file and shows an error dialog if that fails.
func (mw *MainWindow) OpenFile(path string) {
	if err := mw.open(path); err != nil {
		dialog.ShowError(err, mw.win)
	}
}

// open opens a file through the viewer; the viewer's OnChange redraws.
func (mw *MainWindow) open(path string) error {
	return mw.viewer.Open(path)
}

func (mw *MainWindow) showOpenDialog() {
	d := dialog.NewFileOpen(func(r fyne.URIReadCloser, err error) {
		if err != nil {
			dialog.ShowError(err, mw.win)
			return
		}
		if r == nil {
			return // cancelled
		}
		path := r.URI().Path()
		// Only the path is needed; the model opens the file itself.
		if err := r.Close(); err != nil {
			log.Printf("closing dialog reader: %v", err)
		}
		mw.OpenFile(path)
	}, mw.win)
	d.SetFilter(storage.NewExtensionFileFilter(fileExtensions))
	d.Resize(fyne.NewSize(defaultWidth*3/4, defaultHeight*3/4))
	d.Show()
}

// refresh redraws everything after the file or sheet changes.
func (mw *MainWindow) refresh() {
	if name := mw.viewer.FileName(); name != "" {
		mw.fileLabel.SetText(name)
		mw.win.SetTitle(name + " - " + appTitle)
	} else {
		mw.fileLabel.SetText(noFileText)
		mw.win.SetTitle(appTitle)
	}
	mw.refreshTabs()
	mw.table.Refresh()
	mw.table.ScrollToTop()
	mw.table.ScrollToLeading()
	// Selecting A1 again does nothing in the table if A1 was already
	// selected, so update the formula bar directly as well.
	mw.table.Select(widget.TableCellID{Row: 0, Col: 0})
	mw.updateFormulaBar()
}

func (mw *MainWindow) refreshTabs() {
	current := mw.viewer.CurrentSheet()
	objs := make([]fyne.CanvasObject, 0, len(mw.viewer.Sheets()))
	for i, name := range mw.viewer.Sheets() {
		b := widget.NewButton(name, func() {
			if err := mw.viewer.SelectSheet(i); err != nil {
				dialog.ShowError(err, mw.win)
			}
		})
		if i == current {
			b.Importance = widget.HighImportance
		}
		objs = append(objs, b)
	}
	mw.tabs.Objects = objs
	mw.tabs.Refresh()
}

func (mw *MainWindow) updateFormulaBar() {
	addr, text := mw.viewer.Selected()
	mw.address.SetText(addr)
	mw.formula.SetText(text)
}
