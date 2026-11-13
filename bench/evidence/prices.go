package evidence

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// PriceTable is the pinned price snapshot every cost row is priced from
// (deploy/pricing-snapshot.json). It is loaded once and read-only afterwards.
type PriceTable struct {
	ID         string                `json:"id"`
	Source     string                `json:"source"`      // where the prices were copied from
	CapturedAt string                `json:"captured_at"` // when they were copied
	Models     map[string]ModelPrice `json:"models"`
	SHA256     string                `json:"-"` // sha256 of the file as loaded
}

// ModelPrice is one model's price per million tokens for each token bucket.
type ModelPrice struct {
	InputPerMTok      float64 `json:"input_per_mtok"`
	OutputPerMTok     float64 `json:"output_per_mtok"`
	CacheReadPerMTok  float64 `json:"cache_read_per_mtok"`
	CacheWritePerMTok float64 `json:"cache_write_per_mtok"`
}

// LoadPrices reads a price snapshot and records the sha256 of its bytes, so a
// cost row names exactly the file it was priced from. Every model must state
// all four prices: a missing price would otherwise read as free.
func LoadPrices(path string) (*PriceTable, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read price snapshot %s: %w", path, err)
	}
	var file struct {
		ID         string                         `json:"id"`
		Source     string                         `json:"source"`
		CapturedAt string                         `json:"captured_at"`
		Models     map[string]map[string]*float64 `json:"models"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("parse price snapshot %s: %w", path, err)
	}
	if strings.TrimSpace(file.ID) == "" {
		return nil, fmt.Errorf("price snapshot %s: %w", path, errNoSnapshotID)
	}
	t := &PriceTable{
		ID:         file.ID,
		Source:     file.Source,
		CapturedAt: file.CapturedAt,
		Models:     make(map[string]ModelPrice, len(file.Models)),
		SHA256:     utils.SHA256Bytes(raw),
	}
	for model, fields := range file.Models {
		p, err := modelPrice(fields)
		if err != nil {
			return nil, fmt.Errorf("price snapshot %s: model %s: %w", path, model, err)
		}
		t.Models[model] = p
	}
	return t, nil
}

var errNoSnapshotID = errors.New("missing id")

// Ref is the price reference a cost row carries: "<id>@sha256:<file hash>".
func (t *PriceTable) Ref() string {
	if t == nil {
		return ""
	}
	return t.ID + "@sha256:" + t.SHA256
}

// Lookup returns the price of model. A provider prefix ("anthropic/...") is
// tried without the prefix too, since the gateway may report either form.
func (t *PriceTable) Lookup(model string) (ModelPrice, bool) {
	if t == nil || model == "" {
		return ModelPrice{}, false
	}
	if p, ok := t.Models[model]; ok {
		return p, true
	}
	if i := strings.LastIndex(model, "/"); i >= 0 {
		p, ok := t.Models[model[i+1:]]
		return p, ok
	}
	return ModelPrice{}, false
}

// Max returns the highest price of each bucket over every model in the table,
// and false when the table is empty. It bounds the cost of a model the table
// does not list.
func (t *PriceTable) Max() (ModelPrice, bool) {
	if t == nil || len(t.Models) == 0 {
		return ModelPrice{}, false
	}
	var m ModelPrice
	for _, p := range t.Models {
		m.InputPerMTok = max(m.InputPerMTok, p.InputPerMTok)
		m.OutputPerMTok = max(m.OutputPerMTok, p.OutputPerMTok)
		m.CacheReadPerMTok = max(m.CacheReadPerMTok, p.CacheReadPerMTok)
		m.CacheWritePerMTok = max(m.CacheWritePerMTok, p.CacheWritePerMTok)
	}
	return m, true
}

// Cost returns the USD price of tok: each bucket times its per-token price.
func (p ModelPrice) Cost(tok schemas.Tokens) float64 {
	return (float64(tok.Fresh)*p.InputPerMTok +
		float64(tok.CacheRead)*p.CacheReadPerMTok +
		float64(tok.CacheWrite)*p.CacheWritePerMTok +
		float64(tok.Out)*p.OutputPerMTok) / 1e6
}

// priceFields are the four prices every model must state.
var priceFields = []string{"input_per_mtok", "output_per_mtok", "cache_read_per_mtok", "cache_write_per_mtok"}

// modelPrice checks one model's fields and builds its price.
func modelPrice(fields map[string]*float64) (ModelPrice, error) {
	for _, f := range priceFields {
		v, ok := fields[f]
		if !ok || v == nil {
			return ModelPrice{}, fmt.Errorf("missing %s", f)
		}
		if *v < 0 {
			return ModelPrice{}, fmt.Errorf("negative %s", f)
		}
	}
	for f := range fields {
		if !slices.Contains(priceFields, f) {
			return ModelPrice{}, fmt.Errorf("unknown field %s", f)
		}
	}
	return ModelPrice{
		InputPerMTok:      *fields["input_per_mtok"],
		OutputPerMTok:     *fields["output_per_mtok"],
		CacheReadPerMTok:  *fields["cache_read_per_mtok"],
		CacheWritePerMTok: *fields["cache_write_per_mtok"],
	}, nil
}
