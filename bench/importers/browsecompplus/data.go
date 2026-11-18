package browsecompplus

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// rawRow is one line of the queries file: a row of Tevatron/browsecomp-plus
// (fields per the dataset card, revision 144cff8e, checked 2026-11-18). In
// the published dataset every field but query_id is obfuscated; in the
// authors' decrypted JSONL every field is plain text.
type rawRow struct {
	QueryID  json.RawMessage `json:"query_id"`
	Query    string          `json:"query"`
	Answer   string          `json:"answer"`
	Gold     []rawDoc        `json:"gold_docs"`
	Negative []rawDoc        `json:"negative_docs"`
	Evidence []rawDoc        `json:"evidence_docs"`
}

type rawDoc struct {
	DocID string `json:"docid"`
	Text  string `json:"text"`
	URL   string `json:"url"`
}

// doc is one corpus document, decrypted.
type doc struct {
	DocID string
	URL   string
	Text  string
}

// query is one decrypted query. Answer is the plain-text answer: it lives
// in memory only and is never written, printed or logged; the suite gets
// Cipher, the answer as upstream publishes it.
type query struct {
	ID       string
	Question string
	Answer   string
	Cipher   string
	Gold     []doc
	Evidence []doc
	Negative []doc
}

// inputKind says how the queries file was written.
const (
	inputObfuscated = "obfuscated" // the dataset as published (converted from parquet)
	inputDecrypted  = "decrypted"  // the authors' decrypt_dataset.py output
)

// findQueries finds the queries JSONL under from: the file itself, or the
// *.jsonl files of a folder. A folder of parquet shards only is refused
// with the command that converts them.
func findQueries(from string) ([]string, error) {
	fi, err := os.Stat(from)
	if err != nil {
		return nil, fmt.Errorf("-from %s: %w", from, err)
	}
	if !fi.IsDir() {
		if strings.HasSuffix(from, ".parquet") {
			return nil, parquetRefusal([]string{from})
		}
		return []string{from}, nil
	}
	files, _ := filepath.Glob(filepath.Join(from, "*.jsonl"))
	if len(files) > 0 {
		sort.Strings(files)
		return files, nil
	}
	pq, _ := filepath.Glob(filepath.Join(from, "*.parquet"))
	if more, _ := filepath.Glob(filepath.Join(from, "data", "*.parquet")); len(more) > 0 {
		pq = append(pq, more...)
	}
	if len(pq) > 0 {
		sort.Strings(pq)
		return nil, parquetRefusal(pq)
	}
	return nil, fmt.Errorf("-from %s: no *.jsonl file there (the dataset's rows as JSONL; see docs/importers-research.md)", from)
}

func parquetRefusal(files []string) error {
	return fmt.Errorf("%s is parquet, which this importer does not read; convert it once with the script shipped beside the importer, which keeps every field as published (obfuscated):\n  python3 importers/browsecompplus/assets/tojsonl.py %s > queries.jsonl\nthen import with -from queries.jsonl", files[0], strings.Join(files, " "))
}

// readQueries streams the queries files. For each row whose query_id is in
// want (every row when want is empty) it decrypts the question and the
// answer and asks keep; only a kept row has its pages decrypted and is
// returned, so a large shard is never held in memory. keep's stop ends the
// reading. kind forces the input kind; empty detects it from the first row.
func readQueries(ctx context.Context, files []string, want map[string]bool, kind string, keep func(q query) (take, stop bool)) ([]query, string, error) {
	var out []query
	for _, path := range files {
		f, err := os.Open(path)
		if err != nil {
			return nil, kind, err
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1<<20), 512<<20)
		n := 0
		for sc.Scan() {
			if err := ctx.Err(); err != nil {
				f.Close()
				return nil, kind, err
			}
			n++
			line := bytes.TrimSpace(sc.Bytes())
			if len(line) == 0 {
				continue
			}
			var r rawRow
			if err := json.Unmarshal(line, &r); err != nil {
				f.Close()
				return nil, kind, fmt.Errorf("%s line %d: %w", path, n, err)
			}
			id := strings.Trim(string(r.QueryID), `" `)
			if id == "" {
				f.Close()
				return nil, kind, fmt.Errorf("%s line %d: no query_id", path, n)
			}
			if len(want) > 0 && !want[id] {
				continue
			}
			if kind == "" {
				kind = detectKind(r)
			}
			q, err := decodeHead(r, id, kind)
			if err != nil {
				f.Close()
				return nil, kind, fmt.Errorf("%s line %d (query %s): %w", path, n, id, err)
			}
			take, stop := keep(q)
			if take {
				if err := decodeDocs(&q, r, kind); err != nil {
					f.Close()
					return nil, kind, fmt.Errorf("%s line %d (query %s): %w", path, n, id, err)
				}
				out = append(out, q)
			}
			if stop {
				f.Close()
				return out, kind, nil
			}
		}
		err = sc.Err()
		f.Close()
		if err != nil {
			return nil, kind, fmt.Errorf("read %s: %w", path, err)
		}
	}
	return out, kind, nil
}

