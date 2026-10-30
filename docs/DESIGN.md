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
| In-cell editing (an editor over the cell, like Excel; the MVP edits in the formula bar) | v1.1 |
| Formula evaluation (via excelize's `CalcCellValue`) | Later |

## UI layout

One window with a menu bar and four strips top to bottom, no ribbon or sidebars.

- **Menu bar:** a File menu with Open (Ctrl+O), Save (Ctrl+S), Save As (Ctrl+Shift+S) and Quit. Fyne adds Quit itself.

1. **Toolbar:** Open, Save, Undo, Redo, and the file name with a • when unsaved
2. **Formula bar:** cell address (e.g. `B4`) plus an editable field showing the raw value or formula
3. **Grid:** spreadsheet-style column letters and row numbers; double-click or F2 to edit
4. **Sheet tabs + status bar:** tabs with a + button to switch between a workbook's sheets; status shows the selection's count and sum

**Keyboard shortcuts:** Ctrl+O/S/Z/Y as usual, Ctrl+Shift+S for Save As, arrows and Tab/Enter to move, Delete to clear, Ctrl+Space / Shift+Space to select column/row, Ctrl+Shift+= / Ctrl+- to insert/delete.

**Editing (MVP):** cells are edited in the formula bar, not in the cell.
- **Starting an edit:** F2, a double-click, or typing a character moves the cursor into the formula bar for the selected cell. A typed character replaces the content.
- **Finishing an edit:** Enter commits and moves down, Tab commits and moves right, Esc cancels.
- **Line breaks:** the formula bar keeps line breaks already in a cell. Typing new ones isn't supported yet.
- **Grid keys:** in the grid, arrows move the selection, Enter moves down, Tab / Shift+Tab move right/left, and Delete or Backspace clears the cell.
- **Merged cells:** editing a cell inside a merged range edits the range's top-left cell, the only one Excel shows.
- **What the formula bar shows** (`model.EditText`): text that recreates the cell when typed back in, not the formatted value the grid shows:
  - a number shows its stored value (`0.123456`, not `0.12`), or its shortest form when the stored value has too many digits to type (excelize stores 1e20 as `100000000000000000000`, which shows as `1E+20`)
  - a date or time shows as `yyyy-mm-dd[ hh:mm[:ss]]` when that gives back the exact same number; otherwise the number shows
  - text that would be read as something else gets a leading `'` (`'123`, `'=x`, `'TRUE`)
  - a number whose cell style can't be read (e.g. a style id past the end of the style table) shows as a plain number
- **When the text isn't known:** if `EditText` returns an error, the formula bar (3b) must not commit. An empty or stale text would overwrite the cell.
- **Unchanged commits:** committing the formula bar's text unchanged changes nothing and doesn't mark the file unsaved. That keeps a formula's cached value, and two kinds of number that would otherwise come back as text:
  - a number that needs more than 15 digits even in its shortest form (like `0.30000000000000004`)
  - negative zero: a cell stored as `-0` shows `-0`, which typed in is text (typed numbers never give `-0`, so "-0" is kept as typed)
- **Save As:** a small form with a folder picker and a file-name field, with its own "overwrite?" confirm. Fyne's file-save dialog calls `os.Create` on the chosen file before handing it back, which empties an existing file before the atomic save even starts, so it isn't used.

**How typed text is stored** (.xlsx/.xlsm; in a .csv everything is stored exactly as typed):

| Typed | Stored as |
| --- | --- |
| empty | value cleared, style kept |
| `'abc` | text `abc` (a leading `'` forces text) |
| `=…` | formula (a lone `=` is text) |
| `TRUE` / `FALSE` (any case) | boolean |
| `-3`, `1234.5`, `.5`, `1e5` | number |
| `2024-03-15`, `2024-03-15 13:45`, `2024-03-15 13:45:30` | date and time |
| anything else | text |

- **Numbers that stay text:** anything that would change if stored as a number. That means leading zeros (`007`), a leading `+`, more than 15 significant digits (Excel's precision), and values out of range.
- **Dates are timezone-free:** they are parsed as plain dates in UTC, so the local timezone can never shift them by hours.
- **1904 date system:** in a workbook that counts dates from 1904 (old Mac Excel), dates get that system's serial numbers, and a date before 1904 stays text, since Excel would show it as `####`.
- **Text is stored exactly:**
  - Text like `_x0041_`, which the file format would read back as `A`, is escaped so it stays as typed. Every `_` followed by `x` and four hex digits is escaped, closing `_` or not: in `_x0041` followed by a control character, the control character's own escape would supply the `_`. Escaping more than needed is safe, since `_x005F_` always reads back as `_`. A fuzz test (`FuzzSetText`) checks that any text reads back exactly, before and after a save.
  - Control characters XML can't hold (all below U+0020 except tab, line feed and carriage return, plus U+FFFE and U+FFFF) are stored as `_xHHHH_` escapes, as the spec requires. Written raw, they would turn into `�` on save.
  - Text over Excel's limit of 32,767 characters per cell is refused, not cut short. The limit is checked on the text as stored, escapes included, because excelize cuts longer text without an error. A CSV with a longer field is refused on open.
  - **Shared-string mix-ups are caught before writing.** excelize looks up a new shared string by its text, and can find an entry that isn't exactly that text in two ways:
    - An entry in the file can have both a plain part and runs (formatted runs, or a phonetic reading guide). excelize files it under the plain part only, so typing `abc` could get the entry `abc` + bold `def` and read back as `abcdef`, or get another cell's reading guide. `textStore` lists these entries when the file is opened. If the file's shared strings can't be read, text edits are refused.
    - An entry added in this session is filed under its own escaped form (`bstrMarshal`), so one text can be pointed at another's entry (e.g. `_x005F_x0041_` after `_x0041_`). Only `SetCellStr` adds to that lookup, and MinXLSX only calls it from `textStore`, which keeps its own copy of every key it may have added. (`sharedStringKey` is a copy of excelize's `bstrMarshal`, credited in `internal/model/LICENSE.excelize`; `FuzzSharedStringKey` checks the copy against excelize's behaviour. Recheck both when upgrading excelize.)
    - A text that would get the wrong entry is stored as its own rich-text entry instead, which skips the lookup.
    - Rich text can't hold control characters or some overlapping escape-like text. A text that would get the wrong entry and that rich text can't hold either is refused before anything is written ("this text can't be stored exactly"), so the cell is left exactly as it was, rich text included. Such texts exist, but only after another text has been stored whose key they match. Two examples:
      - `_x0001\x02` after `\x01x0002_` (with `\x01` and `\x02` control characters)
      - the plain text `_x0001_x005F_x0000` after `\x01x005F_x0000`: no control characters in the refused text itself, but rich text can't hold its overlapping escape-like text
      They need control characters or escape-like text that real files are unlikely to have.
    - A CSV field that can't be stored exactly makes the CSV refuse to open.
    - The copy of the keys can hold more than excelize's (when the lookup found an entry from the file, no key was added), never fewer. The extra keys can only send a few more texts to rich text, or to refusal if rich text can't hold them, when the file already holds escaped text and its double-escaped form is typed.
    - Every stored text is read back after it is stored, as a last check. A mismatch fails the edit, and the cell is put back as far as `Restore` can.
- **Date formatting:** a cell that already has a date or time format keeps it. Otherwise the cell keeps its style (font, fill, borders) and gets `yyyy-mm-dd` (or `yyyy-mm-dd hh:mm[:ss]`) as its number format. Known limits:
  - excelize makes the new format a new cell format, so the cell loses its link to a named cell style (like "Heading 1") and its "quote prefix" flag; the look stays the same.
  - If making the new cell format fails (e.g. the workbook's table of cell formats is full), the edit fails and the cell is untouched, but excelize may already have added the number format (and a font or fill) to the style sheet. Those unused entries are saved with the file; nothing shows them.

**Undo:** every edit is a command on an undo stack from milestone 3, as CLAUDE.md requires. Milestone 4 adds the Undo/Redo buttons and Ctrl+Z/Y. Unsaved state is "the stack isn't where it was at the last save", so undoing back to the saved state counts as clean.
- Each edit reports whether it wrote to the cell. One that fails before writing (a refused text, a full table of cell formats) leaves the cell exactly as it was, and so does a refused undo. Nothing else changes either: a shared-formula group around the cell isn't rewritten, and the file isn't marked unsaved. One that fails after writing puts the cell back as far as `Restore` can: rich-text formatting, a formula's cached value, `t="d"` dates (see below) and error constants like `#N/A` (which come back as text) are not kept. A shared-formula group keeps its formulas either way.
- Known limits of undo (`Restore`) for dates some apps store as ISO text (`t="d"`) instead of a number. They come back as the same date, stored as a number, except:
  - a date with a timezone offset is converted to UTC, so its time shifts
  - a time without a date comes back as text
  - in a 1904-based workbook, a date before 1904 comes back as a negative number

  Excel itself never writes `t="d"`.

## File fidelity and data safety

The app must never silently destroy a user's file. excelize keeps most workbook parts, but no library round-trips everything Excel can make, so the app checks for risky content and warns before saving.

- **No autosave:** every change (cell edits, structure edits, column widths) stays in memory until the user saves, with Ctrl+S, the toolbar's Save button, or File → Save / Save As. The file on disk only changes on an explicit save. Closing the window, or quitting, with unsaved changes asks first.
- **Detect before editing:** on open, scan the .xlsx zip for `drawings/`, `charts/`, `pivotTables/` and `vbaProject.bin`; if found, show a banner: "This file contains items that may change on save."
- **Atomic saves:** a crash or a pulled USB stick must never leave a half-written file. Every save goes through `writeFileAtomic` in `internal/model/atomic.go`, in this order:
  1. Write a temp file in the same folder.
  2. `Sync` it.
  3. `Close` it and check the error.
  4. Copy the original's permissions onto it. A new file gets 0666 minus the umask, never excelize's executable 0777.
  5. Rename it over the original.
  6. Sync the folder (Linux; Windows has no folder sync).

  If any step before the rename fails, the temp file is removed and the original is untouched. Tests check that a failed write leaves the original's bytes and permissions as they were, with no temp file left. If only step 6 fails, the file is saved: the error matches `ErrSavedNotSynced`, the workbook counts as saved, and the UI shows it as a warning.

  Known limit: the saved file is a new file renamed into place, so only its permissions are copied from the original. These are lost:
  - hard links (the other names keep the old content)
  - the owner and group, if the user saving isn't the owner
  - ACLs and extended attributes
  - Windows alternate data streams, including the "downloaded from the internet" mark
- **Files open in another program (Windows):** Excel locks a file it has open, so the rename fails with "access denied", which MinXLSX currently reports as read-only. A sharing violation (`ERROR_SHARING_VIOLATION`) isn't recognised at all. Known limit: the UI should say "the file is open in another program" instead; to do in milestone 6.
- **Read-only files:** saving over a read-only file (no owner-write permission, or the Windows read-only flag) is refused before anything is written, even when running as root. On Linux the OS is also asked whether the file is writable, which covers files owned by someone else. The app then offers Save As. A permission error from the folder, or a write-protected disk (for example a USB stick with its lock switch on), gets the same offer. On Linux a rename only needs a writable folder, so without this check a read-only file would be replaced silently.
- **Symlinks:** saving through a symlink writes to the link's target (temp file in the target's folder, the target's permissions) and keeps the link. A link whose target is missing is an error.
- **Save As keeps the file type:** .xlsx saves as .xlsx and .xlsm as .xlsm, because switching would change or drop the macro part. A .csv can be saved as .csv or .xlsx.
- **Shared formulas:** editing the first cell of a shared formula (the master, which holds the formula text the other cells reuse) would make excelize delete the formula from every cell in the group. Before such an edit, the other cells get their own plain copy of the formula. excelize finds the group by its id, not its range, so this covers every cell with the master's id, even one outside the range. A scan of each sheet's XML on open finds the masters and groups.
- **Array formulas:** one cell of an array formula (or a data table) can't be edited on its own, as in Excel; the edit is refused with a message naming the range. Editing the whole range waits for range selection (v1.1).
- **Broken sheets:** the same scan checks that each sheet's XML is well-formed. excelize reads a broken sheet as an empty one, so a file with a broken sheet opens for viewing only, with editing and saving refused, so an empty sheet is never written over the user's data.
- **Stale formula values:** MinXLSX has no calculation engine yet (formula evaluation is "Later"). After an edit, formulas that depend on the edited cell keep showing their old value in MinXLSX, and a newly typed formula shows no value until the file is recalculated. Saving after an edit sets the workbook's `fullCalcOnLoad` flag, so Excel and LibreOffice recalculate everything when they open the file.
- **CSV files:** MinXLSX uses its own small CSV reader and writer, because Go's `encoding/csv` skips blank lines and rewrites line breaks inside cells.
  - Every cell is text, exactly as written, so "007" and "1.50" never change.
  - Every row the user didn't change is written back exactly as it was read: same quoting, same line ending, trailing empty fields and blank lines included. The model tracks which rows were edited and copies the others from the file, without asking excelize what it holds for them. An edited row that is back to its original fields (after an undo) gets its original text back too. Changed or new rows are re-encoded, with quotes only where needed and the file's usual line ending. The BOM and the final newline (or its absence) are kept.
  - An unedited CSV saves back byte-for-byte. That's tested on both CSV fixtures and on a deliberately messy file (unneeded quotes, unquoted leading spaces, mixed line endings, blank lines, no final newline).
  - Only UTF-8 is accepted; other encodings are refused on open rather than garbled.
- **Backup on first save:** keep `name.xlsx.bak` from the session's first save.
- **Formulas stay formulas:** the grid shows the cached value (`GetCellValue`); the formula bar shows the formula (`GetCellFormula`, through `EditText`). Both come from one load.
- **Macro files:** open .xlsm and save back as .xlsm, keeping the macro part untouched.
- **Large files:** use excelize's row iterator to load big sheets in pages instead of all at once.

## Testing

Round-trip tests are the safety net: every change must keep "open → save → reopen" lossless.

- **Fixture files** in `testdata/`: plain data, formulas, dates, merged cells, styled cells, multiple sheets, a macro-enabled `.xlsm`, two CSVs (plain, and CRLF + BOM as Excel writes them), real Excel-made files from Apache POI's test suite, one saved by ONLYOFFICE, and one with a chart (to test the warning). A LibreOffice-saved file is optional.
- **Round-trip tests:** open each fixture, save to a temp path, reopen, and compare every cell value, formula, merge and style.
- **Model tests:** edits, undo/redo, row/column insert and delete, sheet operations.
- **UI tests** with Fyne's headless `test` package: typing in a cell, keyboard navigation, the unsaved-changes prompt.
- **CI:** GitHub Actions runs `golangci-lint` and `go test` on Windows and Linux for every pull request and every push to main or a v* tag, then builds both executables on release.

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
   - Built as two PRs: **3a** is the model (save target rules, typed input, editing, save and round-trip tests, CSV); **3b** is the controller and UI (undo stack, formula-bar editing, Save/Save As, prompts). Each gets its own review.
   - From the milestone 2 review: excelize reads a worksheet part full of broken XML as an empty sheet, with no error. Decided: such a file opens for viewing only (see "Broken sheets" above).
   - Before starting (from the milestone 1 review):
     - Excel-made fixtures come from Apache POI's test files (Apache-2.0): `excel-budget`, `excel-shared-formulas`, `excel-hidden-sheet`, `excel-unicode` and `excel-chart` (all `.xlsx`). `onlyoffice-bankbook.xlsx` is saved by ONLYOFFICE (tables with structured references, a shared `TODAY()` formula). A LibreOffice-saved file is optional, not required. See `testdata/README.md`.
     - From the ONLYOFFICE fixture: round-trip tests must check that every table's name (`displayName`) and range survive a save, since the fixture's structured references depend on them.
     - Done: macro fixture `macro.xlsm`, built with `AddVBAProject` from `testdata/vbaProject.bin` (excelize's own test file, BSD-3-Clause). The round-trip tests must prove its `xl/vbaProject.bin` stays byte-identical after a save.
     - Done: CSV fixture `crlf-bom.csv`, with CRLF line endings and a UTF-8 BOM, as Excel writes them. It holds the same rows as `plain.csv`. Go's `encoding/csv` doesn't strip the BOM, so the CSV loader must.
     - Done: the styled-cell check compares each border's side, style and colour.
     - Done: `.golangci.yml` enables `errcheck`, `gosec`, `bodyclose` and `errorlint` on top of the standard set. errcheck uses `check-blank: true`, so `_ = f.Close()` fails lint as well. Tests are exempt only from gosec's file path and permission rules, and may discard errors with `_ =`.
   - Done: the atomic-save helper `writeFileAtomic` (see "Atomic saves" above), with tests. Save must call it with excelize's `Write`, never `SaveAs`. `SaveAs` writes straight over the target, and it creates new files with mode 0777 minus the umask, so on Linux they come out executable.
   - Decided: saving over a read-only file refuses and offers Save As; saving through a symlink writes to the link's target and keeps the link (see "Read-only files" and "Symlinks" above).
4. **Undo/redo:** Undo/Redo buttons and Ctrl+Z/Y on the undo stack from milestone 3, and commands for the structure edits
5. **Structure edits:** insert/delete rows and columns, add/rename/delete/reorder sheets, unhide hidden sheets
   - From the milestone 3a review: in a CSV, inserting or deleting rows must shift the model's edited-row marks (`csvFile.edited`) and the original records in step, or saving would copy the wrong rows from the file.
6. **Safety:** risky-content warning, `.bak` backup, paged loading for large files
   - Paged loading must not bring back excelize's temp files in the OS temp folder (see milestone 2); point `Options.TmpDir` beside the executable if it needs them.
   - From the milestone 3a review: on Windows, report a file open in another program (access denied on rename, `ERROR_SHARING_VIOLATION`) as such, not as read-only (see "Files open in another program" above). Also see issue #7: saving in OneDrive folders may be refused as "not a regular file".
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
