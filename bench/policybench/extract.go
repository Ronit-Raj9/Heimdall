// Package policybench turns a policy or SOP document into a compliance
// benchmark (lane Z23): the document's text is split into numbered clauses,
// a pinned model drafts three support scenarios per clause (the normal
// case, an edge case and a customer who pushes to break the rule) through
// the gateway under a hard budget, and each scenario becomes a support task
// in the Y14 scenario shape (simulated customer, seeded account, checks.yaml
// tied to the clause, reference and violating controls). Drafted tasks wait
// in the document's staging folder until a person accepts them; readiness
// then proves the compliant reference passes and the violating control
// fails. The report reads the evaluations of the accepted tasks: per-clause
// pass rate with 95% ranges and plain-words explanations such as "breaks
// clause 4 when the customer insists", exported as Markdown or PDF.
package policybench

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

// Source formats Extract reads.
const (
	FormatMarkdown = "markdown"
	FormatText     = "text"
	FormatPDF      = "pdf"
	FormatDOCX     = "docx"
)

// MaxSourceBytes caps an uploaded document.
const MaxSourceBytes = 10 << 20

// MaxTextBytes caps the extracted text.
const MaxTextBytes = 1 << 20

// FormatOf names a document's format from its file name, then its bytes.
func FormatOf(name string, data []byte) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".pdf":
		return FormatPDF
	case ".docx":
		return FormatDOCX
	case ".md", ".markdown":
		return FormatMarkdown
	case ".txt", ".text":
		return FormatText
	}
	switch {
	case bytes.HasPrefix(data, []byte("%PDF-")):
		return FormatPDF
	case bytes.HasPrefix(data, []byte("PK\x03\x04")):
		return FormatDOCX
	}
	return FormatText
}

// Extract returns the plain text of a document. Markdown and text are kept
// as written (normalised line ends); a DOCX gives one line per paragraph,
// headings as Markdown headings and list paragraphs as "- " lines; a PDF is
// read with pdftotext when the server has it, else with the built-in reader
// (text drawn with simple fonts, Flate streams), which refuses a PDF whose
// text it cannot read (a scan, or fonts without a Unicode map) instead of
// returning garbage.
func Extract(ctx context.Context, format string, data []byte) (string, error) {
	if len(data) == 0 {
		return "", errors.New("the document is empty")
	}
	if len(data) > MaxSourceBytes {
		return "", fmt.Errorf("the document is %d bytes; upload at most %d MiB", len(data), MaxSourceBytes>>20)
	}
	var text string
	var err error
	switch format {
	case FormatMarkdown, FormatText, "":
		if !utf8.Valid(data) {
			return "", errors.New("the text is not UTF-8; save it as UTF-8 and upload again")
		}
		text = string(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")))
	case FormatDOCX:
		text, err = docxText(data)
	case FormatPDF:
		text, err = pdfText(ctx, data)
	default:
		return "", fmt.Errorf("format %q: want markdown, text, pdf or docx", format)
	}
	if err != nil {
		return "", err
	}
	text = normalise(text)
	if strings.TrimSpace(text) == "" {
		return "", errors.New("no text found in the document (a scanned PDF has none: export it with its text, or paste the text)")
	}
	if len(text) > MaxTextBytes {
		return "", fmt.Errorf("the text is %d bytes; split the document (at most %d KiB of text)", len(text), MaxTextBytes>>10)
	}
	return text, nil
}

func normalise(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.ReplaceAll(s, " ", " ")
	s = strings.ReplaceAll(s, "\f", "\n")
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	return strings.TrimSpace(strings.Join(lines, "\n")) + "\n"
}

// docxText reads word/document.xml: paragraphs, tabs and breaks.
func docxText(data []byte) (string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("not a DOCX file: %v", err)
	}
	var doc *zip.File
	for _, f := range zr.File {
		if f.Name == "word/document.xml" {
			doc = f
		}
	}
	if doc == nil {
		return "", errors.New("not a DOCX file: word/document.xml is missing")
	}
	if doc.UncompressedSize64 > 50<<20 {
		return "", errors.New("the DOCX text part is over 50 MiB")
	}
	rc, err := doc.Open()
	if err != nil {
		return "", err
	}
	defer rc.Close()
	dec := xml.NewDecoder(io.LimitReader(rc, 50<<20))
	var out, para strings.Builder
	style, list, inText := "", false, false
	flush := func() {
		p := strings.TrimSpace(para.String())
		para.Reset()
		if p != "" {
			switch {
			case strings.HasPrefix(strings.ToLower(style), "heading"):
				n := 1
				if d := strings.TrimPrefix(strings.ToLower(style), "heading"); len(d) == 1 && d[0] >= '1' && d[0] <= '6' {
					n = int(d[0] - '0')
				}
				p = strings.Repeat("#", n) + " " + p
			case strings.EqualFold(style, "title"):
				p = "# " + p
			case list:
				p = "- " + p
			}
			out.WriteString(p)
			out.WriteString("\n\n")
		}
		style, list = "", false
	}
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("read the DOCX text: %v", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "pStyle":
				for _, a := range t.Attr {
					if a.Name.Local == "val" {
						style = a.Value
					}
				}
			case "numPr":
				list = true
			case "t":
				inText = true
			case "tab":
				para.WriteString("\t")
			case "br", "cr":
				para.WriteString(" ")
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "t":
				inText = false
			case "p":
				flush()
			}
		case xml.CharData:
			if inText {
				para.Write(t)
			}
		}
	}
	flush()
	return out.String(), nil
}

// pdftotextPath finds pdftotext; a var so tests can turn it off.
var pdftotextPath = func() string {
	p, err := exec.LookPath("pdftotext")
	if err != nil {
		return ""
	}
	return p
}

func pdfText(ctx context.Context, data []byte) (string, error) {
	if bin := pdftotextPath(); bin != "" {
		if s, err := runPdftotext(ctx, bin, data); err == nil && strings.TrimSpace(s) != "" {
			return s, nil
		}
	}
	return PDFText(data)
}

func runPdftotext(ctx context.Context, bin string, data []byte) (string, error) {
	dir, err := os.MkdirTemp("", "policy-pdf-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	in := filepath.Join(dir, "in.pdf")
	if err := os.WriteFile(in, data, 0o600); err != nil {
		return "", err
	}
	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, bin, "-q", "-enc", "UTF-8", "-nopgbrk", in, "-")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return "", err
	}
	if stdout.Len() > MaxTextBytes*4 {
		return "", errors.New("pdftotext output too large")
	}
	return stdout.String(), nil
}
