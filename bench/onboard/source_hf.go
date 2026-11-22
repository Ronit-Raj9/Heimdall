package onboard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/maximhq/bifrost/bench/scaffold"
)

// hfSource reads a Hugging Face dataset through the dataset viewer API
// (splits, first rows, rows pages of 100): question-and-answer rows become
// row tasks; SWE-bench style rows go to the swebench importer.
type hfSource struct{}

func init() { Register(hfSource{}) }

func (hfSource) Name() string { return "huggingface" }
func (hfSource) Kind() Kind   { return KindHF }

var hfPathRE = regexp.MustCompile(`^/datasets/([A-Za-z0-9][A-Za-z0-9._-]{0,95})/([A-Za-z0-9][A-Za-z0-9._-]{0,95})(?:/|$)`)

// HFDataset returns "org/name" of a Hugging Face dataset URL.
func HFDataset(u *url.URL) (string, bool) {
	if u == nil {
		return "", false
	}
	switch strings.ToLower(u.Hostname()) {
	case "huggingface.co", "www.huggingface.co", "hf.co":
	default:
		return "", false
	}
	m := hfPathRE.FindStringSubmatch(u.Path)
	if m == nil {
		return "", false
	}
	return m[1] + "/" + m[2], true
}

func (hfSource) Detect(p Peek) (Match, bool) {
	if ds, ok := HFDataset(p.URL); ok {
		return Match{Label: "Hugging Face dataset " + ds, Score: 100, CanDraft: true}, true
	}
	return Match{}, false
}

// hfGet calls the dataset viewer or the hub API and decodes JSON.
func hfGet(ctx context.Context, env *Env, token, raw string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "heimdall-onboard")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := env.client().Do(req)
	if err != nil {
		return fmt.Errorf("Hugging Face: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxDataBytes+1))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &e)
		msg := strings.TrimSpace(e.Error)
		if msg == "" {
			msg = http.StatusText(resp.StatusCode)
		}
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden || strings.Contains(strings.ToLower(msg), "gated") {
			return fmt.Errorf("Hugging Face refused (%d: %s): a gated or private dataset needs a Hugging Face token saved under Settings > Secrets, picked as the connection", resp.StatusCode, msg)
		}
		return fmt.Errorf("Hugging Face answered %d: %s", resp.StatusCode, msg)
	}
	return json.Unmarshal(data, out)
}

type hfSplit struct {
	Config string `json:"config"`
	Split  string `json:"split"`
}

type hfRows struct {
	Features []struct {
		Name string `json:"name"`
	} `json:"features"`
	Rows []struct {
		RowIdx int            `json:"row_idx"`
		Row    map[string]any `json:"row"`
	} `json:"rows"`
	NumRowsTotal int `json:"num_rows_total"`
}

// hfInfo is what Inspect and Draft learn about a dataset.
type hfInfo struct {
	dataset, config, split string
	splits                 []hfSplit
	license                string
	gated                  bool
	first                  hfRows
	total                  int
}

