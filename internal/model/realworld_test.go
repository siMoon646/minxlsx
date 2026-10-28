package model

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

// realFixture describes a file saved by a real spreadsheet app (not generated
// by us). See testdata/README.md for where each one comes from.
type realFixture struct {
	name   string
	sheets []string          // expected sheet names, in order
	hidden []string          // sheets that must be hidden; all others visible
	cells  map[string]string // "Sheet!A1" -> raw stored value (no number formatting)
	forms  map[string]string // "Sheet!A1" -> formula (without leading '=')
	merges map[string]string // sheet -> merged ranges that must be present, comma separated
	names  []string          // defined names that must exist
	tables []string          // table ranges that must be present, e.g. "A2:C33"
	chart  bool              // file must contain a chart part
}

func realFixtures() []realFixture {
	return []realFixture{
		{
			name:   "excel-budget.xlsx",
			sheets: []string{"Simple Monthly Budget"},
			cells: map[string]string{
				"Simple Monthly Budget!A1": "SIMPLE MONTHLY BUDGET",
				"Simple Monthly Budget!B4": "Item",
				"Simple Monthly Budget!C4": "Amount",
				"Simple Monthly Budget!E4": "2336",
				"Simple Monthly Budget!E9": "3750",
				"Simple Monthly Budget!G9": "1414",
			},
			forms: map[string]string{
				"Simple Monthly Budget!E4": "TotalMonthlyExpenses",
				"Simple Monthly Budget!H4": "TotalMonthlyExpenses/TotalMonthlyIncome",
				"Simple Monthly Budget!G9": "TotalMonthlyIncome-TotalMonthlyExpenses",
			},
			merges: map[string]string{"Simple Monthly Budget": "E4:G5,H4:H5,B8:C8"},
			names:  []string{"TotalMonthlyExpenses", "TotalMonthlyIncome"},
			chart:  true,
		},
		{
			name:   "excel-shared-formulas.xlsx",
			sheets: []string{"Label"},
			cells: map[string]string{
				"Label!A1":  "Currently Using",
				"Label!A2":  "ProductionOrderConfirmation",
				"Label!B2":  "ProductionOrderConfirmation",
				"Label!A41": "Code",
			},
			// A2 holds the master formula of a shared group (A2:A41); the
			// others only reference it and must be expanded relative to it.
			forms: map[string]string{
				"Label!A2":  "B2",
				"Label!A3":  "B3",
				"Label!A41": "B41",
			},
		},
		{
			name:   "excel-hidden-sheet.xlsx",
			sheets: []string{"Sheet1", "Sheet2"},
			hidden: []string{"Sheet1"},
			cells: map[string]string{
				"Sheet1!A1": "Sheet1A1",
				"Sheet2!A1": "Sheet2A1",
			},
		},
		{
			name:   "excel-unicode.xlsx",
			sheets: []string{"Sheet1", "Sheet2", "Sheet3"},
			// Mathematical Greek letters, all outside the Basic Multilingual
			// Plane (stored as UTF-16 surrogate pairs by Excel).
			cells: map[string]string{
				"Sheet1!A1": "𝝊𝝋𝝌𝝍𝝎𝝏𝝐𝝑𝝒𝝓𝝔𝝕𝝖𝝗𝝘𝝙𝝚𝝛𝝜𝝝𝝞𝝟𝝠𝝡𝝢𝝣𝝤𝝥𝝦𝝧𝝨𝝩𝝪𝝫𝝬𝝭𝝮𝝯𝝰𝝱𝝲𝝳𝝴𝝵𝝶𝝷𝝸𝝹𝝺",
			},
		},
		{
			name:   "excel-chart.xlsx",
			sheets: []string{"Sheet1", "Sheet2", "Sheet3"},
			cells: map[string]string{
				"Sheet1!A1": "1", "Sheet1!B1": "10",
				"Sheet1!A3": "3", "Sheet1!B3": "14",
			},
			chart: true,
		},
		{
			name:   "onlyoffice-bankbook.xlsx",
			sheets: []string{"Sheet1"},
			cells: map[string]string{
				"Sheet1!A1": "One-Time-Purchase",
				"Sheet1!E1": "Ongoing Subscription",
				"Sheet1!A2": "Item",
			},
			// H3 is the master of a shared formula group (H3:H6) that uses
			// TODAY(); the K and Q cells use structured table references.
			forms: map[string]string{
				"Sheet1!H3": `(DATEDIF(G3,TODAY(),"m")+1)*F3`,
				"Sheet1!H6": `(DATEDIF(G6,TODAY(),"m")+1)*F6`,
				"Sheet1!K3": `(SUMIF(Table1[Item],"*Retail*",Table1[Cost]))`,
				"Sheet1!K5": `SUM(Table5[Total])+(SUMIF(Table1[Item],"*Subscription*",Table1[Cost]))`,
				"Sheet1!Q3": "SUM(Table2[Income])",
				"Sheet1!Q5": "Q3-Q4",
			},
			merges: map[string]string{"Sheet1": "A1:C1,E1:H1,J1:K1,M1:N1,P1:Q1"},
			// Table1, Table2, Table4, Table5 and Table7. ONLYOFFICE writes only
			// displayName (no name attribute), and excelize's GetTables reports
			// name, so the names come back empty; only ranges are checked.
			tables: []string{"A2:C33", "M2:N8", "J2:K8", "E2:H7", "P2:Q5"},
		},
	}
}

