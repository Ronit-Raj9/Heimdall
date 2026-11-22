// Package platformimport reads datasets and traces from the observability
// and eval platforms customers already use (lane Y15): LangSmith, Langfuse,
// Braintrust and Datadog LLM Observability. A dataset becomes benchmark rows
// (dataset.Row); a trace becomes a conversation for the failure inbox or a
// row whose reference answer is what the agent said.
//
// The endpoints, as each vendor documents them (read 22 Nov 2026):
//
//   - LangSmith (X-Api-Key): GET /api/v1/datasets?name=, GET
//     /api/v1/examples?dataset=&offset=&limit= (a list of {id, inputs,
//     outputs, metadata}); GET /api/v1/sessions?name= for a project, POST
//     /api/v2/runs/query {project_ids, is_root, page_size, selects, cursor}
//     answering {items, next_cursor}.
//   - Langfuse (Basic auth, public key and secret key): GET
//     /api/public/dataset-items?datasetName=&page=&limit= and GET
//     /api/public/traces?page=&limit=, both {data, meta: {totalPages}}.
//   - Braintrust (Bearer): GET /v1/dataset?dataset_name=&project_name= and
//     GET /v1/project?project_name= ({objects}), POST /v1/dataset/{id}/fetch
//     and POST /v1/project_logs/{id}/fetch {limit, cursor} answering
//     {events, cursor} (https://docs.braintrust.dev/api-reference).
//   - Datadog (DD-API-KEY and DD-APPLICATION-KEY; the API host of the
//     customer's site): GET /api/v2/llm-obs/v1/projects, GET
//     /api/v2/llm-obs/v1/{project}/datasets, GET
//     /api/v2/llm-obs/v1/{project}/datasets/{dataset}/records with
//     page[limit] and page[cursor] answering {data: [{id, input,
//     expected_output, metadata}], meta: {after}}, and POST
//     /api/v2/llm-obs/v1/spans/events/search
//     (https://docs.datadoghq.com/api/latest/agent-observability/).
//
// No real platform was called while writing this; the tests use local fakes
// of these endpoints.
package platformimport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Platforms.
const (
	LangSmith  = "langsmith"
	Langfuse   = "langfuse"
	Braintrust = "braintrust"
	Datadog    = "datadog"
)

// Kinds of import.
const (
	KindDataset = "dataset"
	KindTraces  = "traces"
)

// MaxRecords caps what one import reads.
const MaxRecords = 500

const (
	maxAnswerBytes = 32 << 20
	pageSize       = 100
	maxPages       = 50
	errorSnippet   = 300
	userAgent      = "heimdall-import/1"
)

// Platform describes one platform for the import form.
type Platform struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	BaseURL string   `json:"base_url"`
	Kinds   []string `json:"kinds"`
	// Secrets are the org secrets the import reads, by role, with the
	// default secret name of each.
	Secrets []SecretSpec `json:"secrets"`
	// DatasetHint and ProjectHint say what the dataset and project fields
	// hold on this platform.
	DatasetHint string `json:"dataset_hint"`
	ProjectHint string `json:"project_hint"`
	Docs        string `json:"docs"`
}

// SecretSpec is one credential an import needs.
type SecretSpec struct {
	Role    string `json:"role"` // api_key, public_key, secret_key, app_key
	Label   string `json:"label"`
	Default string `json:"default"`
}

