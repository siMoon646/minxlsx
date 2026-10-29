# MinXLSX — Design Doc

## Overview

A small, portable desktop app that opens an .xlsx file, lets you view and edit cell values, and saves it back without damaging the rest of the workbook. It runs straight from a USB stick on Windows and Linux with no install, and should launch in under a second. It is open source under GPL-3.0, developed in a public GitHub repo.

**Goals**

- Open, edit and save .xlsx files on Windows 10/11 and desktop Linux (x86-64 only), both supported from day one
- Portable: one executable per OS, no install or admin rights, settings stored beside the executable
- Edit cell values, add/delete rows and columns, manage sheets
- Preserve everything it doesn't understand (styles, formulas, charts) on save
- Stay small and fast: one window, keyboard-first, no ribbon

**Non-goals**

- Formula calculation engine (formulas are shown and edited as text, not computed)
- Charts, pivot tables, macros, conditional-formatting editors
- Real-time collaboration or cloud sync
- Legacy .xls or .ods support in v1

## Tech stack

Go + Fyne + excelize. Go compiles to one fast executable per OS, excelize is the most mature read-write .xlsx library outside Python, and Go is simple enough to review what Claude Code writes. Python was dropped because a packaged Python app is 60–80 MB and unpacks itself on every launch, which is slow from a USB stick.

| Option | Binary size | Pros | Cons |
| --- | --- | --- | --- |
| **Go + Fyne + excelize** (chosen) | ~20–30 MB | Instant launch; strong .xlsx fidelity; easy to read and debug | Fyne widgets look slightly non-native |
| Rust + egui + umya-spreadsheet | ~5–15 MB | Smallest and fastest | Younger .xlsx library; harder to debug |
| Python + PySide6 + openpyxl | ~60–80 MB | Fastest to build | Slow launch from USB; slow on large files |

