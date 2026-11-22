package policybench

// A small PDF text reader for servers without pdftotext. It reads the
// objects of the file (and of its object streams), decodes Flate streams,
// maps each font's ToUnicode table, and replays the text operators of the
// page content streams in file order. It does not lay text out in columns
// and it refuses a file whose text it cannot read, so a scan or an exotic
// font encoding never becomes garbage clauses.

import (
	"bytes"
	"compress/zlib"
	"errors"
	"io"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
)

var (
	pdfObjRe     = regexp.MustCompile(`(\d+)\s+(\d+)\s+obj\b`)
	pdfRefRe     = regexp.MustCompile(`/([A-Za-z0-9_.+-]+)\s+(\d+)\s+\d+\s+R`)
	pdfToUniRe   = regexp.MustCompile(`/ToUnicode\s+(\d+)\s+\d+\s+R`)
	pdfFontDict  = regexp.MustCompile(`/Font\s*<<([^<>]*)>>`)
	pdfFontRefRe = regexp.MustCompile(`/Font\s+(\d+)\s+\d+\s+R`)
	pdfIntRe     = regexp.MustCompile(`/(N|First)\s+(\d+)`)
)

type pdfObj struct {
	dict   string // the object's text before "stream" (or all of it)
	stream []byte // decoded stream; nil when none or not decodable
}

// PDFText returns the text of a PDF with the built-in reader.
func PDFText(data []byte) (string, error) {
	if !bytes.HasPrefix(bytes.TrimSpace(data[:min(len(data), 1024)]), []byte("%PDF-")) && !bytes.Contains(data[:min(len(data), 1024)], []byte("%PDF-")) {
		return "", errors.New("not a PDF file")
	}
	if bytes.Contains(data, []byte("/Encrypt")) {
		return "", errors.New("the PDF is encrypted; save a copy without a password and upload again")
	}
	objs := pdfObjects(data)
	// Fonts: resource name -> font object -> ToUnicode map.
	cmaps := map[string]map[string]string{}
	fontObj := map[string]string{}
	addFonts := func(body string) {
		for _, m := range pdfRefRe.FindAllStringSubmatch(body, -1) {
			fontObj[m[1]] = m[2]
		}
	}
	for _, o := range objs {
		for _, m := range pdfFontDict.FindAllStringSubmatch(o.dict, -1) {
			addFonts(m[1])
		}
		for _, m := range pdfFontRefRe.FindAllStringSubmatch(o.dict, -1) {
			if fo, ok := objs[m[1]]; ok {
				addFonts(fo.dict)
			}
		}
	}
	for name, num := range fontObj {
		fo, ok := objs[num]
		if !ok {
			continue
		}
		if m := pdfToUniRe.FindStringSubmatch(fo.dict); m != nil {
			if cm, ok := objs[m[1]]; ok && cm.stream != nil {
				cmaps[name] = parseCMap(cm.stream)
			}
		}
	}
	nums := make([]int, 0, len(objs))
	for k := range objs {
		n, _ := strconv.Atoi(k)
		nums = append(nums, n)
	}
	sortInts(nums)
	var out strings.Builder
	for _, n := range nums {
		o := objs[strconv.Itoa(n)]
		if o.stream == nil || !isContent(o) {
			continue
		}
		replayText(o.stream, cmaps, &out)
		out.WriteString("\n")
	}
	text := out.String()
	if strings.TrimSpace(text) == "" {
		return "", errors.New("no text found in the PDF (a scan has none: export it with its text, or paste the text)")
	}
	if !readable(text) {
		return "", errors.New("the PDF's text uses a font encoding this server cannot read; install pdftotext on the server, or upload the document as DOCX, Markdown or text")
	}
	return text, nil
}

func sortInts(a []int) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j] < a[j-1]; j-- {
			a[j], a[j-1] = a[j-1], a[j]
		}
	}
}

func isContent(o pdfObj) bool {
	d := o.dict
	for _, skip := range []string{"/Image", "/FontFile", "/Length1", "/XRef", "/ObjStm", "/CMap", "/Metadata", "/EmbeddedFile"} {
		if strings.Contains(d, skip) {
			return false
		}
	}
	return bytes.Contains(o.stream, []byte("BT")) && (bytes.Contains(o.stream, []byte("Tj")) || bytes.Contains(o.stream, []byte("TJ")) || bytes.Contains(o.stream, []byte("'")))
}

