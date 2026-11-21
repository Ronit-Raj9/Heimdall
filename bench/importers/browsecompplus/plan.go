package browsecompplus

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/maximhq/bifrost/bench/research"
	"github.com/maximhq/bifrost/bench/research/standin"
)

// searchResults is how many results the reference asks for, the most the
// websearch tool returns (research.SearchResults).
const searchResults = research.SearchResults

// corpusIndex is the suite's corpus as the corpus service ranks it: the
// stand-in's BM25 (research/standin), which assets/corpus/server.py follows
// exactly, so the importer knows which query surfaces which page.
type corpusIndex struct {
	docs  []corpusDoc
	byURL map[string]int
	bm25  *standin.Corpus
	df    map[string]int
}

// buildCorpus makes the suite's corpus from the documents of the selected
// queries (gold, evidence and hard negatives), one per docid, sorted by
// docid. Pages are cited and fetched by URL, so two documents with one URL
// (upstream has a few: two crawls of one page) are told apart first by
// distinctURLs, which rewrites the queries in place.
func buildCorpus(qs []query) (*corpusIndex, error) {
	distinctURLs(qs)
	byID := map[string]doc{}
	for _, q := range qs {
		for _, set := range [][]doc{q.Gold, q.Evidence, q.Negative} {
			for _, d := range set {
				if prev, ok := byID[d.DocID]; ok && (prev.URL != d.URL || prev.Text != d.Text) {
					return nil, fmt.Errorf("docid %s appears with two different texts or URLs", d.DocID)
				}
				byID[d.DocID] = d
			}
		}
	}
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return docidLess(ids[i], ids[j]) })
	ix := &corpusIndex{byURL: map[string]int{}, df: map[string]int{}}
	type sdoc struct {
		URL           string `json:"url"`
		Title         string `json:"title"`
		PublishedDate string `json:"published_date"`
		Content       string `json:"content"`
	}
	var sdocs []sdoc
	for _, id := range ids {
		d := byID[id]
		title, date, off := splitFrontMatter(d.Text, d.URL)
		key := research.NormalizeURL(d.URL)
		if prev, dup := ix.byURL[key]; dup {
			return nil, fmt.Errorf("docids %s and %s share the URL %s", ix.docs[prev].DocID, id, d.URL)
		}
		ix.byURL[key] = len(ix.docs)
		cd := corpusDoc{DocID: id, URL: d.URL, Title: title, Date: date, BodyOffset: off, Text: d.Text}
		ix.docs = append(ix.docs, cd)
		// The stand-in wants a date on every page; it does not rank by it.
		pd := date
		if pd == "" {
			pd = "0001-01-01"
		}
		body := string([]rune(d.Text)[off:])
		if strings.TrimSpace(body) == "" {
			body = title
		}
		sdocs = append(sdocs, sdoc{URL: d.URL, Title: title, PublishedDate: pd, Content: body})
		seen := map[string]bool{}
		for _, t := range standin.Tokens(body) {
			if !seen[t] {
				seen[t] = true
				ix.df[t]++
			}
		}
	}
	raw, err := json.Marshal(map[string]any{"name": "browsecomp-plus", "docs": sdocs})
	if err != nil {
		return nil, err
	}
	if ix.bm25, err = standin.ParseCorpus(raw); err != nil {
		return nil, fmt.Errorf("index the corpus: %w", err)
	}
	return ix, nil
}

// docidQuery is the query parameter distinctURLs adds to a duplicate URL.
const docidQuery = "bcp-docid"

