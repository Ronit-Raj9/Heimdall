package handlers

import (
	"bufio"
	"bytes"
	"strings"
	"unicode/utf8"

	benchdocs "github.com/maximhq/bifrost/bench/docs"
)

// Docs search (fix wave 2, N138): GET /docs and GET /public/docs take an
// optional q that matches a guide's title, name or body.

const (
	docSearchMinLen  = 2
	docSearchMaxLen  = 80
	docSnippetMaxLen = 160
)

// docSearchQuery reads q from a docs list request. It returns ok=false with
// a message when q is present but not 2 to 80 characters after trimming.
func docSearchQuery(raw string) (q string, ok bool, msg string) {
	q = strings.TrimSpace(raw)
	if raw == "" {
		return "", true, ""
	}
	if n := utf8.RuneCountInString(q); n < docSearchMinLen || n > docSearchMaxLen {
		return "", false, "q must be 2 to 80 characters"
	}
	return q, true, ""
}

// searchDocs lists the guides among names, through read, whose title, name or
// markdown contains q (case-insensitive). Title or name matches come first;
// each hit carries the first body line that contains q as its snippet. An
// empty q lists every readable guide with no snippet, in names order.
func searchDocs(names []string, read func(string) ([]byte, bool), q string) []DocSummary {
	lq := strings.ToLower(q)
	var head, body []DocSummary
	for _, n := range names {
		data, ok := read(n)
		if !ok {
			continue
		}
		s := DocSummary{Name: n, Title: docTitle(n, data)}
		if lq == "" {
			head = append(head, s)
			continue
		}
		s.Snippet = docSnippet(data, lq)
		switch {
		case strings.Contains(strings.ToLower(s.Title), lq) || strings.Contains(strings.ToLower(n), lq):
			head = append(head, s)
		case s.Snippet != "":
			body = append(body, s)
		}
	}
	out := make([]DocSummary, 0, len(head)+len(body))
	out = append(out, head...)
	return append(out, body...)
}

// docSnippet is the first line of a guide (headings included) that contains
// lq, with markdown marks dropped and cut to about 160 characters around the
// match; empty when no line contains it.
func docSnippet(data []byte, lq string) string {
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), benchdocs.MaxBytes)
	for sc.Scan() {
		line := strings.TrimSpace(strings.NewReplacer("#", "", "*", "", "`", "").Replace(sc.Text()))
		if line == "" {
			continue
		}
		at := strings.Index(strings.ToLower(line), lq)
		if at < 0 {
			continue
		}
		return cutAround([]rune(line), utf8.RuneCountInString(line[:at]), utf8.RuneCountInString(lq))
	}
	return ""
}

// cutAround keeps at most docSnippetMaxLen runes of line, centred on the
// match at rune offset at of length n, marking cut ends with "...".
func cutAround(line []rune, at, n int) string {
	if len(line) <= docSnippetMaxLen {
		return string(line)
	}
	start := at + n/2 - docSnippetMaxLen/2
	if start < 0 {
		start = 0
	}
	end := start + docSnippetMaxLen
	if end > len(line) {
		end = len(line)
		start = end - docSnippetMaxLen
	}
	out := strings.TrimSpace(string(line[start:end]))
	if start > 0 {
		out = "..." + out
	}
	if end < len(line) {
		out += "..."
	}
	return out
}