**Tooling:** Go modules, `gofmt` + `golangci-lint`, `go test` (Fyne's `test` package for headless UI tests), `fyne package` for icons and metadata.

**Portable builds:** Fyne uses OpenGL through cgo, so plain Go cross-compiling doesn't work. GitHub Actions builds `minxlsx.exe` on a Windows runner and `minxlsx` on a Linux runner for every release, free and unlimited because the repo is public; `fyne-cross` (Docker) builds both locally. Both files go on the USB stick, with a `settings/` folder beside them.

**Development environment:** WSL Ubuntu on Windows 11. Keep the repo in the WSL filesystem (`~/proj/personal/minxlsx`), not under `/mnt/c`. WSLg shows the Fyne window on the Windows desktop.

## Architecture

Three layers, with each one only talking to the one below. This keeps the UI swappable and makes the risky part (file I/O) easy to test on its own. Only the model layer touches excelize and the file.

```
UI (internal/ui)
  Main window ............ toolbar, menus, file dialogs
  Grid + formula bar ..... Fyne table, edit cell values
  Sheet tabs + status .... switch sheets, count and sum
        │ user actions
        ▼
Controller (internal/app)
  Edit controller ........ turns user actions into commands
  Undo stack ............. one command per edit, redo too
        │ commands change state
        ▼
Model (internal/model)
  Grid data source ....... cells the grid reads
  Workbook state ......... excelize file + dirty flag
  File I/O ............... atomic save, .bak, scan
        │ load / save via excelize
        ▼
.xlsx / .xlsm / .csv file on disk
```

The model tells the grid to refresh through a change callback, so the UI never calls excelize directly.

```
minxlsx/
├── cmd/minxlsx/main.go
├── internal/
│   ├── ui/        # windows, widgets, shortcuts
│   ├── app/       # controller, undo commands
│   └── model/     # workbook state, grid data source, file I/O
├── testdata/      # fixture .xlsx and .csv files
├── .github/workflows/build.yml
├── docs/DESIGN.md
└── CLAUDE.md
```

## Feature scope

The MVP is "open, edit values, save safely"; everything else waits until that is solid.

| Feature | Release |
| --- | --- |
| Open/save .xlsx, Save As, recent files | MVP |
| Grid view with sheet tabs | MVP |
| Edit cell values (text, numbers, dates, booleans, formulas as text) | MVP |
| Undo/redo | MVP |
| Insert/delete rows and columns | MVP |
| Add, rename, delete, reorder sheets | MVP |
| Unsaved-changes warning | MVP |
| Open .csv as a one-sheet workbook and save back as .csv (adding a second sheet prompts Save As .xlsx) | MVP |
| Find and replace | v1.1 |
| Copy/paste ranges (tab-separated, works with Excel) | v1.1 |
| Basic formatting: bold, italic, number format | v1.1 |
| Column width (drag to resize; marked unsaved, written to the file only on save) / row height, freeze panes | v1.1 |
| Dark mode | v1.1 |
| Formula evaluation (via excelize's `CalcCellValue`) | Later |

## UI layout

One window with a menu bar and four strips top to bottom, no ribbon or sidebars.

- **Menu bar:** a File menu with Open (Ctrl+O), Save (Ctrl+S), Save As (Ctrl+Shift+S) and Quit. Fyne adds Quit itself.

1. **Toolbar:** Open, Save, Undo, Redo, and the file name with a • when unsaved
2. **Formula bar:** cell address (e.g. `B4`) plus an editable field showing the raw value or formula
3. **Grid:** spreadsheet-style column letters and row numbers; double-click or F2 to edit
4. **Sheet tabs + status bar:** tabs with a + button to switch between a workbook's sheets; status shows the selection's count and sum

**Keyboard shortcuts:** Ctrl+O/S/Z/Y as usual, Ctrl+Shift+S for Save As, arrows and Tab/Enter to move, Delete to clear, Ctrl+Space / Shift+Space to select column/row, Ctrl+Shift+= / Ctrl+- to insert/delete.

## File fidelity and data safety

The app must never silently destroy a user's file. excelize keeps most workbook parts, but no library round-trips everything Excel can make, so the app checks for risky content and warns before saving.

- **No autosave:** every change (cell edits, structure edits, column widths) stays in memory until the user saves, with Ctrl+S, the toolbar's Save button, or File → Save / Save As. The file on disk only changes on an explicit save. Closing the window, or quitting, with unsaved changes asks first.
- **Detect before editing:** on open, scan the .xlsx zip for `drawings/`, `charts/`, `pivotTables/` and `vbaProject.bin`; if found, show a banner: "This file contains items that may change on save."
- **Atomic saves:** write to a temp file in the same folder, then rename over the original, so a crash or a pulled USB stick never leaves a half-written file.
- **Backup on first save:** keep `name.xlsx.bak` from the session's first save.
- **Formulas stay formulas:** the grid shows the cached value (`GetCellValue`); the formula bar shows the formula (`GetCellFormula`). Both come from one load.
- **Macro files:** open .xlsm and save back as .xlsm, keeping the macro part untouched.
- **Large files:** use excelize's row iterator to load big sheets in pages instead of all at once.

## Testing

Round-trip tests are the safety net: every change must keep "open → save → reopen" lossless.

- **Fixture files** in `testdata/`: plain data, formulas, dates, merged cells, styled cells, multiple sheets, a CSV, real Excel-made files from Apache POI's test suite, one saved by ONLYOFFICE, and one with a chart (to test the warning). A LibreOffice-saved file is optional.
- **Round-trip tests:** open each fixture, save to a temp path, reopen, and compare every cell value, formula, merge and style.
- **Model tests:** edits, undo/redo, row/column insert and delete, sheet operations.
- **UI tests** with Fyne's headless `test` package: typing in a cell, keyboard navigation, the unsaved-changes prompt.
- **CI:** GitHub Actions runs `golangci-lint` and `go test` on Windows and Linux for every push, then builds both executables on release.

## Milestones

Seven milestones, each sized for one or two Claude Code sessions and ending with passing tests and a commit.

1. **Skeleton:** Go module, CLAUDE.md, GPL-3.0 LICENSE file, empty Fyne window, lint + tests wired up, CI building both executables
2. **Read-only viewer:** open a file, show the grid and sheet tabs, formula bar shows the selected cell, File menu with Open (Ctrl+O)
   - Opens .xlsx and .xlsm only; .csv moves to milestone 3 so reading and writing it land together.
   - Hidden sheets get no tab (as in Excel) but stay in the file.
   - Formulas without a saved result show an empty cell, since there is no calculation engine.
   - Files are read fully into memory. By default excelize unpacks any sheet over 16 MiB into the OS temp folder, which breaks the portable rule and is left behind on a crash, so `model.Open` raises that limit. A test checks no temp files are made.
3. **Editing + save:** edit values, atomic save, Save As, unsaved-changes prompt, round-trip tests on all fixtures, open and save .csv files
   - Save (Ctrl+S) and Save As (Ctrl+Shift+S) go in the File menu and the toolbar. The unsaved-changes prompt covers closing the window and File → Quit.
   - From the milestone 2 review: excelize reads a worksheet part full of broken XML as an empty sheet, with no error. Before saving, make sure such a sheet can't be written back empty over the user's data (refuse to save, or warn).
   - Before starting (from the milestone 1 review):
     - Excel-made fixtures come from Apache POI's test files (Apache-2.0): `excel-budget`, `excel-shared-formulas`, `excel-hidden-sheet`, `excel-unicode` and `excel-chart` (all `.xlsx`). `onlyoffice-bankbook.xlsx` is saved by ONLYOFFICE (tables with structured references, a shared `TODAY()` formula). A LibreOffice-saved file is optional, not required. See `testdata/README.md`.
     - From the ONLYOFFICE fixture: round-trip tests must check that every table's name (`displayName`) and range survive a save, since the fixture's structured references depend on them.
     - Add a macro fixture (`.xlsm` with a small committed `vbaProject.bin`, via excelize's `AddVBAProject`) so the round-trip tests prove macros survive a save.
     - Add a CSV fixture with CRLF line endings and a UTF-8 BOM, as Excel writes them.
     - Make the styled-cell check compare each border's type, style and colour, not just how many borders there are.
     - Add a `.golangci.yml` that enables at least `errcheck`, `gosec`, `bodyclose` and `errorlint`, so ignored file I/O errors fail lint.
4. **Undo/redo:** command pattern for every edit
5. **Structure edits:** insert/delete rows and columns, add/rename/delete/reorder sheets, unhide hidden sheets
6. **Safety:** risky-content warning, `.bak` backup, paged loading for large files
   - Paged loading must not bring back excelize's temp files in the OS temp folder (see milestone 2); point `Options.TmpDir` beside the executable if it needs them.
7. **Portable release:** app icon, settings beside the executable, README, test both builds from a real USB stick
   - From the milestone 1 review:
     - Never use Fyne's Preferences API. Once `fyne package` (or a `FyneApp.toml`) gives the app an ID, Fyne stores preferences in the user's config folder (e.g. `%APPDATA%`), which breaks portability. Add a test that settings are only written to `./settings/`. Fyne's file dialog also writes preferences (last folder, view layout), so that test must open a file dialog too.
     - Pin `softprops/action-gh-release` (and other third-party actions) to a full commit SHA, since the release job has `contents: write`.
     - Build release executables without restoring the Go build cache, so tagged releases always compile from clean.
     - Publish a `SHA256SUMS` file with each release so users can verify the executables they copy.

After the MVP, take v1.1 features one at a time in the same way.

**Notes for v1.1 column widths** (checked against Fyne v2.8.2):

- Fyne's `widget.Table` already lets users drag the column borders in the header row to resize (and row borders in the header column). This works in the viewer today, but only on screen.
- The table has no callback when a column is resized. To mark the file unsaved and record the width as an undoable edit, wrap `widget.Table` and override `Dragged`/`DragEnd` to call the table's own handler, then report the new width. That's a small amount of custom code, not a custom table.
- While dragging, Fyne won't make a column narrower than the cell template's width. Milestone 2 uses that template width as the default column width, so v1.1 must make the template narrow and set default widths with `SetColumnWidth` instead. That call refreshes the whole table, so set widths once per sheet, not per column per refresh.
- Widths read from the file need excelize's character-based units converted to pixels, and back again on save.

## Working with Claude Code

- Start each milestone in plan mode and review the plan before any code is written.
- Ask for tests first, then the code that makes them pass.
- Keep sessions to one milestone; use `/clear` between unrelated tasks so context stays clean.
- Read each diff before committing, even when tests pass — you're the reviewer.
- When something breaks, give Claude Code the exact error and the fixture file that triggers it.

## Agent plan

One main Claude Code session builds the core milestones in order; subagents take side tasks that don't touch the same files. Each parallel agent works on its own git branch or worktree.

| Task | Run as | When |
| --- | --- | --- |
| Milestones 1–7 (core code) | Main session, one at a time | Throughout |
| Generate fixture .xlsx files + round-trip tests | Parallel subagent (`test-writer`) | During milestone 1 |
| GitHub Actions workflow for Windows + Linux builds | Parallel subagent (`ci-builder`) | During milestone 1 |
| Look up excelize / Fyne APIs for a feature | Research subagent | Whenever the main session is unsure |
| Review the milestone's diff for bugs and data-loss risks | Fresh review subagent (`reviewer`) | End of every milestone |
| Final check of each diff before merging | You | End of every milestone |

Never run two agents on the core milestones at once: they edit the same files and will conflict.
