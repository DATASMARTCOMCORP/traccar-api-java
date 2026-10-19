// Package xlsx fills the report templates in templates/export the way JXLS 2 does for
// Traccar: jx:area and jx:each commands in cell comments, ${...} JEXL expressions in
// cells, and multisheet output (one sheet per item, the template sheet dropped).
//
// Only what the templates use is supported: commands that span whole rows, one area per
// workbook, and a single template sheet.
package xlsx

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type xmlCell struct {
	Ref   string `xml:"r,attr"`
	Style string `xml:"s,attr"`
	Type  string `xml:"t,attr"`
	V     string `xml:"v"`
	F     string `xml:"f"`
	IS    *struct {
		Inner string `xml:",innerxml"`
	} `xml:"is"`
}

type xmlRow struct {
	R     int        `xml:"r,attr"`
	Attrs []xml.Attr `xml:",any,attr"`
	Cells []xmlCell  `xml:"c"`
}

// command is a jx:each (or jx:area, with no items) spanning rows first..last.
type command struct {
	first, last int
	items, name string
	multisheet  string
}

type template struct {
	files     map[string][]byte
	order     []string // zip entry order
	sheetPath string   // xl/worksheets/sheetN.xml
	sheetName string
	sheetXML  string
	rows      map[int]*xmlRow
	maxRow    int
	merges    [][2]string // single-row merges by their first/last cell
	shared    []string    // raw <si> elements
	texts     []string    // their text
	area      command
	eaches    []command
}

var (
	reCommand = regexp.MustCompile(`jx:(area|each)\(([^)]*)\)`)
	reParam   = regexp.MustCompile(`(\w+)\s*=\s*"([^"]*)"`)
	reSI      = regexp.MustCompile(`(?s)<si>.*?</si>|<si/>`)
	reMerge   = regexp.MustCompile(`<mergeCell ref="([A-Z]+)(\d+):([A-Z]+)(\d+)"\s*/>`)
)

func loadTemplate(file string) (*template, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	t := &template{files: map[string][]byte{}, rows: map[int]*xmlRow{}}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, err
		}
		t.files[f.Name] = b
		t.order = append(t.order, f.Name)
	}

	var wb struct {
		Sheets []struct {
			Name string `xml:"name,attr"`
			ID   string `xml:"http://schemas.openxmlformats.org/officeDocument/2006/relationships id,attr"`
		} `xml:"sheets>sheet"`
	}
	if err := xml.Unmarshal(t.files["xl/workbook.xml"], &wb); err != nil {
		return nil, err
	}
	if len(wb.Sheets) != 1 {
		return nil, fmt.Errorf("xlsx: %s has %d sheets", file, len(wb.Sheets))
	}
	t.sheetName = wb.Sheets[0].Name
	target := rels(t.files["xl/_rels/workbook.xml.rels"])[wb.Sheets[0].ID].Target
	t.sheetPath = path.Join("xl", strings.TrimPrefix(target, "/xl/"))
	t.sheetXML = string(t.files[t.sheetPath])

	var sheet struct {
		Rows []*xmlRow `xml:"sheetData>row"`
	}
	if err := xml.Unmarshal([]byte(t.sheetXML), &sheet); err != nil {
		return nil, err
	}
	for _, r := range sheet.Rows {
		t.rows[r.R] = r
		t.maxRow = max(t.maxRow, r.R)
	}
	for _, m := range reMerge.FindAllStringSubmatch(t.sheetXML, -1) {
		if m[2] == m[4] {
			t.merges = append(t.merges, [2]string{m[1] + m[2], m[3] + m[4]})
		}
	}

	t.shared = reSI.FindAllString(string(t.files["xl/sharedStrings.xml"]), -1)
	for _, si := range t.shared {
		t.texts = append(t.texts, siText(si))
	}

	for _, r := range rels(t.files[relsPath(t.sheetPath)]) {
		if strings.HasSuffix(r.Type, "/comments") {
			if err := t.commands(t.files[path.Join(path.Dir(t.sheetPath), r.Target)]); err != nil {
				return nil, err
			}
		}
	}
	if t.area.last == 0 {
		return nil, fmt.Errorf("xlsx: %s has no jx:area", file)
	}
	return t, nil
}