// readable is true when most of the text is letters, digits, spaces and
// common punctuation.
func readable(s string) bool {
	good, all := 0, 0
	for _, r := range s {
		if unicode.IsSpace(r) {
			continue
		}
		all++
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) {
			if r != unicode.ReplacementChar {
				good++
			}
		}
	}
	return all > 0 && float64(good)/float64(all) >= 0.9
}

// pdfObjects reads every "N G obj ... endobj" of the file and the objects
// of its object streams, keyed by object number.
func pdfObjects(data []byte) map[string]pdfObj {
	objs := map[string]pdfObj{}
	locs := pdfObjRe.FindAllSubmatchIndex(data, -1)
	for i, loc := range locs {
		num := string(data[loc[2]:loc[3]])
		start := loc[1]
		end := len(data)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		if e := bytes.Index(data[start:end], []byte("endobj")); e >= 0 {
			end = start + e
		}
		body := data[start:end]
		o := pdfObj{dict: string(body)}
		if s := bytes.Index(body, []byte("stream")); s >= 0 && !bytes.HasPrefix(body[s:], []byte("streamend")) {
			o.dict = string(body[:s])
			raw := body[s+len("stream"):]
			raw = bytes.TrimPrefix(raw, []byte("\r"))
			raw = bytes.TrimPrefix(raw, []byte("\n"))
			if e := bytes.LastIndex(raw, []byte("endstream")); e >= 0 {
				raw = raw[:e]
			}
			o.stream = decodeStream(o.dict, raw)
		}
		objs[num] = o
	}
	for _, o := range objs {
		if !strings.Contains(o.dict, "/ObjStm") || o.stream == nil {
			continue
		}
		var n, first int
		for _, m := range pdfIntRe.FindAllStringSubmatch(o.dict, -1) {
			v, _ := strconv.Atoi(m[2])
			if m[1] == "N" {
				n = v
			} else {
				first = v
			}
		}
		if first <= 0 || first > len(o.stream) || n <= 0 || n > 100000 {
			continue
		}
		head := strings.Fields(string(o.stream[:first]))
		for k := 0; k+1 < len(head) && k/2 < n; k += 2 {
			off, err := strconv.Atoi(head[k+1])
			if err != nil || first+off > len(o.stream) {
				continue
			}
			end := len(o.stream)
			if k+3 < len(head) {
				if next, err := strconv.Atoi(head[k+3]); err == nil && first+next <= len(o.stream) && next >= off {
					end = first + next
				}
			}
			if _, have := objs[head[k]]; !have {
				objs[head[k]] = pdfObj{dict: string(o.stream[first+off : end])}
			}
		}
	}
	return objs
}

func decodeStream(dict string, raw []byte) []byte {
	switch {
	case strings.Contains(dict, "/FlateDecode") || strings.Contains(dict, "/Fl "):
		if strings.Contains(dict, "/DCTDecode") || strings.Contains(dict, "/ASCII85Decode") || strings.Contains(dict, "/LZWDecode") {
			return nil
		}
		zr, err := zlib.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil
		}
		defer zr.Close()
		out, err := io.ReadAll(io.LimitReader(zr, 64<<20))
		if err != nil && len(out) == 0 {
			return nil
		}
		return out
	case strings.Contains(dict, "/Filter"):
		return nil
	}
	return raw
}

// parseCMap reads the bfchar and bfrange entries of a ToUnicode CMap.
func parseCMap(b []byte) map[string]string {
	m := map[string]string{}
	toks := pdfTokens(b)
	for i := 0; i < len(toks); i++ {
		switch toks[i].op {
		case "beginbfchar":
			for i++; i+1 < len(toks) && toks[i].op != "endbfchar"; i += 2 {
				m[string(toks[i].str)] = utf16Text(toks[i+1].str)
			}
		case "beginbfrange":
			for i++; i+2 < len(toks) && toks[i].op != "endbfrange"; i += 3 {
				lo, hi := toks[i].str, toks[i+1].str
				if len(lo) == 0 || len(lo) != len(hi) || len(lo) > 4 {
					continue
				}
				a, b := codeInt(lo), codeInt(hi)
				if b < a || b-a > 65535 {
					continue
				}
				if toks[i+2].arr != nil {
					for k, dst := range toks[i+2].arr {
						if a+k > b {
							break
						}
						m[string(intCode(a+k, len(lo)))] = utf16Text(dst.str)
					}
					continue
				}
				dst := toks[i+2].str
				if len(dst) == 0 {
					continue
				}
				for c := a; c <= b; c++ {
					d := append([]byte(nil), dst...)
					d[len(d)-1] += byte(c - a)
					m[string(intCode(c, len(lo)))] = utf16Text(d)
				}
			}
		}
	}
	return m
}

