package browsecompplus

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The fixture is invented: three queries in the shape of
// Tevatron/browsecomp-plus rows (query_id, query, answer, gold_docs,
// evidence_docs, negative_docs with docid, text and url), about places and
// people that do not exist, on reserved .example domains. Tests obfuscate it
// with the canary, as upstream publishes rows, or write it decrypted, as the
// authors' decrypt_dataset.py does.
type fxDoc struct{ id, url, text string }

type fxQuery struct {
	id, question, answer string
	gold, evidence, neg  []fxDoc
}

var (
	fxShipPage = fxDoc{"101", "https://brindle-cove.example/keepers/first-ships", "---\ntitle: First ships of the Brindle Cove keepers\ndate: 2024-05-02\n---\nEvery keeper of the Brindle Cove light names a cat after the first ship they served on. Keeper Ilse Varrow served first on the Marrow Gull, a herring drifter, and her cat is called Marrow Gull too. The light was automated in 1998.\n"}
	fxShipLog  = fxDoc{"102", "https://brindle-cove.example/history/light", "---\ntitle: History of the Brindle Cove light\ndate: 2019-11-20\n---\nThe Brindle Cove light was built in 1871. Its last resident keeper, Ilse Varrow, retired when the light was automated.\n"}
	fxNegCats  = fxDoc{"103", "https://cats-of-the-coast.example/names", "---\ntitle: Popular cat names on the coast\ndate: 2022-01-15\n---\nThe most popular cat names along the coast are Pilchard, Biscuit and Captain. Few cats are named after ships.\n"}
	fxNegGull  = fxDoc{"104", "https://birds.example/gulls", "---\ntitle: Gulls of the northern coast\ndate: 2021-06-01\n---\nHerring gulls and kittiwakes nest on the cliffs. The marrow of a gull's bones is hollow.\n"}
	fxFishPage = fxDoc{"201", "https://skerry-landings.example/2025/week-12", "---\ntitle: Skerry landings, week 12\ndate: 2025-03-24\n---\nIn week 12 the Skerry fleet landed 1,250 kilograms of cod, its best week of the spring.\n"}
	fxFishNeg  = fxDoc{"202", "https://skerry-landings.example/2025/week-11", "---\ntitle: Skerry landings, week 11\ndate: 2025-03-17\n---\nIn week 11 the Skerry fleet landed 980 kilograms of cod.\n"}
	fxNoFront  = fxDoc{"203", "https://fleet-notes.example/plain", "Fleet notes without front matter.\nThe harbour master counts every box of cod by hand.\n"}
	fxLongPage = fxDoc{"301", "https://harrowgate.example/society/founder", "---\ntitle: Who founded the Harrowgate Rail Society\ndate: 2023-08-08\n---\nThe society was founded by the lighthouse keeper's daughter, Ada Fenwick, in 1952.\n"}

	fixtureQueries = []fxQuery{
		{"9001", "A keeper of an invented cove light named her cat after the first ship she served on, a herring drifter. What is the cat called?", "Marrow Gull",
			[]fxDoc{fxShipPage}, []fxDoc{fxShipPage, fxShipLog}, []fxDoc{fxNegCats, fxNegGull}},
		{"9002", "How many kilograms of cod did the Skerry fleet land in its best week of spring 2025?", "1250",
			[]fxDoc{fxFishPage}, []fxDoc{fxFishPage}, []fxDoc{fxFishNeg, fxNoFront}},
		{"9003", "Who founded the invented Harrowgate Rail Society?", "The lighthouse keeper's daughter, Ada Fenwick",
			[]fxDoc{fxLongPage}, []fxDoc{fxLongPage}, []fxDoc{fxNegCats}},
	}
)

// writeFixture writes the fixture as JSONL, obfuscated or decrypted.
func writeFixture(t *testing.T, dir string, obfuscated bool) string {
	t.Helper()
	enc := func(s string) string {
		if obfuscated {
			return Encrypt(s, Canary)
		}
		return s
	}
	docs := func(in []fxDoc) []map[string]string {
		out := []map[string]string{}
		for _, d := range in {
			out = append(out, map[string]string{"docid": enc(d.id), "text": enc(d.text), "url": enc(d.url)})
		}
		return out
	}
	var b strings.Builder
	for _, q := range fixtureQueries {
		row := map[string]any{"query_id": q.id, "query": enc(q.question), "answer": enc(q.answer),
			"gold_docs": docs(q.gold), "negative_docs": docs(q.neg), "evidence_docs": docs(q.evidence)}
		line, err := json.Marshal(row)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	p := filepath.Join(dir, "queries.jsonl")
	if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}
