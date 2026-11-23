package onboard

import (
	"testing"
)

// Row references name the unit of their source (fix wave 3, N235): a CSV
// keeps the file row a spreadsheet shows (the header is row 1), JSON Lines
// and pasted text count lines, a JSON array counts items. A column mapping
// does not renumber the rows.
func TestRowRefsNameTheUnit(t *testing.T) {
	cases := []struct {
		name, file, data string
		plan             Plan
		want             string
	}{
		{"csv", "faq.csv", "question,answer\nWhat is 2+2?,4\n", Plan{}, "row 2"},
		{"csv with mapping", "faq.csv", "q,a\nWhat is 2+2?,4\n", Plan{Mapping: map[string]string{"question": "q", "expected": "a"}}, "row 2"},
		{"jsonl", "faq.jsonl", "{\"question\":\"What is 2+2?\",\"answer\":\"4\"}\n", Plan{}, "line 1"},
		{"json array", "faq.json", "[{\"question\":\"What is 2+2?\",\"answer\":\"4\"}]", Plan{}, "item 1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			inv, err := inspectRows(c.file, []byte(c.data), c.plan, false)
			if err != nil {
				t.Fatal(err)
			}
			if len(inv.Samples) == 0 {
				t.Fatalf("no sample: %+v", inv)
			}
			if got := inv.Samples[0].Ref; got != c.want {
				t.Fatalf("ref %q, want %q", got, c.want)
			}
		})
	}
}