// TestRealWorldFixtures checks that each file saved by a real spreadsheet app
// opens with excelize and still reads back as expected.
func TestRealWorldFixtures(t *testing.T) {
	raw := excelize.Options{RawCellValue: true}
	for _, fx := range realFixtures() {
		t.Run(fx.name, func(t *testing.T) {
			f, err := excelize.OpenFile(filepath.Join(testdataDir, fx.name))
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			defer closeFile(t, f)

			if got := f.GetSheetList(); strings.Join(got, "|") != strings.Join(fx.sheets, "|") {
				t.Errorf("sheets = %q, want %q", got, fx.sheets)
			}
			for _, sheet := range fx.sheets {
				visible, err := f.GetSheetVisible(sheet)
				if err != nil {
					t.Errorf("%s visible: %v", sheet, err)
					continue
				}
				if want := !slices.Contains(fx.hidden, sheet); visible != want {
					t.Errorf("%s visible = %v, want %v", sheet, visible, want)
				}
			}
			for _, ref := range sortedKeys(fx.cells) {
				sheet, cell := splitRef(t, ref)
				got, err := f.GetCellValue(sheet, cell, raw)
				if err != nil {
					t.Errorf("%s: %v", ref, err)
					continue
				}
				if want := fx.cells[ref]; got != want {
					t.Errorf("%s = %q, want %q", ref, got, want)
				}
			}
			for _, ref := range sortedKeys(fx.forms) {
				sheet, cell := splitRef(t, ref)
				got, err := f.GetCellFormula(sheet, cell)
				if err != nil {
					t.Errorf("%s formula: %v", ref, err)
					continue
				}
				if want := fx.forms[ref]; got != want {
					t.Errorf("%s formula = %q, want %q", ref, got, want)
				}
			}
			for _, sheet := range sortedKeys(fx.merges) {
				merges, err := f.GetMergeCells(sheet)
				if err != nil {
					t.Errorf("%s merges: %v", sheet, err)
					continue
				}
				var got []string
				for _, m := range merges {
					got = append(got, m.GetStartAxis()+":"+m.GetEndAxis())
				}
				for _, want := range strings.Split(fx.merges[sheet], ",") {
					if !slices.Contains(got, want) {
						t.Errorf("%s merges = %q, missing %s", sheet, got, want)
					}
				}
			}
			var names []string
			for _, dn := range f.GetDefinedName() {
				names = append(names, dn.Name)
			}
			for _, want := range fx.names {
				if !slices.Contains(names, want) {
					t.Errorf("defined names = %q, missing %s", names, want)
				}
			}
			if len(fx.tables) > 0 {
				var got []string
				for _, sheet := range fx.sheets {
					tables, err := f.GetTables(sheet)
					if err != nil {
						t.Errorf("%s tables: %v", sheet, err)
						continue
					}
					for _, tb := range tables {
						got = append(got, strings.ReplaceAll(tb.Range, "$", ""))
					}
				}
				for _, want := range fx.tables {
					if !slices.Contains(got, want) {
						t.Errorf("table ranges = %q, missing %s", got, want)
					}
				}
			}
			if fx.chart && !hasPart(f, "xl/charts/chart") {
				t.Error("no xl/charts/chart*.xml part found")
			}
		})
	}
}

// hasPart reports whether the package has a part whose name starts with prefix.
func hasPart(f *excelize.File, prefix string) bool {
	found := false
	f.Pkg.Range(func(k, _ any) bool {
		if name, ok := k.(string); ok && strings.HasPrefix(name, prefix) {
			found = true
			return false
		}
		return true
	})
	return found
}
