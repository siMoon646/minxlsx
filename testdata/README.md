# Test fixtures

Fixtures for round-trip and model tests. The "generated" files are made by
`internal/model/fixtures_test.go` using excelize. The "real-world" files were
saved by real spreadsheet apps and are checked by
`internal/model/realworld_test.go`.

## Regenerate

```sh
go test ./internal/model -run TestGenerateFixtures -update
```

Without `-update` that test is skipped. `TestFixtures` and
`TestFixtureDetails` always run and check that every fixture opens and
contains the expected sheets, values, formulas, merges and styles.

Output is byte-for-byte reproducible (fixed dates, fixed doc properties),
except `chart.xlsx`: excelize writes the image content types in
`[Content_Types].xml` in random map order, so its bytes change between runs
even though the content is the same. Don't commit a regenerated
`chart.xlsx` unless something in it actually changed.

## Generated fixtures

| File | Contents |
| --- | --- |
| `plain.xlsx` | Strings, ints, floats, negatives, booleans, an empty cell, leading/trailing spaces, a newline in a cell, numeric-looking text (`"007"`) |
| `formulas.xlsx` | `SUM`, `IF` with strings, `CONCATENATE`, absolute refs, sheet-qualified ref, `1/0` error formula (no cached values) |
| `dates.xlsx` | 2024-03-15 13:45:30 in several number formats (date, d/m/y, time, datetime), plus 1900-01-01 and 2099-12-31 |
| `merged.xlsx` | Merged ranges `A1:D1` (horizontal), `A3:A6` (vertical), `C3:E5` (block) |
| `styled.xlsx` | Bold, italic + red font, yellow fill, thin borders, centred + wrapped alignment, number formats `#,##0.00`, `0.00%`, `$#,##0.00`, `0000`, `0.00E+00`, an unstyled control cell, custom column width |
| `multisheet.xlsx` | Sheets `Summary`, `Data 2024` (space in name), `Empty`, `Hidden` (hidden); cross-sheet formula `SUM('Data 2024'!B2:B4)` |
| `unicode.xlsx` | Sheet `Ünïcödé 表`; German, Japanese, Arabic (RTL), emoji, Greek with `& < > "`, a zero-width space |
| `empty.xlsx` | One sheet, no cells |
| `large.xlsx` | 5,000 data rows × 3 columns plus a header (~100 KB), written with the stream writer |
| `chart.xlsx` | Small table with a clustered column chart (for the risky-content warning) |
| `plain.csv` | UTF-8 CSV with a quoted comma, embedded quotes, a newline inside a field, an empty field and non-ASCII text |

## Real-world fixtures

These must come from the real applications; never generate or fake them.
`TestRealWorldFixtures` checks that each one opens and still has its key
sheets, values, formulas, merges, defined names and charts. When adding a file,
add an entry to `realFixtures()` in `internal/model/realworld_test.go`.

### From Apache POI (saved by Microsoft Excel)

Copied unchanged from the [Apache POI](https://poi.apache.org/) test suite
(`test-data/spreadsheet/` in <https://github.com/apache/poi>, trunk at
commit `07f1631dd6`), renamed for this repo. Apache POI is Copyright The
Apache Software Foundation and licensed under the
[Apache License 2.0](https://www.apache.org/licenses/LICENSE-2.0).

| File | Original POI file name | What it covers |
| --- | --- | --- |
| `excel-budget.xlsx` | `simple-monthly-budget.xlsx` | Excel template: formulas using defined names, merged cells, styles, a chart |
| `excel-shared-formulas.xlsx` | `shared_formulas.xlsx` | One shared formula (`A2:A41`) that later cells reference instead of repeating |
| `excel-hidden-sheet.xlsx` | `TwoSheetsOneHidden.xlsx` | Two sheets; the first is hidden |
| `excel-unicode.xlsx` | `54084 - Greek - beyond BMP.xlsx` | Text outside the Basic Multilingual Plane (Mathematical Greek letters) |
| `excel-chart.xlsx` | `WithChart.xlsx` | Small table with a chart, three sheets |

### Saved by ONLYOFFICE

Made by the project author in ONLYOFFICE 9.4 and anonymised (made-up item
names and amounts).

| File | What it covers |
| --- | --- |
| `onlyoffice-bankbook.xlsx` | Five tables with structured references (`SUMIF(Table1[Item],…)`, `SUM(Table2[Income])`), a shared formula using `DATEDIF` and `TODAY()`, merged header cells, dates. ONLYOFFICE writes table `displayName` only, so excelize's `GetTables` returns empty table names. |

### Optional

| File | How to make it |
| --- | --- |
| `libreoffice.xlsx` | Saved by LibreOffice Calc as "Excel 2007-365 (.xlsx)". Include text, numbers, a date, a formula, a merged range, a bold/filled cell and two sheets. |