func (t *template) commands(data []byte) error {
	var comments struct {
		List []struct {
			Ref  string `xml:"ref,attr"`
			Text struct {
				Inner string `xml:",innerxml"`
			} `xml:"text"`
		} `xml:"commentList>comment"`
	}
	if err := xml.Unmarshal(data, &comments); err != nil {
		return err
	}
	for _, c := range comments.List {
		_, first := splitRef(c.Ref)
		for _, m := range reCommand.FindAllStringSubmatch(siText("<si>"+c.Text.Inner+"</si>"), -1) {
			params := map[string]string{}
			for _, p := range reParam.FindAllStringSubmatch(m[2], -1) {
				params[p[1]] = p[2]
			}
			_, last := splitRef(params["lastCell"])
			cmd := command{first: first, last: last, items: params["items"], name: params["var"], multisheet: params["multisheet"]}
			if m[1] == "area" {
				t.area = cmd
			} else {
				t.eaches = append(t.eaches, cmd)
			}
		}
	}
	return nil
}

// siText is a shared string's text: its <t> elements, rich text runs included.
func siText(si string) string {
	var v struct {
		T []string `xml:"t"`
		R []struct {
			T string `xml:"t"`
		} `xml:"r"`
	}
	_ = xml.Unmarshal([]byte(si), &v)
	var b strings.Builder
	for _, s := range v.T {
		b.WriteString(s)
	}
	for _, r := range v.R {
		b.WriteString(r.T)
	}
	return b.String()
}

type rel struct{ Type, Target string }

func rels(data []byte) map[string]rel {
	var v struct {
		Rel []struct {
			ID     string `xml:"Id,attr"`
			Type   string `xml:"Type,attr"`
			Target string `xml:"Target,attr"`
		} `xml:"Relationship"`
	}
	_ = xml.Unmarshal(data, &v)
	m := map[string]rel{}
	for _, r := range v.Rel {
		m[r.ID] = rel{r.Type, r.Target}
	}
	return m
}

func relsPath(part string) string {
	return path.Join(path.Dir(part), "_rels", path.Base(part)+".rels")
}

// splitRef splits "AB12" into its column ("AB") and row (12).
func splitRef(ref string) (string, int) {
	i := strings.IndexFunc(ref, func(r rune) bool { return r >= '0' && r <= '9' })
	if i < 0 {
		return ref, 0
	}
	n, _ := strconv.Atoi(ref[i:])
	return ref[:i], n
}

// instance is a template row rendered with a context (row nil: an absent row).
type instance struct {
	row *xmlRow
	tpl int
	ctx map[string]any
}

// expand renders template rows first..last, repeating each command's rows per item.
func (t *template) expand(first, last int, ctx map[string]any, skip *command) ([]instance, error) {
	var out []instance
	for r := first; r <= last; r++ {
		var each *command
		for i := range t.eaches {
			e := &t.eaches[i]
			if e != skip && e.first == r && e.multisheet == "" && e.last <= last {
				each = e
				break
			}
		}
		if each == nil {
			out = append(out, instance{t.rows[r], r, ctx})
			continue
		}
		items, err := t.items(each, ctx)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			inner := with(ctx, each.name, item)
			rows, err := t.expand(each.first, each.last, inner, each)
			if err != nil {
				return nil, err
			}
			out = append(out, rows...)
		}
		r = each.last
	}
	return out, nil
}

func (t *template) items(each *command, ctx map[string]any) ([]any, error) {
	e, err := parseExpr(each.items)
	if err != nil {
		return nil, err
	}
	v, err := eval(e, ctx)
	if err != nil {
		return nil, err
	}
	switch x := v.(type) {
	case nil:
		return nil, nil
	case []any:
		return x, nil
	}
	return nil, fmt.Errorf("xlsx: %s is %T, not a collection", each.items, v)
}

