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
| Column width / row height, freeze panes | v1.1 |
| Dark mode | v1.1 |
| Formula evaluation (via excelize's `CalcCellValue`) | Later |

## UI layout

One window, four strips top to bottom, no ribbon or sidebars.

1. **Toolbar:** Open, Save, Undo, Redo, and the file name with a • when unsaved
2. **Formula bar:** cell address (e.g. `B4`) plus an editable field showing the raw value or formula
3. **Grid:** spreadsheet-style column letters and row numbers; double-click or F2 to edit
4. **Sheet tabs + status bar:** tabs with a + button to switch between a workbook's sheets; status shows the selection's count and sum

**Keyboard shortcuts:** Ctrl+O/S/Z/Y as usual, arrows and Tab/Enter to move, Delete to clear, Ctrl+Space / Shift+Space to select column/row, Ctrl+Shift+= / Ctrl+- to insert/delete.

## File fidelity and data safety

The app must never silently destroy a user's file. excelize keeps most workbook parts, but no library round-trips everything Excel can make, so the app checks for risky content and warns before saving.

- **Detect before editing:** on open, scan the .xlsx zip for `drawings/`, `charts/`, `pivotTables/` and `vbaProject.bin`; if found, show a banner: "This file contains items that may change on save."
- **Atomic saves:** write to a temp file in the same folder, then rename over the original, so a crash or a pulled USB stick never leaves a half-written file.
- **Backup on first save:** keep `name.xlsx.bak` from the session's first save.
- **Formulas stay formulas:** the grid shows the cached value (`GetCellValue`); the formula bar shows the formula (`GetCellFormula`). Both come from one load.
- **Macro files:** open .xlsm and save back as .xlsm, keeping the macro part untouched.
- **Large files:** use excelize's row iterator to load big sheets in pages instead of all at once.

## Testing

Round-trip tests are the safety net: every change must keep "open → save → reopen" lossless.

- **Fixture files** in `testdata/`: plain data, formulas, dates, merged cells, styled cells, multiple sheets, a CSV, a file made in Excel, one made in LibreOffice, and one with a chart (to test the warning).
- **Round-trip tests:** open each fixture, save to a temp path, reopen, and compare every cell value, formula, merge and style.
- **Model tests:** edits, undo/redo, row/column insert and delete, sheet operations.
- **UI tests** with Fyne's headless `test` package: typing in a cell, keyboard navigation, the unsaved-changes prompt.
- **CI:** GitHub Actions runs `golangci-lint` and `go test` on Windows and Linux for every push, then builds both executables on release.

## Milestones

Seven milestones, each sized for one or two Claude Code sessions and ending with passing tests and a commit.

1. **Skeleton:** Go module, CLAUDE.md, GPL-3.0 LICENSE file, empty Fyne window, lint + tests wired up, CI building both executables
2. **Read-only viewer:** open a file, show the grid and sheet tabs, formula bar shows the selected cell
3. **Editing + save:** edit values, atomic save, Save As, unsaved-changes prompt, round-trip tests on all fixtures
   - Before starting (from the milestone 1 review):
     - Add the hand-made fixtures `excel.xlsx` (saved by Excel) and `libreoffice.xlsx` (saved by LibreOffice Calc); see `testdata/README.md`.
     - Add a macro fixture (`.xlsm` with a small committed `vbaProject.bin`, via excelize's `AddVBAProject`) so the round-trip tests prove macros survive a save.
     - Add a CSV fixture with CRLF line endings and a UTF-8 BOM, as Excel writes them.
     - Make the styled-cell check compare each border's type, style and colour, not just how many borders there are.
     - Add a `.golangci.yml` that enables at least `errcheck`, `gosec`, `bodyclose` and `errorlint`, so ignored file I/O errors fail lint.
4. **Undo/redo:** command pattern for every edit
5. **Structure edits:** insert/delete rows and columns, add/rename/delete/reorder sheets
6. **Safety:** risky-content warning, `.bak` backup, paged loading for large files
7. **Portable release:** app icon, settings beside the executable, README, test both builds from a real USB stick
   - From the milestone 1 review:
     - Never use Fyne's Preferences API. Once `fyne package` (or a `FyneApp.toml`) gives the app an ID, Fyne stores preferences in the user's config folder (e.g. `%APPDATA%`), which breaks portability. Add a test that settings are only written to `./settings/`.
     - Pin `softprops/action-gh-release` (and other third-party actions) to a full commit SHA, since the release job has `contents: write`.
     - Build release executables without restoring the Go build cache, so tagged releases always compile from clean.
     - Publish a `SHA256SUMS` file with each release so users can verify the executables they copy.

After the MVP, take v1.1 features one at a time in the same way.

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
