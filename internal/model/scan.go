package model

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"

	"github.com/xuri/excelize/v2"
)

// sheetScan is what the open-time scan found in one worksheet's XML.
type sheetScan struct {
	// masters maps the master cell of each shared formula (the cell that
	// holds the formula text the group reuses) to the group's range, e.g.
	// "A2" -> "A2:A41". Editing a master needs care: see unshare.
	masters map[string]string
}

// scanSheets reads every worksheet's raw XML once, before excelize parses
// it. It finds shared-formula masters and checks the XML is well-formed:
// excelize reads a broken sheet as an empty one, and saving would then
// write that empty sheet over the user's data. The returned reason is
// non-empty if the file must stay read-only.
func scanSheets(f *excelize.File) (scans map[string]sheetScan, readOnlyReason string) {
	parts, err := sheetParts(f)
	if err != nil {
		return nil, "couldn't check the file's structure: " + err.Error()
	}
	scans = map[string]sheetScan{}
	var broken []string
	for _, name := range f.GetSheetList() {
		part, ok := parts[name]
		if !ok {
			return nil, fmt.Sprintf("couldn't find the XML for sheet %q", name)
		}
		data, ok := rawPart(f, part)
		if !ok {
			return nil, fmt.Sprintf("couldn't read the XML for sheet %q", name)
		}
		s, err := scanSheet(data)
		if err != nil {
			broken = append(broken, name)
			continue
		}
		scans[name] = s
	}
	if len(broken) > 0 {
		return nil, fmt.Sprintf("sheet %q is damaged (its XML can't be read), so the file is open for viewing only", strings.Join(broken, `", "`))
	}
	return scans, ""
}

// scanSheet streams one worksheet's XML. It fails if the XML isn't
// well-formed.
func scanSheet(data []byte) (sheetScan, error) {
	s := sheetScan{masters: map[string]string{}}
	d := xml.NewDecoder(bytes.NewReader(data))
	row, col := 0, 0 // current cell, 1-based; cells may omit their r attribute
	cell := ""
	for {
		tok, err := d.Token()
		if errors.Is(err, io.EOF) {
			return s, nil
		}
		if err != nil {
			return sheetScan{}, err
		}
		el, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch el.Name.Local {
		case "row":
			row++
			if r, err := strconv.Atoi(attr(el, "r")); err == nil {
				row = r
			}
			col = 0
		case "c":
			col++
			if ref := attr(el, "r"); ref != "" {
				if c, r, err := excelize.CellNameToCoordinates(ref); err == nil {
					col, row = c, r
				}
			}
			cell = CellName(row-1, col-1) // "" if out of range
		case "f":
			if attr(el, "t") == "shared" {
				if ref := attr(el, "ref"); ref != "" && cell != "" {
					s.masters[cell] = ref
				}
			}
		}
	}
}

func attr(el xml.StartElement, name string) string {
	for _, a := range el.Attr {
		if a.Name.Space == "" && a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

// sheetParts maps each sheet name to its part in the zip (e.g.
// "xl/worksheets/sheet1.xml"), following the package relationships the
// same way excelize does.
func sheetParts(f *excelize.File) (map[string]string, error) {
	type rel struct {
		ID     string `xml:"Id,attr"`
		Type   string `xml:"Type,attr"`
		Target string `xml:"Target,attr"`
	}
	type rels struct {
		Rels []rel `xml:"Relationship"`
	}
	readRels := func(part string) ([]rel, error) {
		data, ok := rawPart(f, part)
		if !ok {
			return nil, fmt.Errorf("missing %s", part)
		}
		var r rels
		if err := xml.Unmarshal(data, &r); err != nil {
			return nil, fmt.Errorf("%s: %w", part, err)
		}
		return r.Rels, nil
	}

	rootRels, err := readRels("_rels/.rels")
	if err != nil {
		return nil, err
	}
	workbook := ""
	for _, r := range rootRels {
		if strings.HasSuffix(r.Type, "/officeDocument") {
			workbook = strings.TrimPrefix(r.Target, "/")
		}
	}
	if workbook == "" {
		return nil, errors.New("no workbook in the package")
	}
	dir := path.Dir(workbook)
	wbRels, err := readRels(path.Join(dir, "_rels", path.Base(workbook)+".rels"))
	if err != nil {
		return nil, err
	}
	targets := map[string]string{}
	for _, r := range wbRels {
		if strings.HasPrefix(r.Target, "/") {
			targets[r.ID] = strings.TrimPrefix(r.Target, "/")
		} else {
			targets[r.ID] = path.Join(dir, r.Target)
		}
	}

	data, ok := rawPart(f, workbook)
	if !ok {
		return nil, fmt.Errorf("missing %s", workbook)
	}
	parts := map[string]string{}
	d := xml.NewDecoder(bytes.NewReader(data))
	for {
		tok, err := d.Token()
		if errors.Is(err, io.EOF) {
			return parts, nil
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", workbook, err)
		}
		el, ok := tok.(xml.StartElement)
		if !ok || el.Name.Local != "sheet" {
			continue
		}
		var id string
		for _, a := range el.Attr {
			// r:id, in the transitional or strict relationships namespace.
			if a.Name.Local == "id" && strings.Contains(a.Name.Space, "relationships") {
				id = a.Value
			}
		}
		if target, ok := targets[id]; ok {
			parts[attr(el, "name")] = target
		}
	}
}

// rawPart returns a part's bytes as read from the zip. Every part is kept
// in memory (see openOptions).
func rawPart(f *excelize.File, name string) ([]byte, bool) {
	v, ok := f.Pkg.Load(name)
	if !ok {
		return nil, false
	}
	data, ok := v.([]byte)
	return data, ok
}