func with(ctx map[string]any, name string, v any) map[string]any {
	out := make(map[string]any, len(ctx)+1)
	for k, x := range ctx {
		out[k] = x
	}
	out[name] = v
	return out
}

type outSheet struct {
	name  string
	rows  []instance // placed from row start
	start int
	tail  bool // the template's rows after the area follow
}

// Render fills the template file with the context and returns the workbook.
func Render(file string, ctx map[string]any) ([]byte, error) {
	t, err := loadTemplate(file)
	if err != nil {
		return nil, err
	}
	var sheets []outSheet
	var multi *command
	for i := range t.eaches {
		if t.eaches[i].multisheet != "" {
			multi = &t.eaches[i]
		}
	}
	if multi != nil {
		items, err := t.items(multi, ctx)
		if err != nil {
			return nil, err
		}
		names, _ := ctx[multi.multisheet].([]any)
		for i, item := range items {
			rows, err := t.expand(multi.first, multi.last, with(ctx, multi.name, item), multi)
			if err != nil {
				return nil, err
			}
			name := fmt.Sprintf("Sheet%d", i+1)
			if i < len(names) {
				name = stringOf(names[i])
			}
			sheets = append(sheets, outSheet{name: name, rows: rows, start: multi.first})
		}
	} else {
		rows, err := t.expand(t.area.first, t.area.last, ctx, nil)
		if err != nil {
			return nil, err
		}
		sheets = append(sheets, outSheet{name: t.sheetName, rows: rows, start: t.area.first, tail: true})
	}
	return t.write(sheets)
}

// workbook output state: the shared strings table (the template's first).
type writer struct {
	t       *template
	shared  []string
	indexes map[string]int
}

func (w *writer) str(s string) int {
	if i, ok := w.indexes[s]; ok {
		return i
	}
	w.shared = append(w.shared, "<si><t xml:space=\"preserve\">"+escape(s)+"</t></si>")
	w.indexes[s] = len(w.shared) - 1
	return len(w.shared) - 1
}