// Platforms lists the platforms.
func Platforms() []Platform {
	return []Platform{
		{ID: LangSmith, Name: "LangSmith", BaseURL: "https://api.smith.langchain.com", Kinds: []string{KindDataset, KindTraces},
			Secrets:     []SecretSpec{{Role: "api_key", Label: "API key", Default: "LANGSMITH_API_KEY"}},
			DatasetHint: "Dataset name or id", ProjectHint: "Tracing project name or id (for traces)", Docs: "https://docs.langchain.com/langsmith"},
		{ID: Langfuse, Name: "Langfuse", BaseURL: "https://cloud.langfuse.com", Kinds: []string{KindDataset, KindTraces},
			Secrets:     []SecretSpec{{Role: "public_key", Label: "Public key", Default: "LANGFUSE_PUBLIC_KEY"}, {Role: "secret_key", Label: "Secret key", Default: "LANGFUSE_SECRET_KEY"}},
			DatasetHint: "Dataset name", ProjectHint: "Not needed: the keys name the project", Docs: "https://langfuse.com/docs/api"},
		{ID: Braintrust, Name: "Braintrust", BaseURL: "https://api.braintrust.dev", Kinds: []string{KindDataset, KindTraces},
			Secrets:     []SecretSpec{{Role: "api_key", Label: "API key", Default: "BRAINTRUST_API_KEY"}},
			DatasetHint: "Dataset name or id", ProjectHint: "Project name or id", Docs: "https://docs.braintrust.dev/api-reference"},
		{ID: Datadog, Name: "Datadog LLM Observability", BaseURL: "https://api.datadoghq.com", Kinds: []string{KindDataset, KindTraces},
			Secrets:     []SecretSpec{{Role: "api_key", Label: "API key", Default: "DD_API_KEY"}, {Role: "app_key", Label: "Application key", Default: "DD_APP_KEY"}},
			DatasetHint: "Dataset name or id", ProjectHint: "LLM Observability project name or id (datasets); span search query (traces, optional)", Docs: "https://docs.datadoghq.com/api/latest/agent-observability/"},
	}
}

// PlatformByID finds a platform.
func PlatformByID(id string) (Platform, bool) {
	for _, p := range Platforms() {
		if p.ID == id {
			return p, true
		}
	}
	return Platform{}, false
}

// Request is one import.
type Request struct {
	Platform string
	Kind     string
	BaseURL  string // empty: the platform's default
	Dataset  string
	Project  string
	Query    string // traces: a filter the platform understands (Datadog span query)
	Limit    int
	// Creds are the credential values by SecretSpec role.
	Creds map[string]string
}