// distinctURLs gives every docid its own URL. Upstream's corpus has
// documents with the same URL (after research.NormalizeURL) and different
// texts. The smallest docid keeps the URL; every other one gets the
// parameter bcp-docid=<docid> added to its query, in every query's gold,
// evidence and negative lists, so citations, fetches and evidence recall
// still name one page each.
func distinctURLs(qs []query) {
	// First give every page an http(s) URL: the corpus service and the
	// websearch tool serve only those, and upstream has a few others
	// (chrome-extension://.../https://..., empty).
	for qi := range qs {
		for _, set := range [][]doc{qs[qi].Gold, qs[qi].Evidence, qs[qi].Negative} {
			for i := range set {
				set[i].URL = httpURL(set[i].URL, set[i].DocID)
			}
		}
	}
	byKey := map[string][]string{}
	urlOf := map[string]string{}
	for _, q := range qs {
		for _, set := range [][]doc{q.Gold, q.Evidence, q.Negative} {
			for _, d := range set {
				if _, ok := urlOf[d.DocID]; ok {
					continue
				}
				urlOf[d.DocID] = d.URL
				k := research.NormalizeURL(d.URL)
				byKey[k] = append(byKey[k], d.DocID)
			}
		}
	}
	rewrite := map[string]string{}
	for _, ids := range byKey {
		if len(ids) < 2 {
			continue
		}
		sort.Slice(ids, func(i, j int) bool { return docidLess(ids[i], ids[j]) })
		for _, id := range ids[1:] {
			rewrite[id] = withDocID(urlOf[id], id)
		}
	}
	if len(rewrite) == 0 {
		return
	}
	for qi := range qs {
		for _, set := range [][]doc{qs[qi].Gold, qs[qi].Evidence, qs[qi].Negative} {
			for i := range set {
				if u, ok := rewrite[set[i].DocID]; ok {
					set[i].URL = u
				}
			}
		}
	}
}

// placeholderHost names pages whose upstream URL holds no http(s) URL.
const placeholderHost = "https://browsecomp-plus.invalid/doc/"

// httpURL returns raw when it is an http(s) URL with a host, else the first
// http(s) URL embedded in it (chrome-extension://<id>/https://...), else a
// placeholder naming the docid.
func httpURL(raw, id string) string {
	if isHTTP(raw) {
		return raw
	}
	for _, scheme := range []string{"https://", "http://"} {
		if i := strings.Index(raw, scheme); i > 0 && isHTTP(raw[i:]) {
			return raw[i:]
		}
	}
	return placeholderHost + url.PathEscape(id)
}

