package gwfeature

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"

	"github.com/maximhq/bifrost/bench/leaderboard"
	"github.com/maximhq/bifrost/bench/schemas"
)

// Run folder files the row reader reads.
const (
	fileEvidence   = "evidence.json"
	fileGatewayRaw = "gateway.raw.jsonl"
)

// rawDetail is the part of a sealed Bifrost detail row a feature reads.
type rawDetail struct {
	ServedModel         *string  `json:"served_model"`
	ComplexityTier      *string  `json:"complexity_tier"`
	ComplexityMechanism *string  `json:"complexity_mechanism"`
	ComplexityScore     *float64 `json:"complexity_score"`
	PromptName          *string  `json:"selected_prompt_name"`
	PromptVersion       *string  `json:"selected_prompt_version"`
	CacheDebug          *struct {
		Similarity *float64 `json:"similarity"`
		Threshold  *float64 `json:"threshold"`
	} `json:"cache_debug"`
	Overhead []struct {
		Name       string  `json:"name"`
		DurationUs float64 `json:"duration_us"`
	} `json:"overhead_breakdown"`
	RawResponse string `json:"raw_response"`
}

// responseModel is the model a raw response body names, "" when none.
func (d rawDetail) responseModel() string {
	var body struct {
		Model string `json:"model"`
	}
	if d.RawResponse == "" || json.Unmarshal([]byte(d.RawResponse), &body) != nil {
		return ""
	}
	return body.Model
}

func notFound(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, schemas.ErrNotFound)
}

// ReadRows reads one run's gateway rows: the summary rows of evidence.json
// merged with the sealed raw detail of gateway.raw.jsonl. complete is the
// request log's own completeness; raw counts rows whose detail was sealed.
func ReadRows(src leaderboard.Source, runID string) (rows []Row, complete bool, raw int, err error) {
	data, err := src.RunFile(runID, fileEvidence)
	if err != nil {
		return nil, false, 0, err
	}
	var ev struct {
		Requests *schemas.RequestLog `json:"requests"`
	}
	if err := json.Unmarshal(data, &ev); err != nil {
		return nil, false, 0, fmt.Errorf("evidence.json of run %s: %w", runID, err)
	}
	if ev.Requests == nil {
		return nil, false, 0, nil
	}
	complete = ev.Requests.Complete
	byID := map[string]int{}
	for _, g := range ev.Requests.Requests {
		r := Row{ID: g.ID, Kind: g.Kind, Status: g.Status, Provider: g.Provider, ModelAsked: g.ModelRequested,
			FallbackIndex: g.FallbackIndex, LatencyMS: g.LatencyMS, OverheadMS: g.OverheadMS, CostUSD: g.BifrostUSD,
			ErrorStatus: g.ErrorStatus, CacheHit: g.CacheHit, CacheID: g.CacheID, BudgetRefused: g.BudgetRefused}
		if g.ModelServed != g.ModelRequested {
			r.ModelServed = g.ModelServed
		}
		byID[g.ID] = len(rows)
		rows = append(rows, r)
	}
	data, err = src.RunFile(runID, fileGatewayRaw)
	if err != nil {
		if notFound(err) {
			return rows, complete, 0, nil
		}
		return rows, complete, 0, err
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64<<10), 64<<20)
	for sc.Scan() {
		var line struct {
			RequestID    string          `json:"request_id"`
			RawAvailable bool            `json:"raw_available"`
			RawDetail    json.RawMessage `json:"raw_detail"`
		}
		if json.Unmarshal(sc.Bytes(), &line) != nil || !line.RawAvailable {
			continue
		}
		i, ok := byID[line.RequestID]
		if !ok || rows[i].RawKnown {
			continue
		}
		var d rawDetail
		if json.Unmarshal(line.RawDetail, &d) != nil {
			continue
		}
		fillRaw(&rows[i], d)
		raw++
	}
	return rows, complete, raw, nil
}

// fillRaw fills the raw fields of a row. The served model comes from
// served_model, else the model the raw response names; a summary model equal
// to the requested one stays unknown without either.
func fillRaw(r *Row, d rawDetail) {
	r.RawKnown = true
	str := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	switch {
	case str(d.ServedModel) != "":
		r.ModelServed = *d.ServedModel
	case d.responseModel() != "":
		r.ModelServed = d.responseModel()
	}
	r.ComplexityTier = str(d.ComplexityTier)
	r.ComplexityMechanism = str(d.ComplexityMechanism)
	r.ComplexityScore = d.ComplexityScore
	r.PromptName = str(d.PromptName)
	r.PromptVersion = str(d.PromptVersion)
	if d.CacheDebug != nil {
		r.CacheSimilarity, r.CacheThreshold = d.CacheDebug.Similarity, d.CacheDebug.Threshold
	}
	for _, b := range d.Overhead {
		r.Overhead = append(r.Overhead, Bucket{Name: b.Name, US: b.DurationUs})
	}
}