// Record is one dataset item or trace, before it is mapped to a row.
type Record struct {
	ID       string         `json:"id"`
	Input    any            `json:"input"`
	Expected any            `json:"expected,omitempty"`
	Output   any            `json:"output,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
	Error    string         `json:"error,omitempty"`
	Name     string         `json:"name,omitempty"`
	// Version is the platform's own version of the record when it has one
	// (Braintrust _xact_id), kept in provenance.
	Version string `json:"version,omitempty"`
}

// Result is what a fetch read.
type Result struct {
	Records   []Record
	Truncated bool // more records exist than Limit
	Warnings  []string
}

// Fetch reads the records of r with c (which must enforce the caller's
// network rules).
func Fetch(ctx context.Context, c *http.Client, r Request) (*Result, error) {
	p, ok := PlatformByID(r.Platform)
	if !ok {
		return nil, fmt.Errorf("platform %q: want langsmith, langfuse, braintrust or datadog", r.Platform)
	}
	if r.Kind != KindDataset && r.Kind != KindTraces {
		return nil, errors.New("kind: want dataset or traces")
	}
	for _, s := range p.Secrets {
		if strings.TrimSpace(r.Creds[s.Role]) == "" {
			return nil, fmt.Errorf("%s %s is missing: add it under Settings > Secrets", p.Name, strings.ToLower(s.Label))
		}
	}
	if r.Limit <= 0 || r.Limit > MaxRecords {
		r.Limit = MaxRecords
	}
	if r.BaseURL == "" {
		r.BaseURL = p.BaseURL
	}
	r.BaseURL = strings.TrimRight(strings.TrimSpace(r.BaseURL), "/")
	if r.Kind == KindDataset && strings.TrimSpace(r.Dataset) == "" {
		return nil, errors.New("dataset is required: " + strings.ToLower(p.DatasetHint))
	}
	cl := &client{http: c, base: r.BaseURL}
	var res *Result
	var err error
	switch r.Platform {
	case LangSmith:
		cl.hdr = http.Header{"X-Api-Key": {r.Creds["api_key"]}}
		res, err = langsmith(ctx, cl, r)
	case Langfuse:
		cl.user, cl.pass = r.Creds["public_key"], r.Creds["secret_key"]
		res, err = langfuse(ctx, cl, r)
	case Braintrust:
		cl.hdr = http.Header{"Authorization": {"Bearer " + r.Creds["api_key"]}}
		res, err = braintrust(ctx, cl, r)
	case Datadog:
		cl.hdr = http.Header{"DD-API-KEY": {r.Creds["api_key"]}, "DD-APPLICATION-KEY": {r.Creds["app_key"]}}
		res, err = datadog(ctx, cl, r)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p.Name, err)
	}
	if len(res.Records) > r.Limit {
		res.Records, res.Truncated = res.Records[:r.Limit], true
	}
	return res, nil
}

// client sends JSON requests; it never puts a credential in an error.
type client struct {
	http       *http.Client
	base       string
	hdr        http.Header
	user, pass string
}

// HTTPError is a non-2xx answer.
type HTTPError struct {
	Method, Path string
	Status       int
	Body         string
}

func (e *HTTPError) Error() string {
	msg := fmt.Sprintf("%s %s: status %d", e.Method, e.Path, e.Status)
	switch e.Status {
	case http.StatusUnauthorized, http.StatusForbidden:
		msg += " (the key was refused: check the secret and its permissions)"
	case http.StatusNotFound:
		msg += " (not found: check the dataset, project and base URL)"
	}
	if e.Body != "" {
		msg += ": " + e.Body
	}
	return msg
}

func (c *client) do(ctx context.Context, method, p string, q url.Values, body, out any) error {
	u := c.base + p
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return fmt.Errorf("%s %s: %v", method, p, err)
	}
	for k, vs := range c.hdr {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	if c.user != "" || c.pass != "" {
		req.SetBasicAuth(c.user, c.pass)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return fmt.Errorf("%s %s: %v", method, p, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxAnswerBytes+1))
	if err != nil {
		return fmt.Errorf("%s %s: read answer: %v", method, p, err)
	}
	if resp.StatusCode/100 != 2 {
		snip := strings.Join(strings.Fields(string(b)), " ")
		if len(snip) > errorSnippet {
			snip = snip[:errorSnippet]
		}
		return &HTTPError{Method: method, Path: p, Status: resp.StatusCode, Body: snip}
	}
	if len(b) > maxAnswerBytes {
		return fmt.Errorf("%s %s: answer over %d MiB", method, p, maxAnswerBytes>>20)
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("%s %s: answer is not the expected JSON: %v", method, p, err)
	}
	return nil
}

var uuidRE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func isUUID(s string) bool { return uuidRE.MatchString(s) }

func str(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case nil:
		return ""
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	}
	return ""
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

// ---- LangSmith ----

func langsmith(ctx context.Context, c *client, r Request) (*Result, error) {
	if r.Kind == KindDataset {
		id := strings.TrimSpace(r.Dataset)
		if !isUUID(id) {
			var ds []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			}
			if err := c.do(ctx, http.MethodGet, "/api/v1/datasets", url.Values{"name": {id}}, nil, &ds); err != nil {
				return nil, err
			}
			if len(ds) == 0 {
				return nil, fmt.Errorf("no dataset named %q", r.Dataset)
			}
			id = ds[0].ID
		}
		res := &Result{}
		for off := 0; off < r.Limit+1 && off/pageSize < maxPages; off += pageSize {
			var ex []struct {
				ID       string         `json:"id"`
				Inputs   any            `json:"inputs"`
				Outputs  any            `json:"outputs"`
				Metadata map[string]any `json:"metadata"`
			}
			q := url.Values{"dataset": {id}, "offset": {strconv.Itoa(off)}, "limit": {strconv.Itoa(pageSize)}}
			if err := c.do(ctx, http.MethodGet, "/api/v1/examples", q, nil, &ex); err != nil {
				return nil, err
			}
			for _, e := range ex {
				res.Records = append(res.Records, Record{ID: e.ID, Input: e.Inputs, Expected: e.Outputs, Metadata: e.Metadata})
			}
			if len(ex) < pageSize {
				break
			}
		}
		return res, nil
	}
	project := strings.TrimSpace(r.Project)
	if project == "" {
		return nil, errors.New("project is required for traces: the tracing project name or id")
	}
	if !isUUID(project) {
		var ss []struct {
			ID string `json:"id"`
		}
		if err := c.do(ctx, http.MethodGet, "/api/v1/sessions", url.Values{"name": {project}}, nil, &ss); err != nil {
			return nil, err
		}
		if len(ss) == 0 {
			return nil, fmt.Errorf("no tracing project named %q", r.Project)
		}
		project = ss[0].ID
	}
	body := map[string]any{"project_ids": []string{project}, "is_root": true, "page_size": pageSize,
		"selects": []string{"ID", "NAME", "INPUTS", "OUTPUTS", "ERROR", "STATUS", "START_TIME"}}
	if q := strings.TrimSpace(r.Query); q != "" {
		body["filter"] = q
	}
	res := &Result{}
	for page := 0; page < maxPages && len(res.Records) <= r.Limit; page++ {
		var ans struct {
			Items []struct {
				ID      string `json:"id"`
				Name    string `json:"name"`
				Inputs  any    `json:"inputs"`
				Outputs any    `json:"outputs"`
				Error   string `json:"error"`
			} `json:"items"`
			NextCursor string `json:"next_cursor"`
		}
		if err := c.do(ctx, http.MethodPost, "/api/v2/runs/query", nil, body, &ans); err != nil {
			return nil, err
		}
		for _, it := range ans.Items {
			res.Records = append(res.Records, Record{ID: it.ID, Name: it.Name, Input: it.Inputs, Output: it.Outputs, Error: it.Error})
		}
		if ans.NextCursor == "" {
			break
		}
		body["cursor"] = ans.NextCursor
	}
	return res, nil
}

// ---- Langfuse ----

func langfuse(ctx context.Context, c *client, r Request) (*Result, error) {
	res := &Result{}
	for page := 1; page <= maxPages && len(res.Records) <= r.Limit; page++ {
		var ans struct {
			Data []struct {
				ID             string         `json:"id"`
				Name           string         `json:"name"`
				Input          any            `json:"input"`
				Output         any            `json:"output"`
				ExpectedOutput any            `json:"expectedOutput"`
				Metadata       map[string]any `json:"metadata"`
				Status         string         `json:"status"`
			} `json:"data"`
			Meta struct {
				TotalPages int `json:"totalPages"`
			} `json:"meta"`
		}
		q := url.Values{"page": {strconv.Itoa(page)}, "limit": {"50"}}
		p := "/api/public/traces"
		if r.Kind == KindDataset {
			p = "/api/public/dataset-items"
			q.Set("datasetName", strings.TrimSpace(r.Dataset))
		}
		if err := c.do(ctx, http.MethodGet, p, q, nil, &ans); err != nil {
			return nil, err
		}
		for _, d := range ans.Data {
			if r.Kind == KindDataset && strings.EqualFold(d.Status, "ARCHIVED") {
				continue
			}
			res.Records = append(res.Records, Record{ID: d.ID, Name: d.Name, Input: d.Input, Expected: d.ExpectedOutput, Output: d.Output, Metadata: d.Metadata})
		}
		if page >= ans.Meta.TotalPages || len(ans.Data) == 0 {
			break
		}
	}
	return res, nil
}

// ---- Braintrust ----

func braintrustLookup(ctx context.Context, c *client, p string, q url.Values, what string) (string, error) {
	var ans struct {
		Objects []struct {
			ID string `json:"id"`
		} `json:"objects"`
	}
	if err := c.do(ctx, http.MethodGet, p, q, nil, &ans); err != nil {
		return "", err
	}
	if len(ans.Objects) == 0 {
		return "", fmt.Errorf("no %s found", what)
	}
	return ans.Objects[0].ID, nil
}

func braintrust(ctx context.Context, c *client, r Request) (*Result, error) {
	var fetchPath string
	if r.Kind == KindDataset {
		id := strings.TrimSpace(r.Dataset)
		if !isUUID(id) {
			q := url.Values{"dataset_name": {id}}
			if p := strings.TrimSpace(r.Project); p != "" {
				q.Set("project_name", p)
			}
			var err error
			if id, err = braintrustLookup(ctx, c, "/v1/dataset", q, "dataset named "+strconv.Quote(r.Dataset)); err != nil {
				return nil, err
			}
		}
		fetchPath = "/v1/dataset/" + url.PathEscape(id) + "/fetch"
	} else {
		id := strings.TrimSpace(r.Project)
		if id == "" {
			return nil, errors.New("project is required for traces: the project name or id")
		}
		if !isUUID(id) {
			var err error
			if id, err = braintrustLookup(ctx, c, "/v1/project", url.Values{"project_name": {id}}, "project named "+strconv.Quote(r.Project)); err != nil {
				return nil, err
			}
		}
		fetchPath = "/v1/project_logs/" + url.PathEscape(id) + "/fetch"
	}
	res := &Result{}
	seen := map[string]bool{}
	body := map[string]any{"limit": pageSize}
	for page := 0; page < maxPages && len(res.Records) <= r.Limit; page++ {
		var ans struct {
			Events []struct {
				ID         string         `json:"id"`
				XactID     string         `json:"_xact_id"`
				SpanID     string         `json:"span_id"`
				RootSpanID string         `json:"root_span_id"`
				Input      any            `json:"input"`
				Output     any            `json:"output"`
				Expected   any            `json:"expected"`
				Metadata   map[string]any `json:"metadata"`
				Error      any            `json:"error"`
			} `json:"events"`
			Cursor string `json:"cursor"`
		}
		if err := c.do(ctx, http.MethodPost, fetchPath, nil, body, &ans); err != nil {
			return nil, err
		}
		for _, e := range ans.Events {
			if seen[e.ID] { // later pages may repeat rows: deduplicate by id
				continue
			}
			if r.Kind == KindTraces && e.RootSpanID != "" && e.SpanID != e.RootSpanID {
				continue // only the root span of each trace
			}
			seen[e.ID] = true
			res.Records = append(res.Records, Record{ID: e.ID, Input: e.Input, Expected: e.Expected, Output: e.Output, Metadata: e.Metadata, Error: str(e.Error), Version: e.XactID})
		}
		if ans.Cursor == "" || len(ans.Events) == 0 {
			break
		}
		body["cursor"] = ans.Cursor
	}
	return res, nil
}

// ---- Datadog ----

// ddObject reads an id and a name from either a flat JSON:API object or
// one with attributes.
func ddObject(v map[string]any) (id, name string) {
	id = str(v["id"])
	name = str(v["name"])
	if a := asMap(v["attributes"]); a != nil && name == "" {
		name = str(a["name"])
	}
	return id, name
}

func ddLookup(ctx context.Context, c *client, p, want, what string) (string, error) {
	if isUUID(want) {
		return want, nil
	}
	var ans struct {
		Data []map[string]any `json:"data"`
	}
	if err := c.do(ctx, http.MethodGet, p, nil, nil, &ans); err != nil {
		return "", err
	}
	for _, d := range ans.Data {
		if id, name := ddObject(d); id == want || strings.EqualFold(name, want) {
			return id, nil
		}
	}
	return "", fmt.Errorf("no %s named %q", what, want)
}

func ddMessages(v any) any {
	m := asMap(v)
	if m == nil {
		return v
	}
	if msgs, ok := m["messages"].([]any); ok && len(msgs) > 0 {
		return map[string]any{"messages": msgs}
	}
	if s, ok := m["value"]; ok {
		return s
	}
	return v
}

func datadog(ctx context.Context, c *client, r Request) (*Result, error) {
	res := &Result{}
	if r.Kind == KindDataset {
		if strings.TrimSpace(r.Project) == "" {
			return nil, errors.New("project is required: the LLM Observability project that holds the dataset")
		}
		project, err := ddLookup(ctx, c, "/api/v2/llm-obs/v1/projects", strings.TrimSpace(r.Project), "project")
		if err != nil {
			return nil, err
		}
		ds, err := ddLookup(ctx, c, "/api/v2/llm-obs/v1/"+url.PathEscape(project)+"/datasets", strings.TrimSpace(r.Dataset), "dataset")
		if err != nil {
			return nil, err
		}
		p := "/api/v2/llm-obs/v1/" + url.PathEscape(project) + "/datasets/" + url.PathEscape(ds) + "/records"
		q := url.Values{"page[limit]": {strconv.Itoa(pageSize)}}
		for page := 0; page < maxPages && len(res.Records) <= r.Limit; page++ {
			var ans struct {
				Data []map[string]any `json:"data"`
				Meta struct {
					After string `json:"after"`
				} `json:"meta"`
			}
			if err := c.do(ctx, http.MethodGet, p, q, nil, &ans); err != nil {
				return nil, err
			}
			for _, d := range ans.Data {
				src := d
				if a := asMap(d["attributes"]); a != nil {
					src = a
				}
				res.Records = append(res.Records, Record{ID: str(d["id"]), Input: src["input"], Expected: src["expected_output"], Metadata: asMap(src["metadata"])})
			}
			if ans.Meta.After == "" || len(ans.Data) == 0 {
				break
			}
			q.Set("page[cursor]", ans.Meta.After)
		}
		return res, nil
	}
	query := strings.TrimSpace(r.Query)
	if query == "" {
		query = strings.TrimSpace(r.Project)
	}
	page := map[string]any{"limit": pageSize}
	filter := map[string]any{"from": "now-7d", "to": "now"}
	if query != "" {
		filter["query"] = query
	}
	body := map[string]any{"data": map[string]any{"type": "spans", "attributes": map[string]any{"filter": filter, "page": page}}}
	seen := map[string]bool{}
	for n := 0; n < maxPages && len(res.Records) <= r.Limit; n++ {
		var ans struct {
			Data []struct {
				ID         string         `json:"id"`
				Attributes map[string]any `json:"attributes"`
			} `json:"data"`
			Meta struct {
				Page struct {
					After string `json:"after"`
				} `json:"page"`
			} `json:"meta"`
		}
		if err := c.do(ctx, http.MethodPost, "/api/v2/llm-obs/v1/spans/events/search", nil, body, &ans); err != nil {
			return nil, err
		}
		for _, d := range ans.Data {
			a := d.Attributes
			parent := str(a["parent_id"])
			if parent != "" && parent != "undefined" {
				continue // only root spans: one conversation per trace
			}
			trace := firstNonEmpty(str(a["trace_id"]), d.ID)
			if seen[trace] {
				continue
			}
			seen[trace] = true
			rec := Record{ID: trace, Name: str(a["name"]), Input: ddMessages(a["input"]), Output: ddMessages(a["output"]), Metadata: asMap(a["metadata"])}
			if strings.EqualFold(str(a["status"]), "error") {
				rec.Error = firstNonEmpty(str(asMap(a["error"])["message"]), "span status error")
			}
			res.Records = append(res.Records, rec)
		}
		if ans.Meta.Page.After == "" || len(ans.Data) == 0 {
			break
		}
		page["cursor"] = ans.Meta.Page.After
	}
	return res, nil
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

// ---- mapping ----

// Mapping names the fields of a record's input and expected objects that
// hold the question and the answer (dotted paths such as
// inputs.question). Empty fields are guessed.
type Mapping struct {
	Input    string `json:"input,omitempty"`
	Expected string `json:"expected,omitempty"`
	Rubric   string `json:"rubric,omitempty"`
}

var inputKeys = []string{"question", "input", "query", "prompt", "text", "message", "content", "user_input", "ticket"}
var outputKeys = []string{"answer", "output", "expected", "expected_output", "response", "reference", "text", "content", "result", "completion"}

// Pick reads a dotted path from v.
func Pick(v any, p string) any {
	for _, part := range strings.Split(p, ".") {
		m := asMap(v)
		if m == nil {
			return nil
		}
		v = m[part]
	}
	return v
}

// Text is v as text: a string as is, an object's likely text field, a
// chat message list's last content, else compact JSON.
func Text(v any, keys []string) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64, bool:
		b, _ := json.Marshal(x)
		return string(b)
	case map[string]any:
		for _, k := range keys {
			if s, ok := x[k].(string); ok && strings.TrimSpace(s) != "" {
				return s
			}
		}
		if msgs, ok := x["messages"].([]any); ok && len(msgs) > 0 {
			if m := asMap(msgs[len(msgs)-1]); m != nil {
				return str(m["content"])
			}
		}
		if len(x) == 1 {
			for _, only := range x {
				if s, ok := only.(string); ok {
					return s
				}
			}
		}
	case []any:
		if len(x) > 0 {
			if m := asMap(x[len(x)-1]); m != nil && m["content"] != nil {
				return str(m["content"])
			}
		}
	}
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}

// Messages is v as a chat conversation when it is one: a list of {role,
// content} or an object with such a "messages" list. LangChain message
// objects ({type: human|ai, content}) are read too.
func Messages(v any) []map[string]string {
	var list []any
	switch x := v.(type) {
	case []any:
		list = x
	case map[string]any:
		if l, ok := x["messages"].([]any); ok {
			list = l
		}
	}
	var out []map[string]string
	for _, e := range list {
		if inner, ok := e.([]any); ok { // LangChain batches: a list of lists
			for _, ie := range inner {
				if m := message(ie); m != nil {
					out = append(out, m)
				}
			}
			continue
		}
		if m := message(e); m != nil {
			out = append(out, m)
		}
	}
	return out
}

func message(e any) map[string]string {
	m := asMap(e)
	if m == nil {
		return nil
	}
	role := strings.ToLower(firstNonEmpty(str(m["role"]), str(m["type"])))
	if kw := asMap(m["kwargs"]); kw != nil { // serialized LangChain message
		if c := str(kw["content"]); c != "" {
			m = map[string]any{"content": c}
			if id, ok := e.(map[string]any)["id"].([]any); ok && len(id) > 0 {
				role = strings.ToLower(str(id[len(id)-1]))
			}
		}
	}
	switch {
	case strings.Contains(role, "human") || role == "user":
		role = "user"
	case strings.Contains(role, "ai") || role == "assistant":
		role = "assistant"
	case strings.Contains(role, "system"):
		role = "system"
	case strings.Contains(role, "tool") || role == "function":
		role = "tool"
	default:
		return nil
	}
	content := str(m["content"])
	if content == "" {
		if parts, ok := m["content"].([]any); ok {
			var b []string
			for _, p := range parts {
				if pm := asMap(p); pm != nil && str(pm["text"]) != "" {
					b = append(b, str(pm["text"]))
				}
			}
			content = strings.Join(b, "\n")
		}
	}
	if strings.TrimSpace(content) == "" {
		return nil
	}
	return map[string]string{"role": role, "content": content}
}

// FlatMetadata keeps the scalar values of a metadata object (strings,
// numbers, booleans), at most max keys in sorted order, and drops keys a
// row cannot hold.
func FlatMetadata(m map[string]any, max int) map[string]any {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := map[string]any{}
	for _, k := range keys {
		if len(out) >= max {
			break
		}
		if k == "" || len(k) > 64 || strings.ContainsAny(k, " \t\r\n.") {
			continue
		}
		switch v := m[k].(type) {
		case string:
			if len(v) <= 2000 {
				out[k] = v
			}
		case float64, bool:
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