func (hfSource) info(ctx context.Context, env *Env, in Input, plan Plan) (*hfInfo, string, error) {
	u, err := url.Parse(strings.TrimSpace(in.URL))
	if err != nil {
		return nil, "", err
	}
	ds, ok := HFDataset(u)
	if !ok {
		return nil, "", errors.New("not a Hugging Face dataset link: want https://huggingface.co/datasets/<org>/<name>")
	}
	token, err := env.token(ctx, in)
	if err != nil {
		return nil, "", err
	}
	info := &hfInfo{dataset: ds}
	var splits struct {
		Splits []hfSplit `json:"splits"`
	}
	q := url.Values{"dataset": {ds}}
	if err := hfGet(ctx, env, token, env.hfAPI()+"/splits?"+q.Encode(), &splits); err != nil {
		return nil, "", err
	}
	if len(splits.Splits) == 0 {
		return nil, "", fmt.Errorf("dataset %s has no splits the viewer can read", ds)
	}
	info.splits = splits.Splits
	pick := splits.Splits[0]
	want := plan.Split
	if want == "" {
		// /viewer/<config>/<split> in the link names one.
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) >= 5 && parts[3] == "viewer" {
			want = parts[4]
			if len(parts) >= 6 {
				want = parts[4] + "/" + parts[5]
			}
		}
	}
	for _, s := range splits.Splits {
		if want == s.Split || want == s.Config+"/"+s.Split {
			pick = s
			break
		}
		if want == "" && (s.Split == "test" || s.Split == "validation") && pick.Split == "train" {
			pick = s
		}
	}
	info.config, info.split = pick.Config, pick.Split
	q = url.Values{"dataset": {ds}, "config": {pick.Config}, "split": {pick.Split}}
	if err := hfGet(ctx, env, token, env.hfAPI()+"/first-rows?"+q.Encode(), &info.first); err != nil {
		return nil, "", err
	}
	info.total = len(info.first.Rows)
	var size struct {
		Size struct {
			Splits []struct {
				Config  string `json:"config"`
				Split   string `json:"split"`
				NumRows int    `json:"num_rows"`
			} `json:"splits"`
		} `json:"size"`
	}
	if hfGet(ctx, env, token, env.hfAPI()+"/size?"+url.Values{"dataset": {ds}}.Encode(), &size) == nil {
		for _, s := range size.Size.Splits {
			if s.Config == pick.Config && s.Split == pick.Split && s.NumRows > 0 {
				info.total = s.NumRows
			}
		}
	}
	var card struct {
		CardData struct {
			License any `json:"license"`
		} `json:"cardData"`
		Gated any      `json:"gated"`
		Tags  []string `json:"tags"`
	}
	if hfGet(ctx, env, token, env.hfHub()+"/api/datasets/"+ds, &card) == nil {
		switch l := card.CardData.License.(type) {
		case string:
			info.license = l
		case []any:
			var ls []string
			for _, x := range l {
				if s, ok := x.(string); ok {
					ls = append(ls, s)
				}
			}
			info.license = strings.Join(ls, ", ")
		}
		if info.license == "" {
			for _, t := range card.Tags {
				if strings.HasPrefix(t, "license:") {
					info.license = strings.TrimPrefix(t, "license:")
				}
			}
		}
		if g, ok := card.Gated.(bool); ok {
			info.gated = g
		} else if s, ok := card.Gated.(string); ok && s != "" && s != "false" {
			info.gated = true
		}
	}
	return info, token, nil
}

// sweRows reports whether rows look like SWE-bench instances.
func sweRows(r hfRows) bool {
	for _, f := range r.Features {
		if strings.EqualFold(f.Name, "FAIL_TO_PASS") {
			return true
		}
	}
	return false
}

func rowsJSONL(r hfRows) []byte {
	var b bytes.Buffer
	for _, row := range r.Rows {
		line, err := json.Marshal(row.Row)
		if err == nil {
			b.Write(line)
			b.WriteByte('\n')
		}
	}
	return b.Bytes()
}

