package steprules

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"sort"

	"github.com/maximhq/bifrost/bench/schemas"
)

// maxBytes is the most of steps.jsonl Load reads.
const maxBytes = 16 << 20

func missing(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, schemas.ErrNotFound)
}

// Load reads steps.jsonl through read (a RunFile-like func), keeping at most
// maxRows rows (0 means 20000). It never reads more than 16 MiB. A missing
// file falls back to the steps of evidence.json (the same fallback the run
// step timeline uses); when neither holds steps Load answers nil, false, nil.
// Rows come back in Seq order.
func Load(read func(name string) ([]byte, error), maxRows int) (steps []schemas.StepRecord, truncated bool, err error) {
	if maxRows <= 0 {
		maxRows = MaxRows
	}
	data, err := read(schemas.StepsFile)
	if err != nil {
		if missing(err) {
			return loadEvidence(read, maxRows)
		}
		return nil, false, err
	}
	if len(data) > maxBytes {
		data = data[:maxBytes]
		if i := bytes.LastIndexByte(data, '\n'); i >= 0 {
			data = data[:i]
		}
		truncated = true
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64<<10), maxBytes)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		if len(steps) >= maxRows {
			truncated = true
			break
		}
		var s schemas.StepRecord
		if err := json.Unmarshal(line, &s); err != nil {
			return nil, false, err
		}
		steps = append(steps, s)
	}
	if err := sc.Err(); err != nil {
		return nil, false, err
	}
	sort.SliceStable(steps, func(i, j int) bool { return steps[i].Seq < steps[j].Seq })
	return steps, truncated, nil
}

// evidenceFile is the sealed run evidence; its "steps" field holds the step
// timeline of runs that have no steps.jsonl.
const evidenceFile = "evidence.json"

// loadEvidence reads the steps of evidence.json. A missing file, or one
// without steps, answers nil, false, nil.
func loadEvidence(read func(name string) ([]byte, error), maxRows int) ([]schemas.StepRecord, bool, error) {
	data, err := read(evidenceFile)
	if err != nil {
		if missing(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	var ev struct {
		Steps []schemas.StepRecord `json:"steps"`
	}
	if err := json.Unmarshal(data, &ev); err != nil {
		return nil, false, err
	}
	steps := ev.Steps
	sort.SliceStable(steps, func(i, j int) bool { return steps[i].Seq < steps[j].Seq })
	truncated := false
	if len(steps) > maxRows {
		steps, truncated = steps[:maxRows], true
	}
	return steps, truncated, nil
}

// LoadCost reads cost.json into a request id to dollars map; unknown values
// are left out (a request with any unpriced attempt is left out whole).
func LoadCost(read func(name string) ([]byte, error)) (map[string]float64, error) {
	data, err := read("cost.json")
	if err != nil {
		if missing(err) {
			return map[string]float64{}, nil
		}
		return nil, err
	}
	var rows []schemas.CostRow
	if err := json.Unmarshal(data, &rows); err != nil {
		return nil, err
	}
	out := map[string]float64{}
	bad := map[string]bool{}
	for _, r := range rows {
		if r.RequestID == "" {
			continue
		}
		if r.USD.V == nil {
			bad[r.RequestID] = true
			continue
		}
		out[r.RequestID] += *r.USD.V
	}
	for id := range bad {
		delete(out, id)
	}
	return out, nil
}
