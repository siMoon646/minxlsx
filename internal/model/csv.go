package model

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/xuri/excelize/v2"
)

// utf8BOM is the byte-order mark Excel writes at the start of "CSV UTF-8"
// (U+FEFF, written as bytes so no editor turns it into an invisible
// literal character).
const utf8BOM = "\xef\xbb\xbf"

// csvRecord is one CSV row as read from the file: its exact text, its line
// ending ("\r\n", "\n", or "" for a last line without one) and its fields.
type csvRecord struct {
	raw    string
	eol    string
	fields []string
}

// csvFile is what a CSV workbook remembers about its file, so an unedited
// row is written back byte for byte.
type csvFile struct {
	bom     bool
	records []csvRecord
}

// openCSV loads a UTF-8 CSV file as a one-sheet workbook. Every cell is
// text, exactly as written.
func openCSV(path string) (*Workbook, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	bom := bytes.HasPrefix(data, []byte(utf8BOM))
	text := strings.TrimPrefix(string(data), utf8BOM)
	if !utf8.ValidString(text) {
		return nil, fmt.Errorf("open %s: not UTF-8 text (save it as \"CSV UTF-8\" and try again)", path)
	}
	records := parseCSV(text)
	if len(records) > maxRows {
		return nil, fmt.Errorf("open %s: %d rows, more than a sheet can hold (%d)", path, len(records), maxRows)
	}

	f := excelize.NewFile(openOptions(""))
	sheet := csvSheetName(path)
	fail := func(err error) (*Workbook, error) {
		return nil, errors.Join(fmt.Errorf("open %s: %w", path, err), f.Close())
	}
	if sheet != "Sheet1" {
		if err := f.SetSheetName("Sheet1", sheet); err != nil {
			return fail(err)
		}
	}
	for r, rec := range records {
		if len(rec.fields) > maxCols {
			return fail(fmt.Errorf("row %d has %d fields, more than a sheet can hold (%d)", r+1, len(rec.fields), maxCols))
		}
		for c, field := range rec.fields {
			if field == "" {
				continue
			}
			if err := f.SetCellStr(sheet, CellName(r, c), field); err != nil {
				return fail(fmt.Errorf("row %d: %w", r+1, err))
			}
		}
	}
	return &Workbook{
		path:   path,
		file:   f,
		sheets: map[string]sheetData{},
		scan:   map[string]sheetScan{},
		csv:    &csvFile{bom: bom, records: records},
	}, nil
}

// parseCSV splits CSV text into records (RFC 4180, comma-separated). It
// keeps each record's exact text and line ending, keeps blank lines and
// line breaks inside quoted cells, and never fails: a stray quote is read
// the way spreadsheet apps read it.
func parseCSV(s string) []csvRecord {
	var records []csvRecord
	i := 0
	for i < len(s) {
		start := i
		var fields []string
		for {
			var field strings.Builder
			if i < len(s) && s[i] == '"' {
				i++
				for i < len(s) {
					if s[i] == '"' {
						if i+1 < len(s) && s[i+1] == '"' {
							field.WriteByte('"')
							i += 2
							continue
						}
						i++
						break
					}
					field.WriteByte(s[i])
					i++
				}
			}
			// Unquoted text, or anything after a closing quote.
			for i < len(s) && s[i] != ',' && s[i] != '\n' && !strings.HasPrefix(s[i:], "\r\n") {
				field.WriteByte(s[i])
				i++
			}
			fields = append(fields, field.String())
			if i < len(s) && s[i] == ',' {
				i++
				continue
			}
			break
		}
		rec := csvRecord{raw: s[start:i], fields: fields}
		switch {
		case strings.HasPrefix(s[i:], "\r\n"):
			rec.eol = "\r\n"
		case strings.HasPrefix(s[i:], "\n"):
			rec.eol = "\n"
		}
		i += len(rec.eol)
		records = append(records, rec)
	}
	return records
}

// encodeCSVField quotes a field only when needed, with the same rule as
// Go's encoding/csv.
func encodeCSVField(field string) string {
	needsQuotes := field == `\.` || strings.ContainsAny(field, ",\"\r\n")
	if r, _ := utf8.DecodeRuneInString(field); field != "" && unicode.IsSpace(r) {
		needsQuotes = true
	}
	if !needsQuotes {
		return field
	}
	return `"` + strings.ReplaceAll(field, `"`, `""`) + `"`
}

// writeCSV writes the sheet as CSV. A row whose fields are unchanged is
// written exactly as it was read; a changed or new row is encoded with the
// file's usual line ending. The file keeps its BOM and its final newline
// (or lack of one).
func (wb *Workbook) writeCSV(w io.Writer) error {
	sheet := wb.file.GetSheetList()[0]
	rows, err := wb.file.GetRows(sheet, excelize.Options{RawCellValue: true})
	if err != nil {
		return err
	}
	c := wb.csv
	eol, finalEOL := "\r\n", "\r\n"
	if n := len(c.records); n > 0 {
		finalEOL = c.records[n-1].eol
		eol = "\n"
		for _, rec := range c.records {
			if rec.eol == "\r\n" {
				eol = "\r\n"
				break
			}
		}
	}

	var b strings.Builder
	if c.bom {
		b.WriteString(utf8BOM)
	}
	n := max(len(c.records), len(rows))
	for i := range n {
		var cells []string
		if i < len(rows) {
			cells = rows[i]
		}
		var rec *csvRecord
		if i < len(c.records) {
			rec = &c.records[i]
		}
		if rec != nil && sameFields(rec.fields, cells) {
			b.WriteString(rec.raw)
		} else {
			width := len(cells)
			if rec != nil {
				width = max(width, len(rec.fields))
			}
			for j := range width {
				if j > 0 {
					b.WriteByte(',')
				}
				if j < len(cells) {
					b.WriteString(encodeCSVField(cells[j]))
				}
			}
		}
		switch {
		case i == n-1:
			b.WriteString(finalEOL)
		case rec != nil && rec.eol != "":
			b.WriteString(rec.eol)
		default:
			b.WriteString(eol)
		}
	}
	_, err = io.WriteString(w, b.String())
	return err
}

// sameFields compares a record's fields with a row from GetRows, which
// drops trailing empty cells.
func sameFields(fields, cells []string) bool {
	trim := func(s []string) []string {
		for len(s) > 0 && s[len(s)-1] == "" {
			s = s[:len(s)-1]
		}
		return s
	}
	f, c := trim(fields), trim(cells)
	if len(f) != len(c) {
		return false
	}
	for i := range f {
		if f[i] != c[i] {
			return false
		}
	}
	return true
}

// csvSheetName names the sheet after the file, as Excel does: at most 31
// characters, none of : \ / ? * [ ], and no leading or trailing apostrophe.
func csvSheetName(path string) string {
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	name = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`:\/?*[]`, r) {
			return '_'
		}
		return r
	}, name)
	if runes := []rune(name); len(runes) > 31 {
		name = string(runes[:31])
	}
	name = strings.Trim(name, "'")
	if name == "" {
		return "Sheet1"
	}
	return name
}