func escape(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

type link struct{ ref, url string }

// sheet renders one output sheet's sheetData, merges and hyperlinks.
func (w *writer) sheet(s outSheet) (data string, merges []string, links []link, dim string, err error) {
	var b strings.Builder
	b.WriteString("<sheetData>")
	maxCol, minRow, maxRow := 0, 0, 0
	minCol := 1 << 30
	put := func(row *xmlRow, r int, ctx map[string]any) error {
		if row == nil {
			return nil
		}
		b.WriteString(`<row r="` + strconv.Itoa(r) + `"`)
		for _, a := range row.Attrs {
			if a.Name.Space != "" || a.Name.Local == "r" || a.Name.Local == "spans" {
				continue
			}
			b.WriteString(" " + a.Name.Local + `="` + escape(a.Value) + `"`)
		}
		b.WriteString(">")
		for _, c := range row.Cells {
			col, _ := splitRef(c.Ref)
			ref := col + strconv.Itoa(r)
			n := colIndex(col)
			minCol, maxCol = min(minCol, n), max(maxCol, n)
			if err := w.cell(&b, c, ref, ctx, &links); err != nil {
				return err
			}
		}
		b.WriteString("</row>")
		if minRow == 0 {
			minRow = r
		}
		maxRow = r
		return nil
	}
	r := s.start
	for _, in := range s.rows {
		if err := put(in.row, r, in.ctx); err != nil {
			return "", nil, nil, "", err
		}
		for _, m := range w.t.merges {
			c1, r1 := splitRef(m[0])
			c2, _ := splitRef(m[1])
			if r1 == in.tpl && in.row != nil {
				merges = append(merges, fmt.Sprintf("%s%d:%s%d", c1, r, c2, r))
			}
		}
		r++
	}
	if s.tail {
		for tr := w.t.area.last + 1; tr <= w.t.maxRow; tr++ {
			if tr >= r {
				if err := put(w.t.rows[tr], tr, nil); err != nil {
					return "", nil, nil, "", err
				}
			}
		}
	}
	b.WriteString("</sheetData>")
	if maxRow > 0 {
		dim = fmt.Sprintf("%s%d:%s%d", colName(minCol), minRow, colName(maxCol), maxRow)
	} else {
		dim = "A1"
	}
	return b.String(), merges, links, dim, nil
}

func colIndex(col string) int {
	n := 0
	for _, c := range col {
		n = n*26 + int(c-'A'+1)
	}
	return n
}

func colName(n int) string {
	s := ""
	for n > 0 {
		n--
		s = string(rune('A'+n%26)) + s
		n /= 26
	}
	return s
}

var reExpr = regexp.MustCompile(`\$\{(.*?)\}`)

// cell writes one cell: a static copy, or the value of its expressions.
func (w *writer) cell(b *strings.Builder, c xmlCell, ref string, ctx map[string]any, links *[]link) error {
	b.WriteString(`<c r="` + ref + `"`)
	if c.Style != "" {
		b.WriteString(` s="` + c.Style + `"`)
	}
	text, isString := "", false
	if c.Type == "s" {
		i, err := strconv.Atoi(strings.TrimSpace(c.V))
		if err != nil || i < 0 || i >= len(w.t.texts) {
			return fmt.Errorf("xlsx: bad shared string %q", c.V)
		}
		text, isString = w.t.texts[i], true
	}
	if !isString || ctx == nil || !strings.Contains(text, "${") {
		// A static cell keeps its value as it is.
		switch {
		case c.Type != "" && c.Type != "n":
			b.WriteString(` t="` + c.Type + `"`)
		}
		b.WriteString(">")
		if c.F != "" {
			b.WriteString("<f>" + escape(c.F) + "</f>")
		}
		if c.IS != nil {
			b.WriteString("<is>" + c.IS.Inner + "</is>")
		}
		if c.V != "" {
			b.WriteString("<v>" + escape(c.V) + "</v>")
		}
		b.WriteString("</c>")
		return nil
	}

	var value any
	if m := reExpr.FindStringSubmatchIndex(text); m[0] == 0 && m[1] == len(text) {
		value = evalText(text[m[2]:m[3]], ctx)
	} else {
		value = reExpr.ReplaceAllStringFunc(text, func(s string) string {
			v := evalText(s[2:len(s)-1], ctx)
			if v == nil {
				return ""
			}
			if h, ok := v.(Hyperlink); ok {
				return h.Text
			}
			return stringOf(v)
		})
	}
	switch v := value.(type) {
	case nil:
		b.WriteString("/>")
		return nil
	case bool:
		x := "0"
		if v {
			x = "1"
		}
		b.WriteString(` t="b"><v>` + x + `</v></c>`)
		return nil
	case int, int64, float64:
		b.WriteString(` t="n"><v>` + stringOf(v) + `</v></c>`)
		return nil
	case Hyperlink:
		*links = append(*links, link{ref, v.URL})
		value = v.Text
	case time.Time:
		value = v.String()
	}
	b.WriteString(` t="s"><v>` + strconv.Itoa(w.str(stringOf(value))) + `</v></c>`)
	return nil
}

// evalText evaluates a cell expression; a failure leaves the cell empty, as JXLS logs
// and moves on.
func evalText(s string, ctx map[string]any) any {
	e, err := parseExpr(s)
	if err != nil {
		return nil
	}
	v, err := eval(e, ctx)
	if err != nil {
		return nil
	}
	return v
}

var (
	reSheetData = regexp.MustCompile(`(?s)<sheetData>.*</sheetData>|<sheetData/>`)
	reDimension = regexp.MustCompile(`<dimension ref="[^"]*"\s*/>`)
	reMerges    = regexp.MustCompile(`(?s)<mergeCells[^>]*>.*?</mergeCells>`)
	reDrawing   = regexp.MustCompile(`<(drawing|legacyDrawing|legacyDrawingHF)\b[^>]*/>`)
	reSheets    = regexp.MustCompile(`(?s)<sheets>.*</sheets>`)
	reTabSel    = regexp.MustCompile(`\s+tabSelected="(1|true)"`)
	reRel       = regexp.MustCompile(`<Relationship\b[^>]*/>`)
	reOverride  = regexp.MustCompile(`<Override\b[^>]*/>`)
	reSST       = regexp.MustCompile(`(?s)<sst\b[^>]*>.*</sst>|<sst\b[^>]*/>`)
	reAfterLink = regexp.MustCompile(`<(printOptions|pageMargins|pageSetup|headerFooter)\b`)
	reAttr      = regexp.MustCompile(`(\w+)="([^"]*)"`)
)

const (
	relSheet     = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet"
	relHyperlink = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/hyperlink"
	typeSheet    = "application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"
)

func (t *template) write(sheets []outSheet) ([]byte, error) {
	if len(sheets) == 0 {
		return nil, errors.New("xlsx: no sheets")
	}
	w := &writer{t: t, shared: append([]string(nil), t.shared...), indexes: map[string]int{}}
	for i, s := range t.texts {
		if _, ok := w.indexes[s]; !ok && !strings.Contains(s, "${") {
			w.indexes[s] = i
		}
	}

	// Parts that go: the template sheet with its comments and drawings.
	dropped := map[string]bool{t.sheetPath: true, relsPath(t.sheetPath): true}
	var sheetRels []string
	for id, r := range rels(t.files[relsPath(t.sheetPath)]) {
		target := path.Join(path.Dir(t.sheetPath), r.Target)
		switch {
		case strings.HasSuffix(r.Type, "/comments"), strings.HasSuffix(r.Type, "/drawing"), strings.HasSuffix(r.Type, "/vmlDrawing"):
			dropped[target] = true
			if strings.HasSuffix(r.Type, "/drawing") {
				dropped[relsPath(target)] = true
			}
		default:
			sheetRels = append(sheetRels, fmt.Sprintf(`<Relationship Id="%s" Type="%s" Target="%s"/>`, id, r.Type, escape(r.Target)))
		}
	}

	out := map[string][]byte{}
	var sheetEntries, relEntries, typeEntries []string
	for i, s := range sheets {
		data, merges, links, dim, err := w.sheet(s)
		if err != nil {
			return nil, err
		}
		x := reSheetData.ReplaceAllLiteralString(t.sheetXML, data)
		x = reDimension.ReplaceAllLiteralString(x, `<dimension ref="`+dim+`"/>`)
		x = reMerges.ReplaceAllLiteralString(x, "")
		x = reDrawing.ReplaceAllLiteralString(x, "")
		if i > 0 {
			x = reTabSel.ReplaceAllLiteralString(x, "")
		}
		var extra strings.Builder
		if len(merges) > 0 {
			fmt.Fprintf(&extra, `<mergeCells count="%d">`, len(merges))
			for _, m := range merges {
				extra.WriteString(`<mergeCell ref="` + m + `"/>`)
			}
			extra.WriteString("</mergeCells>")
		}
		x = strings.Replace(x, "</sheetData>", "</sheetData>"+extra.String(), 1)
		rs := append([]string(nil), sheetRels...)
		if len(links) > 0 {
			var h strings.Builder
			h.WriteString("<hyperlinks>")
			for j, l := range links {
				id := fmt.Sprintf("rIdLink%d", j+1)
				fmt.Fprintf(&h, `<hyperlink ref="%s" r:id="%s"/>`, l.ref, id)
				rs = append(rs, fmt.Sprintf(`<Relationship Id="%s" Type="%s" Target="%s" TargetMode="External"/>`, id, relHyperlink, escape(l.url)))
			}
			h.WriteString("</hyperlinks>")
			if loc := reAfterLink.FindStringIndex(x); loc != nil {
				x = x[:loc[0]] + h.String() + x[loc[0]:]
			} else {
				x = strings.Replace(x, "</worksheet>", h.String()+"</worksheet>", 1)
			}
		}
		part := fmt.Sprintf("xl/worksheets/sheet%d.xml", i+1)
		out[part] = []byte(x)
		if len(rs) > 0 {
			out[relsPath(part)] = []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n" +
				`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
				strings.Join(rs, "") + `</Relationships>`)
		}
		id := fmt.Sprintf("rIdSheet%d", i+1)
		sheetEntries = append(sheetEntries, fmt.Sprintf(`<sheet name="%s" sheetId="%d" r:id="%s"/>`, escape(s.name), i+1, id))
		relEntries = append(relEntries, fmt.Sprintf(`<Relationship Id="%s" Type="%s" Target="worksheets/sheet%d.xml"/>`, id, relSheet, i+1))
		typeEntries = append(typeEntries, fmt.Sprintf(`<Override PartName="/%s" ContentType="%s"/>`, part, typeSheet))
	}

	wb := reSheets.ReplaceAllLiteralString(string(t.files["xl/workbook.xml"]), "<sheets>"+strings.Join(sheetEntries, "")+"</sheets>")
	out["xl/workbook.xml"] = []byte(wb)

	wbRels := reRel.ReplaceAllStringFunc(string(t.files["xl/_rels/workbook.xml.rels"]), func(s string) string {
		if attr(s, "Type") == relSheet {
			return ""
		}
		return s
	})
	out["xl/_rels/workbook.xml.rels"] = []byte(strings.Replace(wbRels, "</Relationships>", strings.Join(relEntries, "")+"</Relationships>", 1))

	types := reOverride.ReplaceAllStringFunc(string(t.files["[Content_Types].xml"]), func(s string) string {
		part := strings.TrimPrefix(attr(s, "PartName"), "/")
		if dropped[part] || strings.HasPrefix(part, "xl/worksheets/") {
			return ""
		}
		return s
	})
	out["[Content_Types].xml"] = []byte(strings.Replace(types, "</Types>", strings.Join(typeEntries, "")+"</Types>", 1))

	sst := string(t.files["xl/sharedStrings.xml"])
	open := reSST.FindString(sst)
	if i := strings.Index(open, ">"); i >= 0 {
		open = open[:i+1]
	}
	open = strings.TrimSuffix(open, "/>")
	open = regexp.MustCompile(`\s+(count|uniqueCount)="[^"]*"`).ReplaceAllString(strings.TrimSuffix(open, ">"), "")
	n := strconv.Itoa(len(w.shared))
	sst = reSST.ReplaceAllLiteralString(sst, open+` count="`+n+`" uniqueCount="`+n+`">`+strings.Join(w.shared, "")+"</sst>")
	out["xl/sharedStrings.xml"] = []byte(sst)

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(name string, data []byte) error {
		f, err := zw.Create(name)
		if err != nil {
			return err
		}
		_, err = f.Write(data)
		return err
	}
	for _, name := range t.order {
		if dropped[name] || strings.HasPrefix(name, "xl/worksheets/sheet") || strings.HasPrefix(name, "xl/worksheets/_rels/") {
			continue
		}
		data := t.files[name]
		if d, ok := out[name]; ok {
			data = d
			delete(out, name)
		}
		if err := add(name, data); err != nil {
			return nil, err
		}
	}
	for i := range sheets {
		for _, name := range []string{fmt.Sprintf("xl/worksheets/sheet%d.xml", i+1), relsPath(fmt.Sprintf("xl/worksheets/sheet%d.xml", i+1))} {
			if data, ok := out[name]; ok {
				if err := add(name, data); err != nil {
					return nil, err
				}
				delete(out, name)
			}
		}
	}
	for name, data := range out { // parts the template lacked (sharedStrings)
		if err := add(name, data); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func attr(element, name string) string {
	for _, m := range reAttr.FindAllStringSubmatch(element, -1) {
		if m[1] == name {
			return m[2]
		}
	}
	return ""
}
