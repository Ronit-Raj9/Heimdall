// Package hfsource reads a Hugging Face dataset split through the dataset
// viewer API (datasets-server.huggingface.co: /splits, /info and /rows, 100
// rows a page, a bearer token for gated sets), writes it as JSON Lines and
// says which existing importer reads it, from the column names:
//
//   - FAIL_TO_PASS and PASS_TO_PASS (with instance_id): swebench
//   - question_id and haystack_sessions: longmemeval
//   - task_id, Question and "Final answer": gaia
//   - a question-like and an answer-like column: rows (task import-rows)
//
// Anything else has no route; the JSONL is still written for the mapper.
package hfsource

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// BaseURL is the dataset viewer API.
const BaseURL = "https://datasets-server.huggingface.co"

// PageRows is the most rows /rows returns per call.
const PageRows = 100

// Client calls the dataset viewer API.
type Client struct {
	BaseURL string // default BaseURL
	Token   string // bearer token for gated datasets (HF_TOKEN)
	HTTP    *http.Client
	// Pause between pages; the API rate-limits bursts. Default 200ms.
	Pause time.Duration
}

// Split is one config and split of a dataset.
type Split struct {
	Dataset string `json:"dataset"`
	Config  string `json:"config"`
	Split   string `json:"split"`
}

// Info is what /info and /size say about a config.
type Info struct {
	License     string   `json:"license,omitempty"`
	Description string   `json:"description,omitempty"`
	Columns     []string `json:"columns"`
	Rows        int      `json:"rows"`
}

// Route is the importer that reads the dataset's JSONL.
type Route struct {
	Importer string `json:"importer"` // swebench, longmemeval, gaia, rows, or "" when none fits
	Reason   string `json:"reason"`
}

func (c *Client) base() string {
	if c.BaseURL != "" {
		return strings.TrimRight(c.BaseURL, "/")
	}
	return BaseURL
}

func (c *Client) get(ctx context.Context, path string, q url.Values, out any) error {
	u := c.base() + path + "?" + q.Encode()
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return err
		}
		if c.Token != "" {
			req.Header.Set("Authorization", "Bearer "+c.Token)
		}
		hc := c.HTTP
		if hc == nil {
			hc = &http.Client{Timeout: 2 * time.Minute}
		}
		resp, err := hc.Do(req)
		if err != nil {
			return err
		}
		b, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
		resp.Body.Close()
		if err != nil {
			return err
		}
		if (resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500) && attempt < 4 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt+1) * 2 * time.Second):
			}
			continue
		}
		if resp.StatusCode != http.StatusOK {
			var e struct {
				Error string `json:"error"`
			}
			_ = json.Unmarshal(b, &e)
			if e.Error == "" {
				e.Error = resp.Status
			}
			if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
				e.Error += " (a gated dataset: accept its terms on the Hub and set HF_TOKEN)"
			}
			return fmt.Errorf("hugging face %s: %s", path, e.Error)
		}
		return json.Unmarshal(b, out)
	}
}

// ParseDataset accepts "org/name" or a https://huggingface.co/datasets/org/name URL.
func ParseDataset(s string) (string, error) {
	s = strings.TrimSpace(s)
	if u, err := url.Parse(s); err == nil && u.Host != "" {
		if u.Host != "huggingface.co" && u.Host != "www.huggingface.co" {
			return "", fmt.Errorf("%s is not a Hugging Face URL", s)
		}
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) < 3 || parts[0] != "datasets" {
			return "", fmt.Errorf("%s: want https://huggingface.co/datasets/<org>/<name>", s)
		}
		s = parts[1] + "/" + parts[2]
	}
	parts := strings.Split(s, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" || strings.ContainsAny(s, " ?#") {
		return "", fmt.Errorf("dataset %q: want <org>/<name>", s)
	}
	return s, nil
}

// Splits lists the dataset's configs and splits.
func (c *Client) Splits(ctx context.Context, dataset string) ([]Split, error) {
	var out struct {
		Splits []Split `json:"splits"`
	}
	if err := c.get(ctx, "/splits", url.Values{"dataset": {dataset}}, &out); err != nil {
		return nil, err
	}
	return out.Splits, nil
}

// Info reads licence, columns and the row count of split.
func (c *Client) Info(ctx context.Context, s Split) (*Info, error) {
	var out struct {
		DatasetInfo struct {
			License     string         `json:"license"`
			Description string         `json:"description"`
			Features    map[string]any `json:"features"`
			Splits      map[string]struct {
				NumExamples int `json:"num_examples"`
			} `json:"splits"`
		} `json:"dataset_info"`
	}
	if err := c.get(ctx, "/info", url.Values{"dataset": {s.Dataset}, "config": {s.Config}}, &out); err != nil {
		return nil, err
	}
	info := &Info{License: out.DatasetInfo.License, Description: out.DatasetInfo.Description, Rows: out.DatasetInfo.Splits[s.Split].NumExamples}
	for k := range out.DatasetInfo.Features {
		info.Columns = append(info.Columns, k)
	}
	sort.Strings(info.Columns)
	return info, nil
}

