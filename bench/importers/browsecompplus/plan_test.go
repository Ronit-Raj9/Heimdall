package browsecompplus

import "testing"

func TestDistinctURLs(t *testing.T) {
	a := doc{DocID: "18046", URL: "https://en.wikipedia.org/wiki/X", Text: "two"}
	b := doc{DocID: "15261", URL: "https://EN.wikipedia.org/wiki/X/", Text: "one"}
	c := doc{DocID: "30148", URL: "https://g.example/b.html?x=1#ch10", Text: "c1"}
	d := doc{DocID: "61171", URL: "https://g.example/b.html?x=1#ch10", Text: "c2"}
	e := doc{DocID: "7", URL: "https://other.example/", Text: "e"}
	qs := []query{
		{ID: "1", Gold: []doc{a}, Negative: []doc{c, e}},
		{ID: "2", Evidence: []doc{b, a}, Negative: []doc{d}},
	}
	distinctURLs(qs)
	want := map[string]string{
		"15261": "https://EN.wikipedia.org/wiki/X/",
		"18046": "https://en.wikipedia.org/wiki/X?bcp-docid=18046",
		"30148": "https://g.example/b.html?x=1#ch10",
		"61171": "https://g.example/b.html?x=1&bcp-docid=61171#ch10",
		"7":     "https://other.example/",
	}
	for _, q := range qs {
		for _, set := range [][]doc{q.Gold, q.Evidence, q.Negative} {
			for _, x := range set {
				if x.URL != want[x.DocID] {
					t.Errorf("query %s docid %s: %s, want %s", q.ID, x.DocID, x.URL, want[x.DocID])
				}
			}
		}
	}
	ix, err := buildCorpus(qs)
	if err != nil {
		t.Fatalf("corpus with duplicate URLs: %v", err)
	}
	if len(ix.docs) != 5 {
		t.Errorf("%d docs, want 5", len(ix.docs))
	}
}

func TestHTTPURL(t *testing.T) {
	for _, c := range []struct{ raw, id, want string }{
		{"https://a.example/x", "1", "https://a.example/x"},
		{"chrome-extension://efaid/https://t.example/a.pdf", "2", "https://t.example/a.pdf"},
		{"", "3", "https://browsecomp-plus.invalid/doc/3"},
		{"file:///tmp/x", "4", "https://browsecomp-plus.invalid/doc/4"},
	} {
		if got := httpURL(c.raw, c.id); got != c.want {
			t.Errorf("httpURL(%q) = %q, want %q", c.raw, got, c.want)
		}
	}
	qs := []query{{ID: "1", Gold: []doc{{DocID: "9", URL: "chrome-extension://e/https://t.example/a.pdf", Text: "a"}}, Negative: []doc{{DocID: "10", URL: "https://t.example/a.pdf", Text: "b"}}}}
	if _, err := buildCorpus(qs); err != nil {
		t.Fatal(err)
	}
	if qs[0].Gold[0].URL != "https://t.example/a.pdf" || qs[0].Negative[0].URL != "https://t.example/a.pdf?bcp-docid=10" {
		t.Errorf("%s %s", qs[0].Gold[0].URL, qs[0].Negative[0].URL)
	}
}
