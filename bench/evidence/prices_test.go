package evidence

import (
	"encoding/json"
	"errors"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

const priceFixture = `{
  "id": "test-snap",
  "source": "unit test",
  "captured_at": "2026-11-14T00:00:00Z",
  "models": {
    "claude-x": {"input_per_mtok": 3, "output_per_mtok": 15, "cache_read_per_mtok": 0.3, "cache_write_per_mtok": 3.75},
    "claude-big": {"input_per_mtok": 15, "output_per_mtok": 75, "cache_read_per_mtok": 1.5, "cache_write_per_mtok": 18.75}
  }
}
`

// writePrices writes a price file and returns its path.
func writePrices(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "pricing-snapshot.json")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// testPrices loads priceFixture.
func testPrices(t *testing.T) *PriceTable {
	t.Helper()
	pt, err := LoadPrices(writePrices(t, priceFixture))
	if err != nil {
		t.Fatal(err)
	}
	return pt
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-12 }

func TestLoadPrices(t *testing.T) {
	t.Run("fixture", func(t *testing.T) {
		pt := testPrices(t)
		if pt.ID != "test-snap" || len(pt.Models) != 2 {
			t.Fatalf("table = %+v", pt)
		}
		want := "test-snap@sha256:" + utils.SHA256Bytes([]byte(priceFixture))
		if pt.Ref() != want {
			t.Errorf("Ref() = %q, want %q", pt.Ref(), want)
		}
	})
	bad := []struct {
		name, body, want string
	}{
		{"no id", `{"models": {}}`, "missing id"},
		{"missing price", `{"id": "x", "models": {"m": {"input_per_mtok": 1, "output_per_mtok": 1, "cache_read_per_mtok": 1}}}`, "missing cache_write_per_mtok"},
		{"null price", `{"id": "x", "models": {"m": {"input_per_mtok": null, "output_per_mtok": 1, "cache_read_per_mtok": 1, "cache_write_per_mtok": 1}}}`, "missing input_per_mtok"},
		{"negative price", `{"id": "x", "models": {"m": {"input_per_mtok": -1, "output_per_mtok": 1, "cache_read_per_mtok": 1, "cache_write_per_mtok": 1}}}`, "negative input_per_mtok"},
		{"unknown field", `{"id": "x", "models": {"m": {"input_per_mtok": 1, "output_per_mtok": 1, "cache_read_per_mtok": 1, "cache_write_per_mtok": 1, "inptu_per_mtok": 2}}}`, "unknown field"},
		{"not json", `{`, "parse price snapshot"},
	}
	for _, tt := range bad {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadPrices(writePrices(t, tt.body))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want %q", err, tt.want)
			}
		})
	}
	t.Run("missing file", func(t *testing.T) {
		_, err := LoadPrices(filepath.Join(t.TempDir(), "absent.json"))
		if !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("err = %v, want one wrapping fs.ErrNotExist", err)
		}
	})
}

func TestParsePrices(t *testing.T) {
	fromBytes, err := ParsePrices([]byte(priceFixture), "embedded default")
	if err != nil {
		t.Fatal(err)
	}
	if fromBytes.Ref() != testPrices(t).Ref() {
		t.Errorf("Ref() = %q, want the file's %q", fromBytes.Ref(), testPrices(t).Ref())
	}
	if _, err := ParsePrices([]byte(`{"models": {}}`), "embedded default"); err == nil || !strings.Contains(err.Error(), "price snapshot embedded default: missing id") {
		t.Errorf("err = %v", err)
	}
}

