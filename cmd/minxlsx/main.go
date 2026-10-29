// Command minxlsx is a portable, minimalist .xlsx editor.
//
// This file only starts the app. Window and widgets live in internal/ui,
// edit logic in internal/app, and all file access in internal/model.
//
// Usage: minxlsx [file.xlsx]
package main

import (
	"log"
	"os"

	fyneapp "fyne.io/fyne/v2/app"

	"github.com/siMoon646/minxlsx/internal/app"
	"github.com/siMoon646/minxlsx/internal/ui"
)

func main() {
	// app.New (no app ID) keeps Fyne from storing preferences in the
	// user's config folder. Settings will live in ./settings/ beside the
	// executable instead, so the app stays portable.
	a := fyneapp.New()

	viewer := app.NewViewer()
	mw := ui.NewMainWindow(a, viewer)
	w := mw.Window()
	w.CenterOnScreen()
	// A file given on the command line (or via "Open with") opens at start.
	if len(os.Args) > 1 {
		mw.OpenFile(os.Args[1])
	}
	w.ShowAndRun()

	// The window's close handler normally closes the file already; this
	// covers quit paths that skip it. Close is safe to call twice.
	if err := viewer.Close(); err != nil {
		log.Printf("closing file: %v", err)
	}
}
