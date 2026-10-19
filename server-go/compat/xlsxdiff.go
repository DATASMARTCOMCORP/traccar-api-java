package main

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
)

// Workbook contents for comparison: POI and Go write different bytes, but a report must
// have the same sheets, and each cell the same type, value, style and hyperlink.

type xlCell struct {
	Ref   string `xml:"r,attr"`
	Type  string `xml:"t,attr"`
	Style string `xml:"s,attr"`
	V     string `xml:"v"`
	F     string `xml:"f"`
	IS    struct {
		T []string `xml:"t"`
		R []struct {
			T string `xml:"t"`
		} `xml:"r"`
	} `xml:"is"`
}

type xlSheet struct {
	Rows []struct {
		R     string   `xml:"r,attr"`
		Cells []xlCell `xml:"c"`
	} `xml:"sheetData>row"`
	Merges []struct {
		Ref string `xml:"ref,attr"`
	} `xml:"mergeCells>mergeCell"`
	Links []struct {
		Ref string `xml:"ref,attr"`
		ID  string `xml:"http://schemas.openxmlformats.org/officeDocument/2006/relationships id,attr"`
	} `xml:"hyperlinks>hyperlink"`
}

// workbookCells reads a workbook into "sheet!ref" → "type:value" lines (blank cells
// dropped), sheet names in order, merges and hyperlink targets. Unordered (reports over
// several devices, which Java visits in HashSet order) compares the sheets as a set and
// each sheet's rows as a multiset: a row's key is its cells and links without the row
// number.
func workbookCells(data []byte, unordered bool) (map[string]string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		b, _ := io.ReadAll(rc)
		rc.Close()
		files[f.Name] = b
	}
	var shared struct {
		SI []struct {
			T []string `xml:"t"`
			R []struct {
				T string `xml:"t"`
			} `xml:"r"`
		} `xml:"si"`
	}
	if b, ok := files["xl/sharedStrings.xml"]; ok {
		if err := xml.Unmarshal(b, &shared); err != nil {
			return nil, err
		}
	}
	sharedText := func(i int) string {
		if i < 0 || i >= len(shared.SI) {
			return fmt.Sprintf("<bad shared string %d>", i)
		}
		si := shared.SI[i]
		var b strings.Builder
		for _, t := range si.T {
			b.WriteString(t)
		}
		for _, r := range si.R {
			b.WriteString(r.T)
		}
		return b.String()
	}
	var wb struct {
		Sheets []struct {
			Name string `xml:"name,attr"`
			ID   string `xml:"http://schemas.openxmlformats.org/officeDocument/2006/relationships id,attr"`
		} `xml:"sheets>sheet"`
	}
	if err := xml.Unmarshal(files["xl/workbook.xml"], &wb); err != nil {
		return nil, err
	}
	rels := func(name string) map[string]string {
		var r struct {
			Rel []struct {
				ID     string `xml:"Id,attr"`
				Target string `xml:"Target,attr"`
			} `xml:"Relationship"`
		}
		_ = xml.Unmarshal(files[name], &r)
		m := map[string]string{}
		for _, x := range r.Rel {
			m[x.ID] = x.Target
		}
		return m
	}
	wbRels := rels("xl/_rels/workbook.xml.rels")
	out := map[string]string{}
	var names []string
	rowKey := map[string]string{} // sheet!row → its cells, for unordered
	count := func(key string) {
		n := 0
		fmt.Sscan(out[key], &n)
		out[key] = fmt.Sprint(n + 1)
	}
	for _, sh := range wb.Sheets {
		names = append(names, sh.Name)
		target := wbRels[sh.ID]
		target = strings.TrimPrefix(target, "/xl/")
		file := path.Join("xl", target)
		var s xlSheet
		if err := xml.Unmarshal(files[file], &s); err != nil {
			return nil, fmt.Errorf("%s: %w", file, err)
		}
		for _, row := range s.Rows {
			for _, c := range row.Cells {
				v := c.V
				switch c.Type {
				case "s":
					var i int
					fmt.Sscan(c.V, &i)
					v = sharedText(i)
				case "inlineStr":
					v = strings.Join(c.IS.T, "")
					for _, r := range c.IS.R {
						v += r.T
					}
				}
				if c.V == "" && c.F == "" && c.Type != "inlineStr" {
					continue
				}
				typ := c.Type
				if typ == "" || typ == "n" {
					typ = "n"
				} else if typ == "s" || typ == "inlineStr" || typ == "str" {
					typ = "s"
				}
				line := typ + ":" + v + " f=" + c.F + " style=" + c.Style
				if unordered {
					col := strings.TrimRight(c.Ref, "0123456789")
					rowKey[sh.Name+"!"+row.R] += " " + col + "=" + line
					continue
				}
				out[sh.Name+"!"+c.Ref] = line
			}
		}
		sheetRels := rels(path.Join(path.Dir(file), "_rels", path.Base(file)+".rels"))
		for _, l := range s.Links {
			if unordered {
				col := strings.TrimRight(l.Ref, "0123456789")
				rowKey[sh.Name+"!"+strings.TrimLeft(l.Ref, "ABCDEFGHIJKLMNOPQRSTUVWXYZ")] += " " + col + "→" + sheetRels[l.ID]
				continue
			}
			out[sh.Name+"!link:"+l.Ref] = sheetRels[l.ID]
		}
		for _, m := range s.Merges {
			if unordered {
				count(sh.Name + "!merge:" + strings.Map(func(r rune) rune {
					if r >= '0' && r <= '9' {
						return -1
					}
					return r
				}, m.Ref))
				continue
			}
			out[sh.Name+"!merge:"+m.Ref] = "merged"
		}
	}
	for k, cells := range rowKey {
		sheet, _, _ := strings.Cut(k, "!")
		count(sheet + "!row:" + cells)
	}
	if unordered {
		sort.Strings(names)
	}
	out["sheets"] = strings.Join(names, " | ")
	return out, nil
}

// diffXLSX compares two workbooks' contents.
func diffXLSX(a, b []byte, unordered bool) []string {
	zipA, zipB := bytes.HasPrefix(a, []byte("PK")), bytes.HasPrefix(b, []byte("PK"))
	if !zipA || !zipB { // an error or an empty answer
		if zipA != zipB || !bytes.Equal(a, b) {
			return []string{fmt.Sprintf("body: A=%q B=%q", clip(a), clip(b))}
		}
		return nil
	}
	ca, errA := workbookCells(a, unordered)
	cb, errB := workbookCells(b, unordered)
	if errA != nil || errB != nil {
		return []string{fmt.Sprintf("xlsx: A=%v B=%v", errA, errB)}
	}
	keys := map[string]bool{}
	for k := range ca {
		keys[k] = true
	}
	for k := range cb {
		keys[k] = true
	}
	var sorted []string
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)
	var out []string
	for _, k := range sorted {
		if ca[k] != cb[k] {
			out = append(out, fmt.Sprintf("xlsx %s: A=%q B=%q", k, ca[k], cb[k]))
		}
		if len(out) >= 30 {
			out = append(out, "xlsx: more differences omitted")
			break
		}
	}
	return out
}