// TestShippedSnapshot keeps deploy/pricing-snapshot.json loadable, honest
// about where it came from, and equal to the datasheet it names: every price
// must be that file's per-token price times one million.
func TestShippedSnapshot(t *testing.T) {
	path := filepath.Join("..", "deploy", "pricing-snapshot.json")
	pt, err := LoadPrices(path)
	if err != nil {
		t.Fatal(err)
	}
	const source = "bench/spike/datasheet/pricing.json"
	if pt.ID == "" || !strings.Contains(pt.Source, source) || !strings.Contains(pt.Source, "commit ") || pt.CapturedAt == "" {
		t.Errorf("snapshot provenance incomplete: %+v", pt)
	}
	if len(pt.Models) == 0 {
		t.Errorf("snapshot lists no models")
	}
	sum, err := utils.SHA256File(path)
	if err != nil {
		t.Fatal(err)
	}
	if pt.Ref() != pt.ID+"@sha256:"+sum {
		t.Errorf("Ref() = %q does not name the file hash %s", pt.Ref(), sum)
	}
	raw, err := os.ReadFile(filepath.Join("..", "spike", "datasheet", "pricing.json"))
	if os.IsNotExist(err) {
		t.Skipf("%s not in this tree; provenance not cross-checked", source)
	}
	if err != nil {
		t.Fatal(err)
	}
	var sheet map[string]map[string]any
	if err := json.Unmarshal(raw, &sheet); err != nil {
		t.Fatal(err)
	}
	// Rows outside the datasheet (other providers, added by hand) name
	// their own official price page and capture date in model_sources.
	snapRaw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var extra struct {
		ModelSources map[string]struct {
			URL  string `json:"url"`
			Date string `json:"date"`
		} `json:"model_sources"`
	}
	if err := json.Unmarshal(snapRaw, &extra); err != nil {
		t.Fatal(err)
	}
	for model, p := range pt.Models {
		t.Run(model, func(t *testing.T) {
			if src, own := extra.ModelSources[model]; own {
				if _, inSheet := sheet[model]; inSheet {
					t.Fatalf("%s is in %s; take its price from there", model, source)
				}
				if !strings.HasPrefix(src.URL, "https://") || len(src.Date) != len("2006-01-02") {
					t.Fatalf("%s: source %+v needs an https URL and a date", model, src)
				}
				return
			}
			row, ok := sheet[model]
			if !ok {
				t.Fatalf("%s is not in %s", model, source)
			}
			pairs := []struct {
				field string
				got   float64
			}{
				{"input_cost_per_token", p.InputPerMTok},
				{"output_cost_per_token", p.OutputPerMTok},
				{"cache_read_input_token_cost", p.CacheReadPerMTok},
				{"cache_creation_input_token_cost", p.CacheWritePerMTok},
			}
			for _, pr := range pairs {
				want, ok := row[pr.field].(float64)
				if _, stated := row[pr.field]; !stated && pr.field == "cache_creation_input_token_cost" {
					want, ok = 0, true // no cache-write price (OpenAI rows): the snapshot must say 0
				}
				if !ok || math.Abs(pr.got-want*1e6) > 1e-9 {
					t.Errorf("%s: snapshot %v per Mtok, datasheet %v per token", pr.field, pr.got, row[pr.field])
				}
			}
		})
	}
}

func TestPriceLookup(t *testing.T) {
	pt := testPrices(t)
	tests := []struct {
		model string
		ok    bool
		in    float64
	}{
		{"claude-x", true, 3},
		{"anthropic/claude-big", true, 15},
		{"claude-z", false, 0},
		{"", false, 0},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			p, ok := pt.Lookup(tt.model)
			if ok != tt.ok || p.InputPerMTok != tt.in {
				t.Errorf("Lookup(%q) = %+v, %v", tt.model, p, ok)
			}
		})
	}
	var nilTable *PriceTable
	if _, ok := nilTable.Lookup("claude-x"); ok {
		t.Errorf("nil table found a price")
	}
	if nilTable.Ref() != "" {
		t.Errorf("nil table ref = %q", nilTable.Ref())
	}
	if _, ok := nilTable.Max(); ok {
		t.Errorf("nil table has a max")
	}
	m, ok := pt.Max()
	if !ok || m.InputPerMTok != 15 || m.OutputPerMTok != 75 || m.CacheReadPerMTok != 1.5 || m.CacheWritePerMTok != 18.75 {
		t.Errorf("Max() = %+v, %v", m, ok)
	}
}

func TestModelPriceCost(t *testing.T) {
	p := ModelPrice{InputPerMTok: 3, OutputPerMTok: 15, CacheReadPerMTok: 0.3, CacheWritePerMTok: 3.75}
	tests := []struct {
		name string
		tok  schemas.Tokens
		want float64
	}{
		{"fresh input", schemas.Tokens{Fresh: 1_000_000}, 3},
		{"cache read", schemas.Tokens{CacheRead: 1_000_000}, 0.3},
		{"cache write", schemas.Tokens{CacheWrite: 1_000_000}, 3.75},
		{"output", schemas.Tokens{Out: 1_000_000}, 15},
		{"all buckets", schemas.Tokens{Fresh: 1000, CacheRead: 2000, CacheWrite: 100, Out: 500}, 0.011475},
		{"none", schemas.Tokens{}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := p.Cost(tt.tok); !near(got, tt.want) {
				t.Errorf("Cost(%+v) = %v, want %v", tt.tok, got, tt.want)
			}
		})
	}
}