func codeInt(b []byte) int {
	n := 0
	for _, c := range b {
		n = n<<8 | int(c)
	}
	return n
}

func intCode(n, width int) []byte {
	out := make([]byte, width)
	for i := width - 1; i >= 0; i-- {
		out[i] = byte(n)
		n >>= 8
	}
	return out
}

func utf16Text(b []byte) string {
	if len(b)%2 != 0 {
		return string(b)
	}
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = uint16(b[2*i])<<8 | uint16(b[2*i+1])
	}
	return string(utf16.Decode(u))
}

// pdfTok is one token of a content stream: an operator, a number, a name,
// a string (literal or hex) or an array.
type pdfTok struct {
	op    string
	num   float64
	isNum bool
	str   []byte
	isStr bool
	arr   []pdfTok
	isArr bool
}

func pdfTokens(b []byte) []pdfTok {
	var stack [][]pdfTok
	var cur []pdfTok
	push := func(t pdfTok) { cur = append(cur, t) }
	for i := 0; i < len(b); {
		c := b[i]
		switch {
		case c == ' ' || c == '\n' || c == '\r' || c == '\t' || c == '\f' || c == 0:
			i++
		case c == '%':
			for i < len(b) && b[i] != '\n' && b[i] != '\r' {
				i++
			}
		case c == '(':
			s, n := literalString(b[i:])
			push(pdfTok{str: s, isStr: true})
			i += n
		case c == '<' && i+1 < len(b) && b[i+1] == '<':
			push(pdfTok{op: "<<"})
			i += 2
		case c == '>' && i+1 < len(b) && b[i+1] == '>':
			push(pdfTok{op: ">>"})
			i += 2
		case c == '<':
			j := bytes.IndexByte(b[i:], '>')
			if j < 0 {
				return append(stack0(stack), cur...)
			}
			push(pdfTok{str: hexBytes(b[i+1 : i+j]), isStr: true})
			i += j + 1
		case c == '[':
			stack = append(stack, cur)
			cur = nil
			i++
		case c == ']':
			arr := cur
			if len(stack) > 0 {
				cur = stack[len(stack)-1]
				stack = stack[:len(stack)-1]
			} else {
				cur = nil
			}
			push(pdfTok{arr: arr, isArr: true})
			i++
		default:
			j := i + 1
			for j < len(b) && !strings.ContainsRune(" \n\r\t\f\x00()<>[]/%", rune(b[j])) {
				j++
			}
			w := string(b[i:j])
			if f, err := strconv.ParseFloat(w, 64); err == nil {
				push(pdfTok{num: f, isNum: true})
			} else {
				push(pdfTok{op: w})
			}
			if j == i {
				j++
			}
			i = j
		}
	}
	return append(stack0(stack), cur...)
}

func stack0(s [][]pdfTok) []pdfTok {
	if len(s) == 0 {
		return nil
	}
	var out []pdfTok
	for _, l := range s {
		out = append(out, l...)
	}
	return out
}

func literalString(b []byte) ([]byte, int) {
	var out []byte
	depth := 0
	i := 0
	for i < len(b) {
		c := b[i]
		switch {
		case c == '(':
			if depth > 0 {
				out = append(out, c)
			}
			depth++
			i++
		case c == ')':
			depth--
			i++
			if depth == 0 {
				return out, i
			}
			out = append(out, c)
		case c == '\\' && i+1 < len(b):
			n := b[i+1]
			i += 2
			switch n {
			case 'n':
				out = append(out, '\n')
			case 'r':
				out = append(out, '\r')
			case 't':
				out = append(out, '\t')
			case 'b':
				out = append(out, '\b')
			case 'f':
				out = append(out, '\f')
			case '\r', '\n':
				if n == '\r' && i < len(b) && b[i] == '\n' {
					i++
				}
			default:
				if n >= '0' && n <= '7' {
					v := int(n - '0')
					for k := 0; k < 2 && i < len(b) && b[i] >= '0' && b[i] <= '7'; k++ {
						v = v*8 + int(b[i]-'0')
						i++
					}
					out = append(out, byte(v))
				} else {
					out = append(out, n)
				}
			}
		default:
			out = append(out, c)
			i++
		}
	}
	return out, i
}

