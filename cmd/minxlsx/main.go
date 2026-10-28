// Command minxlsx is a portable, minimalist .xlsx editor.
//
// This file only starts the app. Window and widgets live in internal/ui,
// edit logic in internal/app, and all file access in internal/model.
package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/widget"
)

const (
	appTitle      = "MinXLSX"
	defaultWidth  = 1024
	defaultHeight = 768
)

func main() {
	// app.New (no app ID) keeps Fyne from storing preferences in the
	// user's config folder. Settings will live in ./settings/ beside the
	// executable instead, so the app stays portable.
	a := app.New()

	w := a.NewWindow(appTitle)
	w.SetContent(widget.NewLabel("No file open"))
	w.Resize(fyne.NewSize(defaultWidth, defaultHeight))
	w.CenterOnScreen()
	w.ShowAndRun()
}