// rowsPage is one /rows response.
type rowsPage struct {
	Features []struct {
		Name string `json:"name"`
	} `json:"features"`
	Rows []struct {
		RowIdx int             `json:"row_idx"`
		Row    json.RawMessage `json:"row"`
	} `json:"rows"`
	NumRowsTotal int `json:"num_rows_total"`
}

// FetchResult says what Fetch wrote.
type FetchResult struct {
	Rows    int      `json:"rows"`
	Total   int      `json:"total"`
	Columns []string `json:"columns"`
	Route   Route    `json:"route"`
}

// Fetch writes up to max rows (0 means all) of split to w, one JSON object a
// line, paging /rows.
func (c *Client) Fetch(ctx context.Context, s Split, max int, w io.Writer) (*FetchResult, error) {
	bw := bufio.NewWriter(w)
	res := &FetchResult{}
	pause := c.Pause
	if pause == 0 {
		pause = 200 * time.Millisecond
	}
	for offset := 0; ; {
		n := PageRows
		if max > 0 && max-res.Rows < n {
			n = max - res.Rows
		}
		if n <= 0 {
			break
		}
		var page rowsPage
		q := url.Values{"dataset": {s.Dataset}, "config": {s.Config}, "split": {s.Split},
			"offset": {fmt.Sprint(offset)}, "length": {fmt.Sprint(n)}}
		if err := c.get(ctx, "/rows", q, &page); err != nil {
			return nil, err
		}
		if res.Columns == nil {
			for _, f := range page.Features {
				res.Columns = append(res.Columns, f.Name)
			}
		}
		res.Total = page.NumRowsTotal
		for _, r := range page.Rows {
			if _, err := bw.Write(append(compact(r.Row), '\n')); err != nil {
				return nil, err
			}
			res.Rows++
		}
		offset += len(page.Rows)
		if len(page.Rows) == 0 || offset >= page.NumRowsTotal {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(pause):
		}
	}
	if err := bw.Flush(); err != nil {
		return nil, err
	}
	res.Route = Sniff(res.Columns)
	return res, nil
}

func compact(raw json.RawMessage) []byte {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return raw
	}
	_ = enc.Encode(v)
	return []byte(strings.TrimRight(b.String(), "\n"))
}

// question-like and answer-like column names (lower case), as the rows
// mapper (scaffold.ParseRows) reads them.
var (
	questionCols = []string{"question", "instruction", "input", "prompt", "query", "task", "problem"}
	answerCols   = []string{"answer", "expected", "expected_answer", "expected_output", "output", "reference_answer", "ground_truth", "solution", "rubric"}
)

// Sniff picks the importer for a set of column names.
func Sniff(columns []string) Route {
	has := map[string]bool{}
	lower := map[string]bool{}
	for _, c := range columns {
		has[c] = true
		lower[strings.ToLower(c)] = true
	}
	switch {
	case has["FAIL_TO_PASS"] && has["PASS_TO_PASS"] && has["instance_id"]:
		return Route{Importer: "swebench", Reason: "SWE-bench columns (instance_id, FAIL_TO_PASS, PASS_TO_PASS)"}
	case has["question_id"] && has["haystack_sessions"]:
		return Route{Importer: "longmemeval", Reason: "LongMemEval columns (question_id, haystack_sessions)"}
	case has["task_id"] && has["Question"] && has["Final answer"]:
		return Route{Importer: "gaia", Reason: "GAIA columns (task_id, Question, Final answer)"}
	}
	q, a := pick(lower, questionCols), pick(lower, answerCols)
	if q != "" && a != "" {
		return Route{Importer: "rows", Reason: fmt.Sprintf("question and answer columns (%s, %s): bifrost-bench task import-rows into a qa suite", q, a)}
	}
	return Route{Reason: "no known schema: map the columns by hand (columns: " + strings.Join(columns, ", ") + ")"}
}

func pick(have map[string]bool, names []string) string {
	for _, n := range names {
		if have[n] {
			return n
		}
	}
	return ""
}

// WriteSource writes SOURCE.md beside the JSONL: dataset, config, split,
// licence, row count and date.
func WriteSource(dir string, s Split, info *Info, res *FetchResult, now time.Time) error {
	lic := "not stated on the dataset card"
	if info != nil && info.License != "" {
		lic = info.License
	}
	body := fmt.Sprintf("# %s\n\n- URL: https://huggingface.co/datasets/%s\n- Config: %s\n- Split: %s\n- Licence: %s\n- Rows: %d of %d\n- Fetched: %s (dataset viewer /rows API)\n- Route: %s (%s)\n\nPublic dataset: likely seen in model training; offer it as a public split, never held out.\n",
		s.Dataset, s.Dataset, s.Config, s.Split, lic, res.Rows, res.Total, now.UTC().Format(time.RFC3339), orNone(res.Route.Importer), res.Route.Reason)
	return os.WriteFile(filepath.Join(dir, "SOURCE.md"), []byte(body), 0o644)
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}