func hexBytes(b []byte) []byte {
	var digits []byte
	for _, c := range b {
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F') {
			digits = append(digits, c)
		}
	}
	if len(digits)%2 == 1 {
		digits = append(digits, '0')
	}
	out := make([]byte, len(digits)/2)
	for i := range out {
		v, _ := strconv.ParseUint(string(digits[2*i:2*i+2]), 16, 8)
		out[i] = byte(v)
	}
	return out
}

// winAnsi maps the WinAnsiEncoding bytes that differ from Latin-1.
var winAnsi = map[byte]rune{0x80: '€', 0x82: '‚', 0x84: '„', 0x85: '…', 0x91: '\'', 0x92: '\'', 0x93: '"', 0x94: '"',
	0x95: '•', 0x96: '-', 0x97: '-', 0x99: '™', 0x8c: 'Œ', 0x9c: 'œ'}

func decodeShown(s []byte, cmap map[string]string) string {
	if len(cmap) > 0 {
		width := 1
		for k := range cmap {
			width = len(k)
			break
		}
		var out strings.Builder
		for i := 0; i+width <= len(s); i += width {
			if v, ok := cmap[string(s[i:i+width])]; ok {
				out.WriteString(v)
			} else if width == 1 {
				out.WriteRune(rune(s[i]))
			} else {
				out.WriteRune(unicode.ReplacementChar)
			}
		}
		return out.String()
	}
	if len(s) >= 2 && s[0] == 0xfe && s[1] == 0xff {
		return utf16Text(s[2:])
	}
	var out strings.Builder
	for _, c := range s {
		if r, ok := winAnsi[c]; ok {
			out.WriteRune(r)
		} else if c >= 0x20 || c == '\t' {
			out.WriteRune(rune(c))
		}
	}
	return out.String()
}

// replayText plays the text operators of one content stream into out.
func replayText(b []byte, cmaps map[string]map[string]string, out *strings.Builder) {
	toks := pdfTokens(b)
	var ops []pdfTok
	var cmap map[string]string
	y, lastY, haveY, lastShown := 0.0, 0.0, false, false
	lineStart := true
	show := func(s string) {
		if s == "" {
			return
		}
		if lastShown && haveY && y != lastY {
			out.WriteString("\n")
			lineStart = true
		}
		out.WriteString(s)
		lastY, lastShown, lineStart = y, true, false
	}
	newline := func() {
		if lastShown && !lineStart {
			out.WriteString("\n")
			lineStart = true
		}
	}
	for _, t := range toks {
		if t.op == "" || t.op == "<<" || t.op == ">>" || strings.HasPrefix(t.op, "/") || t.op == "true" || t.op == "false" || t.op == "null" {
			ops = append(ops, t)
			continue
		}
		switch t.op {
		case "Tf":
			if len(ops) >= 2 && !ops[len(ops)-2].isStr && strings.HasPrefix(ops[len(ops)-2].op, "/") {
				cmap = cmaps[strings.TrimPrefix(ops[len(ops)-2].op, "/")]
			} else {
				cmap = nil
			}
		case "Tm":
			if len(ops) >= 6 && ops[len(ops)-1].isNum {
				y, haveY = ops[len(ops)-1].num, true
			}
		case "Td", "TD":
			if len(ops) >= 2 && ops[len(ops)-1].isNum {
				if dy := ops[len(ops)-1].num; dy != 0 {
					y += dy
					haveY = true
				} else if dx := ops[len(ops)-2].num; dx > 0 && lastShown && !lineStart {
					out.WriteString(" ")
				}
			}
		case "T*":
			newline()
		case "Tj":
			if len(ops) >= 1 && ops[len(ops)-1].isStr {
				show(decodeShown(ops[len(ops)-1].str, cmap))
			}
		case "'", "\"":
			newline()
			if len(ops) >= 1 && ops[len(ops)-1].isStr {
				show(decodeShown(ops[len(ops)-1].str, cmap))
			}
		case "TJ":
			if len(ops) >= 1 && ops[len(ops)-1].isArr {
				var sb strings.Builder
				for _, e := range ops[len(ops)-1].arr {
					switch {
					case e.isStr:
						sb.WriteString(decodeShown(e.str, cmap))
					case e.isNum && e.num < -200:
						sb.WriteString(" ")
					}
				}
				show(sb.String())
			}
		case "ET":
			if lastShown && !lineStart {
				// The next text object decides: same line adds a space.
				out.WriteString(" ")
			}
		}
		ops = ops[:0]
	}
}