func (s hfSource) Inspect(ctx context.Context, env *Env, in Input) (*Inventory, error) {
	info, _, err := s.info(ctx, env, in, Plan{})
	if err != nil {
		return nil, err
	}
	var inv *Inventory
	if sweRows(info.first) {
		inv = &Inventory{Kind: KindHF, Unit: "instance", CanDraft: true, Importers: []string{"swebench"}}
		for i, r := range info.first.Rows {
			if i >= 5 {
				break
			}
			inv.Samples = append(inv.Samples, Sample{Ref: fmt.Sprintf("row %d", r.RowIdx), Fields: map[string]string{
				"instance_id": fmt.Sprint(r.Row["instance_id"]), "problem_statement": fmt.Sprint(r.Row["problem_statement"])}})
		}
		inv.Suggest = Plan{Importer: "swebench", Limit: DefaultImportLimit}
		inv.Estimate = Estimate{Tasks: DefaultImportLimit, ReadinessRuns: DefaultImportLimit * 6}
		inv.Needs = append(inv.Needs, "the repository image of each instance (built at readiness)")
		inv.Next = "SWE-bench style rows: the swebench importer writes code tasks with their hidden tests."
	} else {
		if inv, err = inspectRows("rows.jsonl", rowsJSONL(info.first), Plan{}, true); err != nil {
			return nil, err
		}
		inv.Kind = KindHF
	}
	for _, f := range info.first.Features {
		inv.Columns = appendUnique(inv.Columns, strings.ToLower(f.Name))
	}
	inv.Title = info.dataset
	inv.Units = info.total
	inv.License = info.license
	if inv.License == "" {
		inv.License = "not stated on the dataset card: check it before you publish"
		inv.Warnings = append(inv.Warnings, "the dataset card names no licence")
	}
	if info.gated {
		inv.Warnings = append(inv.Warnings, "gated dataset: reading it needs a Hugging Face token (a saved connection)")
	}
	inv.Suggest.Suite = slug(strings.ReplaceAll(info.dataset, "/", "-"))
	inv.Suggest.Split = info.config + "/" + info.split
	var names []string
	for _, sp := range info.splits {
		names = append(names, sp.Config+"/"+sp.Split)
	}
	if len(names) > 1 {
		inv.Warnings = append(inv.Warnings, "splits: "+strings.Join(names, ", ")+" (reading "+inv.Suggest.Split+")")
	}
	if inv.Suggest.Limit == 0 {
		inv.Suggest.Limit = DefaultImportLimit
	}
	if inv.Estimate.Tasks > inv.Suggest.Limit {
		inv.Estimate.ReadinessRuns = inv.Estimate.ReadinessRuns / inv.Estimate.Tasks * inv.Suggest.Limit
		if inv.Estimate.JudgeCalls > 0 {
			inv.Estimate.JudgeCalls = inv.Estimate.ReadinessRuns
		}
		inv.Estimate.Tasks = inv.Suggest.Limit
	}
	return inv, nil
}

func appendUnique(list []string, v string) []string {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}

// hfMaxRows caps the rows one draft pages through.
const hfMaxRows = scaffold.MaxImportRows

func (s hfSource) Draft(ctx context.Context, env *Env, in Input, plan Plan, set *DraftSet) error {
	info, token, err := s.info(ctx, env, in, plan)
	if err != nil {
		return err
	}
	limit := plan.Limit
	if limit <= 0 {
		limit = DefaultImportLimit
	}
	if limit > hfMaxRows {
		limit = hfMaxRows
	}
	var all hfRows
	all.Features = info.first.Features
	for off := 0; off < limit; off += 100 {
		n := 100
		if limit-off < n {
			n = limit - off
		}
		q := url.Values{"dataset": {info.dataset}, "config": {info.config}, "split": {info.split},
			"offset": {fmt.Sprint(off)}, "length": {fmt.Sprint(n)}}
		var page hfRows
		if err := hfGet(ctx, env, token, env.hfAPI()+"/rows?"+q.Encode(), &page); err != nil {
			return err
		}
		all.Rows = append(all.Rows, page.Rows...)
		if len(page.Rows) < n {
			break
		}
	}
	if len(all.Rows) == 0 {
		return fmt.Errorf("dataset %s split %s/%s returned no rows", info.dataset, info.config, info.split)
	}
	set.Public, set.License = true, info.license
	label := "huggingface.co/datasets/" + info.dataset + " (" + info.config + "/" + info.split + ")"
	data := rowsJSONL(all)
	if plan.Importer != "" || sweRows(all) {
		name := plan.Importer
		if name == "" {
			name = "swebench"
		}
		dir := filepath.Join(set.Dir(), "source")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		f := filepath.Join(dir, "rows.jsonl")
		if err := os.WriteFile(f, data, 0o644); err != nil {
			return err
		}
		return draftImporter(ctx, set, name, f, plan)
	}
	plan.Format = scaffold.RowFormatJSONL
	return draftRows(env, set, plan, "rows.jsonl", data, label)
}