func isHTTP(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// withDocID adds bcp-docid=<id> to the query of raw, before any fragment.
func withDocID(raw, id string) string {
	frag := ""
	if i := strings.IndexByte(raw, '#'); i >= 0 {
		raw, frag = raw[:i], raw[i:]
	}
	sep := "?"
	if strings.Contains(raw, "?") {
		sep = "&"
	}
	return raw + sep + docidQuery + "=" + id + frag
}

// docidLess orders numeric docids numerically, others as text.
func docidLess(a, b string) bool {
	if len(a) != len(b) && isDigits(a) && isDigits(b) {
		return len(a) < len(b)
	}
	return a < b
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

// returns reports whether searching q (as the websearch tool sends it)
// returns url among the first n results.
func (ix *corpusIndex) returns(q, url string, n int) bool {
	key := research.NormalizeURL(url)
	for _, h := range ix.bm25.Search(research.NormalizeQuery(q), n) {
		if research.NormalizeURL(h.Doc.URL) == key {
			return true
		}
	}
	return false
}

// distinctive is a query of the doc's n rarest body terms (ties by term),
// a search that usually finds the doc itself. Terms in skip are left out.
func (ix *corpusIndex) distinctive(d corpusDoc, n int, skip map[string]bool) string {
	body := string([]rune(d.Text)[d.BodyOffset:])
	counts := map[string]int{}
	for _, t := range standin.Tokens(body) {
		counts[t]++
	}
	type term struct {
		t     string
		score float64
	}
	var ts []term
	total := float64(len(ix.docs))
	for t, c := range counts {
		if len(t) < 3 || skip[t] {
			continue
		}
		idf := math.Log(1 + (total-float64(ix.df[t])+0.5)/(float64(ix.df[t])+0.5))
		ts = append(ts, term{t, idf * math.Min(float64(c), 3)})
	}
	sort.Slice(ts, func(i, j int) bool {
		if ts[i].score != ts[j].score {
			return ts[i].score > ts[j].score
		}
		return ts[i].t < ts[j].t
	})
	var words []string
	for i := 0; i < len(ts) && i < n; i++ {
		words = append(words, ts[i].t)
	}
	return strings.Join(words, " ")
}

// refPlan is what a task's reference control does, written into
// solution/reference.sh. It never names the answer: pages are identified by
// the sha256 of their URL (the reference learns the URL from its search
// results), no search query holds the answer, and the answer is either a
// span of a gold page the reference fetched (Span) or, when no gold page
// holds it verbatim, the published ciphertext (Cipher), decrypted by the
// script.
type refPlan struct {
	Steps  []refStep `json:"steps"`
	Span   *refSpan  `json:"span,omitempty"`
	Cipher string    `json:"cipher,omitempty"`
	Canary string    `json:"canary,omitempty"`
	// Kind says how the plan was found, for the script's comment and the
	// import's summary.
	Kind string `json:"-"`
	// Gold is the gold page the plan reads, for the mutant control.
	Gold string `json:"-"`
}

// refStep is a search that must return the page whose URL hashes to
// Expect, or a fetch of that page.
type refStep struct {
	Search string `json:"search,omitempty"`
	Expect string `json:"expect,omitempty"`
	Fetch  string `json:"fetch,omitempty"`
}

// refSpan is the answer's place in the fetched page: characters (code
// points) Start to Start+Length of the page's full text.
type refSpan struct {
	Page   string `json:"page"`
	Start  int    `json:"start"`
	Length int    `json:"length"`
}

// urlSHA is how a plan names a page: the sha256 of its URL exactly as the
// corpus holds it, which is what search results return.
func urlSHA(u string) string {
	sum := sha256.Sum256([]byte(u))
	return hex.EncodeToString(sum[:])
}

// names reports whether s names the answer: the answer's normalised words
// appear, as whole words, in s's.
func names(s, answer string) bool {
	a := normalizeAnswer(answer)
	return a != "" && strings.Contains(" "+normalizeAnswer(s)+" ", " "+a+" ")
}

// errNoPlan says why no reference could be planned.
var errNoPlan = errors.New("no search over the corpus returns a gold page without naming the answer")

// planReference searches like an agent would: for a gold page (one that
// holds the answer verbatim first), the first of the question, the page's
// title and queries of its rarest terms that returns the page among the
// first results and does not name the answer; then it fetches the page and
// reads the answer from it.
func (ix *corpusIndex) planReference(q query) (refPlan, error) {
	type cand struct {
		d     corpusDoc
		start int
	}
	var golds []cand
	for _, g := range q.Gold {
		i, ok := ix.byURL[research.NormalizeURL(g.URL)]
		if !ok {
			continue
		}
		d := ix.docs[i]
		start := findFold(d.Text, q.Answer)
		if start >= 0 && !answersMatch(string([]rune(d.Text)[start:start+utf8.RuneCountInString(q.Answer)]), q.Answer) {
			start = -1 // the case-insensitive find and the grader's normalisation disagree
		}
		golds = append(golds, cand{d, start})
	}
	sort.SliceStable(golds, func(i, j int) bool { return golds[i].start >= 0 && golds[j].start < 0 })
	skip := map[string]bool{}
	for _, t := range standin.Tokens(q.Answer) {
		skip[t] = true
	}
	for _, g := range golds {
		var search string
		for _, s := range []string{q.Question, g.d.Title, ix.distinctive(g.d, 6, skip), ix.distinctive(g.d, 12, skip), ix.distinctive(g.d, 24, skip)} {
			if research.NormalizeQuery(s) != "" && !names(s, q.Answer) && ix.returns(s, g.d.URL, searchResults) {
				search = s
				break
			}
		}
		if search == "" {
			continue
		}
		h := urlSHA(g.d.URL)
		p := refPlan{Steps: []refStep{{Search: search, Expect: h}, {Fetch: h}}, Gold: g.d.URL}
		if g.start >= 0 {
			p.Span = &refSpan{Page: h, Start: g.start, Length: utf8.RuneCountInString(q.Answer)}
			p.Kind = "the reference searches, fetches a gold page and reads the answer from it"
		} else {
			p.Cipher, p.Canary = q.Cipher, Canary
			p.Kind = "no gold page holds the answer verbatim: the reference searches, fetches a gold page and decrypts the published answer"
		}
		return p, nil
	}
	return refPlan{}, errNoPlan
}

// findFold is the character (code point) offset of the first
// case-insensitive occurrence of needle in text, or -1.
func findFold(text, needle string) int {
	t, n := []rune(text), []rune(needle)
	if len(n) == 0 || len(n) > len(t) {
		return -1
	}
	for i := 0; i+len(n) <= len(t); i++ {
		if strings.EqualFold(string(t[i:i+len(n)]), needle) {
			return i
		}
	}
	return -1
}