// detectKind: a published row's query is base64 that decrypts to text; a
// decrypted row's query is a sentence, which is not base64.
func detectKind(r rawRow) string {
	if _, err := Decrypt(r.Query, Canary); err == nil && !strings.ContainsAny(r.Query, " \n") {
		return inputObfuscated
	}
	return inputDecrypted
}

func field(kind, name, v string) (string, error) {
	if kind == inputDecrypted {
		return v, nil
	}
	s, err := Decrypt(v, Canary)
	if err != nil {
		return "", fmt.Errorf("%s: %w (is the file the published dataset? pass -set input=decrypted for the authors' decrypted JSONL)", name, err)
	}
	return s, nil
}

// decodeHead decrypts a row's question and answer.
func decodeHead(r rawRow, id, kind string) (query, error) {
	field := func(name, v string) (string, error) { return field(kind, name, v) }
	q := query{ID: id}
	var err error
	if q.Question, err = field("query", r.Query); err != nil {
		return q, err
	}
	if q.Answer, err = field("answer", r.Answer); err != nil {
		return q, err
	}
	q.Cipher = r.Answer
	if kind == inputDecrypted {
		q.Cipher = Encrypt(r.Answer, Canary)
	}
	return q, nil
}

// decodeDocs decrypts a kept row's pages.
func decodeDocs(q *query, r rawRow, kind string) error {
	field := func(name, v string) (string, error) { return field(kind, name, v) }
	var err error
	docs := func(name string, in []rawDoc) ([]doc, error) {
		out := make([]doc, 0, len(in))
		for i, d := range in {
			var x doc
			var err error
			if x.DocID, err = field(fmt.Sprintf("%s[%d].docid", name, i), d.DocID); err != nil {
				return nil, err
			}
			if x.URL, err = field(fmt.Sprintf("%s[%d].url", name, i), d.URL); err != nil {
				return nil, err
			}
			if x.Text, err = field(fmt.Sprintf("%s[%d].text", name, i), d.Text); err != nil {
				return nil, err
			}
			out = append(out, x)
		}
		return out, nil
	}
	if q.Gold, err = docs("gold_docs", r.Gold); err != nil {
		return err
	}
	if q.Evidence, err = docs("evidence_docs", r.Evidence); err != nil {
		return err
	}
	q.Negative, err = docs("negative_docs", r.Negative)
	return err
}

// corpusDoc is one line of the suite's corpus/corpus.jsonl, which the
// corpus service (assets/corpus/server.py) indexes.
type corpusDoc struct {
	DocID      string `json:"docid"`
	URL        string `json:"url"`
	Title      string `json:"title"`
	Date       string `json:"date,omitempty"`
	BodyOffset int    `json:"body_offset"`
	Text       string `json:"text"`
}

