package main

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// ---------- zip helpers ----------

type zentry struct {
	hdr  zip.FileHeader
	data []byte
}

func readZip(b []byte) ([]*zentry, error) {
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return nil, fmt.Errorf("файл гэмтэлтэй эсвэл Excel/Word биш байна")
	}
	var out []*zentry
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		d, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, err
		}
		out = append(out, &zentry{hdr: f.FileHeader, data: d})
	}
	return out, nil
}

func writeZip(es []*zentry) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range es {
		h := e.hdr
		h.Method = zip.Deflate
		w, err := zw.CreateHeader(&h)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(e.data); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ---------- text runs inside XML ----------

type runSpec struct {
	re    *regexp.Regexp // group 1 = text
	open  string
	close string
}

var (
	xlsxRun = runSpec{regexp.MustCompile(`<t(?:\s[^>]*)?>([\s\S]*?)</t>`), `<t xml:space="preserve">`, `</t>`}
	docxRun = runSpec{regexp.MustCompile(`<w:t(?:\s[^>]*)?>([\s\S]*?)</w:t>`), `<w:t xml:space="preserve">`, `</w:t>`}
)

// transformRuns joins all text runs of one container (a paragraph or a
// shared string), transforms the joined text and writes it back into the
// first run. Formatting of the first run is kept.
func transformRuns(frag string, rs runSpec, fn func(string) string) string {
	locs := rs.re.FindAllStringSubmatchIndex(frag, -1)
	if len(locs) == 0 {
		return frag
	}
	var sb strings.Builder
	for _, l := range locs {
		sb.WriteString(xmlUnescape(frag[l[2]:l[3]]))
	}
	joined := sb.String()
	nw := fn(joined)
	if nw == joined {
		return frag
	}
	var out strings.Builder
	prev := 0
	for i, l := range locs {
		out.WriteString(frag[prev:l[0]])
		if i == 0 {
			out.WriteString(rs.open + xmlEscape(nw) + rs.close)
		} else {
			out.WriteString(rs.open + rs.close)
		}
		prev = l[1]
	}
	out.WriteString(frag[prev:])
	return out.String()
}

func runsText(frag string, rs runSpec) string {
	var sb strings.Builder
	for _, m := range rs.re.FindAllStringSubmatch(frag, -1) {
		sb.WriteString(xmlUnescape(m[1]))
	}
	return sb.String()
}

// ---------- XLSX ----------

var (
	reSI      = regexp.MustCompile(`<si>[\s\S]*?</si>|<si/>`)
	reCell    = regexp.MustCompile(`<c\b([^>]*?)(?:/>|>([\s\S]*?)</c>)`)
	reAttrR   = regexp.MustCompile(`\br="([A-Z]+)(\d+)"`)
	reAttrT   = regexp.MustCompile(`\st="([^"]*)"`)
	reV       = regexp.MustCompile(`<v>([\s\S]*?)</v>`)
	reF       = regexp.MustCompile(`<f[\s>/]`)
	reIsBlock = regexp.MustCompile(`<is>[\s\S]*?</is>`)
)

func columnKind(header string) string {
	h := strings.ToLower(strings.TrimSpace(header))
	if h == "" || utf8.RuneCountInString(h) > 40 {
		return ""
	}
	if strings.Contains(h, "регистр") || h == "рд" || strings.HasPrefix(h, "рд ") {
		return KReg
	}
	if strings.Contains(h, "мэйл") || strings.Contains(h, "email") || strings.Contains(h, "e-mail") || strings.Contains(h, "цахим шуудан") {
		return KEmail
	}
	if strings.Contains(h, "утас") {
		return KPhone
	}
	if strings.Contains(h, "нэр") || strings.Contains(h, "овог") {
		for _, bad := range []string{"тушаал", "байгууллага", "хэлтэс", "бараа", "бүтээгдэхүүн", "сургууль", "анги", "файл", "хичээл", "төсөл"} {
			if strings.Contains(h, bad) {
				return ""
			}
		}
		return KName
	}
	return ""
}

type cellInfo struct {
	start, end int
	col        string
	row        int
	attrs      string
	body       string
	t          string
}

func parseCells(sheet string) []cellInfo {
	var cs []cellInfo
	for _, l := range reCell.FindAllStringSubmatchIndex(sheet, -1) {
		c := cellInfo{start: l[0], end: l[1], attrs: sheet[l[2]:l[3]]}
		if l[4] >= 0 {
			c.body = sheet[l[4]:l[5]]
		}
		if m := reAttrR.FindStringSubmatch(c.attrs); m != nil {
			c.col = m[1]
			c.row, _ = strconv.Atoi(m[2])
		}
		if m := reAttrT.FindStringSubmatch(c.attrs); m != nil {
			c.t = m[1]
		}
		cs = append(cs, c)
	}
	return cs
}

func inlineCell(attrs, text string) string {
	a := reAttrT.ReplaceAllString(attrs, "")
	return `<c` + a + ` t="inlineStr"><is><t xml:space="preserve">` + xmlEscape(text) + `</t></is></c>`
}

func numCell(attrs, num string) string {
	a := reAttrT.ReplaceAllString(attrs, "")
	return `<c` + a + `><v>` + num + `</v></c>`
}

type xlsxDoc struct {
	es      []*zentry
	ssEntry *zentry
	si      []string
	sheets  []*zentry
}

func loadXlsx(b []byte) (*xlsxDoc, error) {
	es, err := readZip(b)
	if err != nil {
		return nil, err
	}
	d := &xlsxDoc{es: es}
	for _, e := range es {
		switch {
		case e.hdr.Name == "xl/sharedStrings.xml":
			d.ssEntry = e
			d.si = reSI.FindAllString(string(e.data), -1)
		case strings.HasPrefix(e.hdr.Name, "xl/worksheets/sheet") && strings.HasSuffix(e.hdr.Name, ".xml"):
			d.sheets = append(d.sheets, e)
		}
	}
	if len(d.sheets) == 0 {
		return nil, fmt.Errorf("Excel хуудас олдсонгүй")
	}
	return d, nil
}

func (d *xlsxDoc) cellText(c cellInfo) string {
	switch c.t {
	case "s":
		if m := reV.FindStringSubmatch(c.body); m != nil {
			if i, err := strconv.Atoi(strings.TrimSpace(m[1])); err == nil && i < len(d.si) {
				return runsText(d.si[i], xlsxRun)
			}
		}
	case "inlineStr":
		return runsText(c.body, xlsxRun)
	case "str", "b", "e":
		return ""
	default:
		if m := reV.FindStringSubmatch(c.body); m != nil {
			return m[1]
		}
	}
	return ""
}

func (d *xlsxDoc) colKinds(cs []cellInfo) map[string]string {
	kinds := map[string]string{}
	for _, c := range cs {
		if c.row == 1 {
			if k := columnKind(d.cellText(c)); k != "" {
				kinds[c.col] = k
			}
		}
	}
	return kinds
}

// CollectNames adds every value of name columns to the key dictionary.
func (d *xlsxDoc) CollectNames(k *Key) int {
	n := 0
	for _, sh := range d.sheets {
		cs := parseCells(string(sh.data))
		kinds := d.colKinds(cs)
		for _, c := range cs {
			if c.row > 1 && kinds[c.col] == KName {
				v := strings.TrimSpace(d.cellText(c))
				if v == "" {
					continue
				}
				before := len(k.Names)
				k.AddName(v)
				for _, w := range strings.Fields(v) {
					k.AddName(strings.Trim(w, ".,"))
				}
				n += len(k.Names) - before
			}
		}
	}
	return n
}

func (d *xlsxDoc) save() ([]byte, error) {
	if d.ssEntry != nil {
		s := string(d.ssEntry.data)
		i := 0
		s = reSI.ReplaceAllStringFunc(s, func(string) string {
			r := d.si[i]
			i++
			return r
		})
		d.ssEntry.data = []byte(s)
	}
	return writeZip(d.es)
}

func AnonymizeXlsx(b []byte, k *Key, st Stats) ([]byte, error) {
	d, err := loadXlsx(b)
	if err != nil {
		return nil, err
	}
	d.CollectNames(k)
	siKind := map[int]string{}
	type pend struct {
		sh   *zentry
		cs   []cellInfo
		kind map[string]string
	}
	var ps []pend
	for _, sh := range d.sheets {
		cs := parseCells(string(sh.data))
		kinds := d.colKinds(cs)
		for _, c := range cs {
			if c.row > 1 && kinds[c.col] != "" && c.t == "s" {
				if m := reV.FindStringSubmatch(c.body); m != nil {
					if i, err := strconv.Atoi(strings.TrimSpace(m[1])); err == nil {
						siKind[i] = kinds[c.col]
					}
				}
			}
		}
		ps = append(ps, pend{sh, cs, kinds})
	}
	for i := range d.si {
		kind := siKind[i]
		d.si[i] = transformRuns(d.si[i], xlsxRun, func(t string) string { return k.Anonymize(t, kind, st) })
	}
	for _, p := range ps {
		s := string(p.sh.data)
		var out strings.Builder
		prev := 0
		for _, c := range p.cs {
			kind := ""
			if c.row > 1 {
				kind = p.kind[c.col]
			}
			repl := ""
			switch {
			case c.t == "inlineStr":
				nb := reIsBlock.ReplaceAllStringFunc(c.body, func(is string) string {
					return transformRuns(is, xlsxRun, func(t string) string { return k.Anonymize(t, kind, st) })
				})
				if nb != c.body {
					repl = `<c` + c.attrs + `>` + nb + `</c>`
				}
			case c.t == "" || c.t == "n":
				if (kind == KPhone || kind == KReg) && !reF.MatchString(c.body) {
					if m := reV.FindStringSubmatch(c.body); m != nil && strings.TrimSpace(m[1]) != "" {
						st[kind]++
						repl = inlineCell(c.attrs, k.codeFor(kind, strings.TrimSpace(m[1]), true))
					}
				}
			}
			if repl != "" {
				out.WriteString(s[prev:c.start])
				out.WriteString(repl)
				prev = c.end
			}
		}
		out.WriteString(s[prev:])
		p.sh.data = []byte(out.String())
	}
	return d.save()
}

func RestoreXlsx(b []byte, k *Key, st Stats) ([]byte, error) {
	d, err := loadXlsx(b)
	if err != nil {
		return nil, err
	}
	for i := range d.si {
		d.si[i] = transformRuns(d.si[i], xlsxRun, func(t string) string { return k.Restore(t, st) })
	}
	for _, sh := range d.sheets {
		s := string(sh.data)
		cs := parseCells(s)
		var out strings.Builder
		prev := 0
		for _, c := range cs {
			if c.t != "inlineStr" {
				continue
			}
			repl := ""
			txt := strings.TrimSpace(runsText(c.body, xlsxRun))
			if m := reCode.FindStringSubmatch(txt); m != nil && m[0] == txt {
				if it, ok := k.byCode[m[1]+"_"+m[2]]; ok && it.Num {
					st[it.Kind]++
					repl = numCell(c.attrs, it.Orig)
				}
			}
			if repl == "" {
				nb := reIsBlock.ReplaceAllStringFunc(c.body, func(is string) string {
					return transformRuns(is, xlsxRun, func(t string) string { return k.Restore(t, st) })
				})
				if nb != c.body {
					repl = `<c` + c.attrs + `>` + nb + `</c>`
				}
			}
			if repl != "" {
				out.WriteString(s[prev:c.start])
				out.WriteString(repl)
				prev = c.end
			}
		}
		out.WriteString(s[prev:])
		sh.data = []byte(out.String())
	}
	return d.save()
}

func CollectNamesFromXlsx(b []byte, k *Key) (int, error) {
	d, err := loadXlsx(b)
	if err != nil {
		return 0, err
	}
	return d.CollectNames(k), nil
}

// ---------- DOCX ----------

var reWP = regexp.MustCompile(`<w:p(?:\s[^>]*)?>[\s\S]*?</w:p>`)

func docxParts(name string) bool {
	if !strings.HasPrefix(name, "word/") || !strings.HasSuffix(name, ".xml") {
		return false
	}
	base := strings.TrimPrefix(name, "word/")
	for _, p := range []string{"document", "header", "footer", "footnotes", "endnotes", "comments"} {
		if strings.HasPrefix(base, p) {
			return true
		}
	}
	return false
}

func processDocx(b []byte, fn func(string) string) ([]byte, error) {
	es, err := readZip(b)
	if err != nil {
		return nil, err
	}
	found := false
	for _, e := range es {
		if docxParts(e.hdr.Name) {
			found = true
			e.data = []byte(reWP.ReplaceAllStringFunc(string(e.data), func(p string) string {
				return transformRuns(p, docxRun, fn)
			}))
		}
	}
	if !found {
		return nil, fmt.Errorf("Word баримтын агуулга олдсонгүй")
	}
	return writeZip(es)
}

func AnonymizeDocx(b []byte, k *Key, st Stats) ([]byte, error) {
	return processDocx(b, func(t string) string { return k.Anonymize(t, "", st) })
}

func RestoreDocx(b []byte, k *Key, st Stats) ([]byte, error) {
	return processDocx(b, func(t string) string { return k.Restore(t, st) })
}
