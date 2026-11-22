package policybench

// A small PDF writer for the compliance report export: A4 pages of
// wrapped text in the standard Helvetica fonts (no embedding), headings in
// bold, page numbers in the footer. Text outside WinAnsi becomes "?".

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"strings"
)

// PDFBlock is one block of the document.
type PDFBlock struct {
	Style string // h1, h2, h3, body, small, mono
	Text  string
}

const (
	pdfPageW   = 595.0
	pdfPageH   = 842.0
	pdfMargin  = 56.0
	pdfFooterY = 30.0
)

type pdfStyle struct {
	font    string // F1 regular, F2 bold, F3 mono
	size    float64
	before  float64
	charW   float64 // average glyph width as a share of the size
	leading float64
}

var pdfStyles = map[string]pdfStyle{
	"h1":    {"F2", 18, 10, 0.56, 1.3},
	"h2":    {"F2", 13.5, 12, 0.56, 1.3},
	"h3":    {"F2", 11, 8, 0.55, 1.3},
	"body":  {"F1", 10, 4, 0.5, 1.4},
	"small": {"F1", 8.5, 2, 0.5, 1.35},
	"mono":  {"F3", 8.5, 2, 0.6, 1.3},
}

// WritePDF renders blocks into a PDF file titled title.
func WritePDF(title string, blocks []PDFBlock) []byte {
	var pages [][]string // content stream lines per page
	var cur []string
	y := pdfPageH - pdfMargin
	newPage := func() {
		if cur != nil {
			pages = append(pages, cur)
		}
		cur = []string{}
		y = pdfPageH - pdfMargin
	}
	newPage()
	for _, b := range blocks {
		st, ok := pdfStyles[b.Style]
		if !ok {
			st = pdfStyles["body"]
		}
		maxChars := int((pdfPageW - 2*pdfMargin) / (st.size * st.charW))
		var lines []string
		for _, para := range strings.Split(b.Text, "\n") {
			lines = append(lines, wrap(para, maxChars)...)
		}
		y -= st.before
		if b.Style == "h1" || b.Style == "h2" {
			if y-3*st.size*st.leading < pdfMargin { // keep a heading with its first lines
				newPage()
			}
		}
		for _, l := range lines {
			if y-st.size < pdfMargin {
				newPage()
			}
			y -= st.size * st.leading
			cur = append(cur, fmt.Sprintf("BT /%s %.1f Tf %.1f %.1f Td (%s) Tj ET", st.font, st.size, pdfMargin, y, pdfEscape(l)))
		}
	}
	pages = append(pages, cur)
	var buf bytes.Buffer
	var offsets []int
	obj := func(body string) {
		offsets = append(offsets, buf.Len())
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", len(offsets), body)
	}
	buf.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	n := len(pages)
	// 1 catalog, 2 pages, 3-5 fonts, 6 info, then per page: page, content.
	obj("<< /Type /Catalog /Pages 2 0 R >>")
	var kids []string
	for i := 0; i < n; i++ {
		kids = append(kids, fmt.Sprintf("%d 0 R", 7+2*i))
	}
	obj(fmt.Sprintf("<< /Type /Pages /Count %d /Kids [%s] >>", n, strings.Join(kids, " ")))
	obj("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>")
	obj("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica-Bold /Encoding /WinAnsiEncoding >>")
	obj("<< /Type /Font /Subtype /Type1 /BaseFont /Courier /Encoding /WinAnsiEncoding >>")
	obj(fmt.Sprintf("<< /Title (%s) /Producer (Heimdall) >>", pdfEscape(title)))
	for i, p := range pages {
		footer := fmt.Sprintf("BT /F1 8 Tf %.1f %.1f Td (%s) Tj ET", pdfMargin, pdfFooterY, pdfEscape(fmt.Sprintf("%s - page %d of %d", title, i+1, n)))
		content := strings.Join(append(append([]string(nil), p...), footer), "\n")
		var z bytes.Buffer
		zw := zlib.NewWriter(&z)
		_, _ = zw.Write([]byte(content))
		_ = zw.Close()
		obj(fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %.0f %.0f] /Resources << /Font << /F1 3 0 R /F2 4 0 R /F3 5 0 R >> >> /Contents %d 0 R >>", pdfPageW, pdfPageH, 8+2*i))
		offsets = append(offsets, buf.Len())
		fmt.Fprintf(&buf, "%d 0 obj\n<< /Length %d /Filter /FlateDecode >>\nstream\n", len(offsets), z.Len())
		buf.Write(z.Bytes())
		buf.WriteString("\nendstream\nendobj\n")
	}
	xref := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n0000000000 65535 f \n", len(offsets)+1)
	for _, o := range offsets {
		fmt.Fprintf(&buf, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R /Info 6 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets)+1, xref)
	return buf.Bytes()
}

// wrap breaks s into lines of at most n characters at spaces.
func wrap(s string, n int) []string {
	if n < 10 {
		n = 10
	}
	words := strings.Fields(s)
	if len(words) == 0 {
		return []string{""}
	}
	indent := ""
	if t := strings.TrimLeft(s, " "); len(t) < len(s) {
		indent = s[:len(s)-len(t)]
	}
	var out []string
	line := indent
	for _, w := range words {
		for len([]rune(w)) > n {
			r := []rune(w)
			if strings.TrimSpace(line) != "" {
				out = append(out, line)
			}
			out = append(out, string(r[:n]))
			w, line = string(r[n:]), indent
		}
		switch {
		case strings.TrimSpace(line) == "":
			line = indent + w
		case len([]rune(line))+1+len([]rune(w)) <= n:
			line += " " + w
		default:
			out = append(out, line)
			line = indent + "  " + w
		}
	}
	return append(out, line)
}

var winAnsiBack = func() map[rune]byte {
	m := map[rune]byte{}
	for b, r := range winAnsi {
		m[r] = b
	}
	m['‘'], m['’'], m['“'], m['”'], m['–'], m['—'] = 0x91, 0x92, 0x93, 0x94, '-', '-'
	return m
}()

// pdfEscape encodes s as the bytes of a WinAnsi literal string.
func pdfEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		var c byte
		switch {
		case r == '\t':
			c = ' '
		case r >= 0x20 && r < 0x7f:
			c = byte(r)
		case winAnsiBack[r] != 0:
			c = winAnsiBack[r]
		case r >= 0xa0 && r <= 0xff:
			c = byte(r)
		default:
			c = '?'
		}
		switch c {
		case '(', ')', '\\':
			b.WriteByte('\\')
			b.WriteByte(c)
		default:
			if c >= 0x80 {
				fmt.Fprintf(&b, "\\%03o", c)
			} else {
				b.WriteByte(c)
			}
		}
	}
	return b.String()
}