var dateRE = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}`)

// splitFrontMatter reads a document's YAML front matter (the corpus's
// documents start "---\ntitle: ...\ndate: ...\n---\n"): its title, its
// date when it is YYYY-MM-DD, and where the body starts, in characters. A
// document without front matter is titled by its first line.
func splitFrontMatter(text, url string) (title, date string, bodyOffset int) {
	body := text
	if strings.HasPrefix(text, "---\n") {
		if end := strings.Index(text[4:], "\n---"); end >= 0 {
			head := text[4 : 4+end]
			rest := text[4+end+4:]
			rest = strings.TrimPrefix(rest, "\n")
			body = rest
			for _, line := range strings.Split(head, "\n") {
				k, v, ok := strings.Cut(line, ":")
				if !ok {
					continue
				}
				v = strings.Trim(strings.TrimSpace(v), `"'`)
				switch strings.TrimSpace(k) {
				case "title":
					if title == "" {
						title = v
					}
				case "date":
					if m := dateRE.FindString(v); m != "" && date == "" {
						date = m
					}
				}
			}
		}
	}
	bodyOffset = len([]rune(text)) - len([]rune(body))
	if strings.TrimSpace(title) == "" {
		for _, line := range strings.Split(body, "\n") {
			if l := strings.TrimSpace(line); l != "" {
				title = l
				break
			}
		}
	}
	if r := []rune(strings.TrimSpace(title)); len(r) > 200 {
		title = string(r[:200])
	}
	title = strings.TrimSpace(title)
	if title == "" {
		title = url
	}
	return title, date, bodyOffset
}

// Exact answers. An answer is graded without a judge only when it is short
// and exact: at most four words and 48 characters, no alternatives ("or",
// "/", parentheses, ";"), no approximation, and not a date (dates are
// written in too many ways for a string match). verify.py's normalize and
// answer_matches are the Python side of normalizeAnswer and answersMatch; a
// test runs both.
const (
	maxExactWords = 4
	maxExactRunes = 48
)

var (
	months     = regexp.MustCompile(`(?i)\b(jan|feb|mar|apr|may|jun|jul|aug|sep|sept|oct|nov|dec)[a-z]*\b`)
	slashDate  = regexp.MustCompile(`\b\d{1,4}[/.-]\d{1,2}[/.-]\d{1,4}\b`)
	hedge      = regexp.MustCompile(`(?i)\b(or|approximately|approx|about|around|between|circa|ca)\b|[/();~≈]`)
	plainNum   = regexp.MustCompile(`^[-+]?(?:[0-9]+(?:\.[0-9]+)?|\.[0-9]+)$`)
	hasDigit   = regexp.MustCompile(`[0-9]`)
	articleSet = map[string]bool{"the": true, "a": true, "an": true}
)

// exactClass reports whether answer can be graded by answersMatch, and why
// not when it cannot.
func exactClass(answer string) (bool, string) {
	a := strings.TrimSpace(answer)
	switch {
	case normalizeAnswer(a) == "":
		return false, "the answer is empty after normalisation"
	case len([]rune(a)) > maxExactRunes || len(strings.Fields(a)) > maxExactWords:
		return false, fmt.Sprintf("the answer is longer than %d words or %d characters", maxExactWords, maxExactRunes)
	case hedge.MatchString(a):
		return false, "the answer holds alternatives or an approximation"
	case slashDate.MatchString(a) || (months.MatchString(a) && hasDigit.MatchString(a)):
		return false, "the answer is a date"
	}
	return true, ""
}

// normalizeAnswer: NFKC, lower case, every punctuation, symbol, separator
// and control character turned into a space, runs of spaces collapsed, and
// a leading article dropped when more words follow.
func normalizeAnswer(s string) string {
	s = strings.ToLower(norm.NFKC.String(s))
	s = strings.Map(func(r rune) rune {
		if unicode.IsPunct(r) || unicode.IsSymbol(r) || unicode.In(r, unicode.Z, unicode.C) {
			return ' '
		}
		return r
	}, s)
	words := strings.Fields(s)
	if len(words) > 1 && articleSet[words[0]] {
		words = words[1:]
	}
	return strings.Join(words, " ")
}

// asNumber reads a plain number, with thousands commas, "$" and "%"
// dropped.
func asNumber(s string) (float64, bool) {
	t := strings.TrimSpace(norm.NFKC.String(s))
	t = strings.TrimSpace(strings.NewReplacer(",", "", "$", "", "%", "").Replace(t))
	if !plainNum.MatchString(t) {
		return 0, false
	}
	f, err := strconv.ParseFloat(t, 64)
	return f, err == nil
}

// answersMatch is verify.py's answer_matches.
func answersMatch(got, want string) bool {
	if n, ok := asNumber(want); ok {
		g, ok := asNumber(got)
		return ok && g == n
	}
	g := normalizeAnswer(got)
	return g != "" && g == normalizeAnswer(want)
}

// errNoQueries is returned when the selection is empty.
var errNoQueries = errors.New("no query selected")
