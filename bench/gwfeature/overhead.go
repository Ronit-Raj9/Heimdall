package gwfeature

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Feature 10: Bifrost's own time per model call, split by plugin (W4 design
// 3, section 13). The comparison answers every model call from the fault
// proxy's scripted model, so the gateway's work is real, the provider's is
// not, and the test costs nothing.

func init() {
	Register(Feature{ID: schemas.GatewayFeatureOverhead, Name: "Gateway time per plugin", Draft: draftOverhead,
		Compute: func(_ context.Context, in Input) Effect { return ComputeOverhead(in.Runs, in.Test.Options) }})
}

// Overhead test limits.
const (
	overheadMinRows      = 50 // rows with a breakdown needed for a report
	overheadDefaultTasks = 10
)

func draftOverhead(ctx context.Context, env DraftEnv, req DraftRequest) (*schemas.Experiment, []string, error) {
	model := strings.TrimSpace(req.Model)
	if model == "" && env.Root != "" {
		if base, err := p4ReadBase(env.Root, req.Base); err == nil {
			model = strings.TrimSpace(base.Model)
		}
	}
	if model == "" {
		return nil, nil, errors.New("pick the model this test runs on")
	}
	req.Trials = 1
	if len(req.Tasks) > overheadDefaultTasks {
		req.Tasks = req.Tasks[:overheadDefaultTasks]
	}
	exp := p4Base(env, req)
	exp.ScriptedUpstream = true
	set := map[string]any{"model": model}
	if req.Provider != "" {
		set["gateway.provider"] = req.Provider
	}
	exp.Arms["A"] = schemas.ArmSpec{Label: "the agent as it is", Set: set}
	opts := map[string]string{}
	for k, v := range req.Options {
		opts[k] = v
	}
	opts["gateway_version"] = "unknown"
	if env.GatewayGet != nil {
		var version string
		if err := env.GatewayGet(ctx, "/api/version", &version); err == nil && strings.TrimSpace(version) != "" {
			opts["gateway_version"] = strings.TrimSpace(version)
		}
		var loaded struct {
			Plugins []string `json:"plugins"`
		}
		if err := env.GatewayGet(ctx, "/api/plugins/loaded", &loaded); err == nil {
			sort.Strings(loaded.Plugins)
			opts["plugins"] = strings.Join(loaded.Plugins, ",")
		}
	}
	p4Test(exp, schemas.GatewayFeatureOverhead, 1, opts)
	return exp, []string{"This test answers model calls from a stand-in model, so it costs nothing and measures only Bifrost's own time."}, nil
}

// overheadBucket is one part of Bifrost's time over the rows that have it.
type overheadBucket struct {
	Name     string  `json:"name"`
	Rows     int     `json:"rows"`
	MedianUS float64 `json:"median_us"`
	P95US    float64 `json:"p95_us"`
	Share    float64 `json:"share"` // of all overhead time
}

// overheadDetails is Effect.Details of the overhead feature.
type overheadDetails struct {
	Rows        int              `json:"rows"`         // message rows with full detail
	BucketRows  int              `json:"bucket_rows"`  // of those, rows with a breakdown
	UnknownRows int              `json:"unknown_rows"` // message rows without full detail
	MedianMS    *float64         `json:"median_ms,omitempty"`
	P95MS       *float64         `json:"p95_ms,omitempty"`
	Buckets     []overheadBucket `json:"buckets"`
	NoTime      []string         `json:"no_time"` // loaded plugins with no time recorded on any row
	Gateway     string           `json:"gateway,omitempty"`
}

// ComputeOverhead reports Bifrost's own time per model call over the rows
// whose full Bifrost detail was sealed. It reads no file and saves nothing,
// so it serves any comparison (GET /experiments/{id}/gateway-overhead).
func ComputeOverhead(runs []Run, options map[string]string) Effect {
	e := Effect{Arms: []ArmResult{}, Source: Source{Gateway: options["gateway_version"]}}
	det := overheadDetails{Buckets: []overheadBucket{}, NoTime: []string{}, Gateway: options["gateway_version"]}
	var totals []float64
	sumUS := 0.0
	byName := map[string][]float64{}
	for _, r := range runs {
		for _, row := range r.Rows {
			if row.Kind != "message" {
				continue
			}
			if !row.RawKnown {
				det.UnknownRows++
				continue
			}
			det.Rows++
			if row.OverheadMS != nil {
				totals = append(totals, *row.OverheadMS)
				sumUS += *row.OverheadMS * 1000
			}
			if len(row.Overhead) > 0 {
				det.BucketRows++
			}
			for _, b := range row.Overhead {
				byName[b.Name] = append(byName[b.Name], b.US)
			}
		}
	}
	det.MedianMS, det.P95MS = p4Quantile(append([]float64(nil), totals...), 0.5), p4Quantile(totals, 0.95)
	for name, v := range byName {
		b := overheadBucket{Name: name, Rows: len(v)}
		sum := 0.0
		for _, x := range v {
			sum += x
		}
		b.MedianUS, b.P95US = *p4Quantile(append([]float64(nil), v...), 0.5), *p4Quantile(v, 0.95)
		if sumUS > 0 {
			b.Share = sum / sumUS
		}
		det.Buckets = append(det.Buckets, b)
	}
	sort.Slice(det.Buckets, func(i, j int) bool {
		if det.Buckets[i].Share != det.Buckets[j].Share {
			return det.Buckets[i].Share > det.Buckets[j].Share
		}
		return det.Buckets[i].Name < det.Buckets[j].Name
	})
	for _, p := range strings.Split(options["plugins"], ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if _, ok := byName["plugin."+p]; !ok {
			if _, ok := byName[p]; !ok {
				det.NoTime = append(det.NoTime, p)
			}
		}
	}
	e.Details, _ = json.Marshal(det)
	e.Setting = &BifrostSetting{Kind: "none", Title: "No setting to copy",
		Note: "To remove a plugin's time, turn the plugin off in Bifrost's Plugins page if you do not use it."}
	if det.BucketRows < overheadMinRows {
		e.State = StateNotEnoughData
		e.Reason = fmt.Sprintf("%d of the %d model calls needed have Bifrost's time breakdown", det.BucketRows, overheadMinRows)
		e.Headline = "Not enough data yet: " + e.Reason + "."
		return e
	}
	e.State = StateMeasured
	var s strings.Builder
	if det.MedianMS != nil && det.P95MS != nil {
		fmt.Fprintf(&s, "Bifrost adds %.1f ms per call (median; %.1f ms at the 95th percentile).", *det.MedianMS, *det.P95MS)
	}
	var parts []string
	for i, b := range det.Buckets {
		if i == 2 {
			break
		}
		parts = append(parts, fmt.Sprintf("%s %.1f ms", strings.TrimPrefix(b.Name, "plugin."), b.MedianUS/1000))
	}
	if len(parts) > 0 {
		s.WriteString(" Largest parts: " + strings.Join(parts, ", ") + ".")
	}
	e.Headline = strings.TrimSpace(s.String())
	e.Result = "worked"
	return e
}
