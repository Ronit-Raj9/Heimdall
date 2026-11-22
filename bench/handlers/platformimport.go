package handlers

// Imports from other platforms (lane Y15): a dataset or recent traces from
// LangSmith, Langfuse, Braintrust or Datadog LLM Observability, read with
// the org's own keys (Settings > Secrets), become benchmark tasks through
// the dataset API's writer (personal data policy included) or items in the
// failure inbox. On a hosted server every URL must be public https and the
// pull runs under the hosted pull caps (liveinbox_pull.go).

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/datapolicy"
	"github.com/maximhq/bifrost/bench/dataset"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/platformimport"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/traceinbox"
	"github.com/valyala/fasthttp"
)

// Import targets.
const (
	ImportToBenchmark = "benchmark"
	ImportToInbox     = "inbox"
)

// MaxInboxImport caps the traces one import puts in the inbox.
const MaxInboxImport = 200

// PlatformImportPlatform is one platform in GET /imports/platforms, with
// whether the org has each secret under its default name.
type PlatformImportPlatform struct {
	platformimport.Platform
	Configured map[string]bool `json:"configured"`
}

// PlatformImportList answers GET /imports/platforms.
type PlatformImportList struct {
	Platforms []PlatformImportPlatform `json:"platforms"`
	// Secrets are the names of the org's secrets, for the secret pickers.
	Secrets []string `json:"secrets"`
	// SecretsLink is where secrets are added.
	SecretsLink string `json:"secrets_link"`
}

// PlatformImportRequest is the body of POST /imports/platforms.
type PlatformImportRequest struct {
	Platform string `json:"platform"`
	Kind     string `json:"kind"` // dataset or traces
	BaseURL  string `json:"base_url,omitempty"`
	Dataset  string `json:"dataset,omitempty"`
	Project  string `json:"project,omitempty"`
	Query    string `json:"query,omitempty"`
	Limit    int    `json:"limit,omitempty"`
	// Secrets names the org secret of each credential role; a missing
	// role reads the platform's default secret name.
	Secrets map[string]string `json:"secrets,omitempty"`
	// Target is benchmark (rows become tasks of Benchmark) or inbox
	// (traces become failure inbox items).
	Target    string                 `json:"target"`
	Benchmark string                 `json:"benchmark,omitempty"`
	Mapping   platformimport.Mapping `json:"mapping,omitempty"`
	Mode      string                 `json:"mode,omitempty"`
	Match     string                 `json:"match,omitempty"`
	DryRun    bool                   `json:"dry_run,omitempty"`
}

// PlatformImportPreview is one record as it would be imported.
type PlatformImportPreview struct {
	SourceID string `json:"source_id"`
	Input    string `json:"input"`
	Expected string `json:"expected,omitempty"`
	Chat     bool   `json:"chat,omitempty"`
}

// PlatformImportResult answers POST /imports/platforms.
type PlatformImportResult struct {
	Platform  string                  `json:"platform"`
	Kind      string                  `json:"kind"`
	Target    string                  `json:"target"`
	Read      int                     `json:"read"`
	Truncated bool                    `json:"truncated"`
	Warnings  []string                `json:"warnings"`
	Preview   []PlatformImportPreview `json:"preview"`
	// Benchmark is the write result for target benchmark.
	Benchmark *DatasetTasksResult `json:"benchmark,omitempty"`
	// Inbox counts what reached the failure inbox for target inbox.
	Inbox *PlatformImportInbox `json:"inbox,omitempty"`
	Next  string               `json:"next"`
}

// PlatformImportInbox is what a traces import did to the inbox.
type PlatformImportInbox struct {
	Created  int      `json:"created"`
	Repeated int      `json:"repeated"`
	Items    []string `json:"items"`
	Problems []string `json:"problems"`
}

type platformImportHandler struct {
	env    OrgEnv
	ds     *datasetAPI
	client func() *http.Client // tests swap it
	now    func() time.Time
}

func init() {
	RegisterOrgArea("platformimport", func(env OrgEnv, r *router.Router) {
		h := &platformImportHandler{env: env, now: time.Now,
			ds: &datasetAPI{build: &suiteBuild{&suiteAuthoring{mode: env.Mode, projects: env.Services.Projects, suites: env.Services.Suites}}, env: env, now: time.Now}}
		h.client = func() *http.Client {
			if env.Services.Hosted {
				return HostedPullClient()
			}
			return &http.Client{Timeout: 60 * time.Second}
		}
		r.GET(APIPrefix+"/imports/platforms", env.R(h.list))
		r.POST(APIPrefix+"/imports/platforms", env.W(h.run))
	})
}

// secret reads one credential: the org secret of that name, or on a local
// server without a key store the environment variable of that name.
func (h *platformImportHandler) secret(ctx context.Context, name string) string {
	if h.env.Services.Keys != nil {
		s := h.env.Services
		if s.OrgID == "" {
			s.OrgID = "local"
		}
		return OrgSecretGetenv(ctx, s)(name)
	}
	if !h.env.Services.Hosted {
		return os.Getenv(name)
	}
	return ""
}

func (h *platformImportHandler) list(ctx *fasthttp.RequestCtx) {
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	s := h.env.Services
	if s.OrgID == "" {
		s.OrgID = "local"
	}
	names := orgSecretNames(work, s)
	out := PlatformImportList{Secrets: []string{}, SecretsLink: PullLinkSecrets}
	for n := range names {
		out.Secrets = append(out.Secrets, n)
	}
	sort.Strings(out.Secrets)
	for _, p := range platformimport.Platforms() {
		pp := PlatformImportPlatform{Platform: p, Configured: map[string]bool{}}
		for _, sp := range p.Secrets {
			pp.Configured[sp.Role] = names[sp.Default] || (!s.Hosted && s.Keys == nil && os.Getenv(sp.Default) != "")
		}
		out.Platforms = append(out.Platforms, pp)
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, out)
}

func (h *platformImportHandler) bad(ctx *fasthttp.RequestCtx, msg string) {
	lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, msg)
}

func (h *platformImportHandler) run(ctx *fasthttp.RequestCtx) {
	var req PlatformImportRequest
	if err := decodeAuthoring(ctx.PostBody(), &req); err != nil {
		h.bad(ctx, `body must be {"platform","kind","target","benchmark"?,"dataset"?,"project"?,"query"?,"base_url"?,"limit"?,"secrets"?,"mapping"?,"dry_run"?}: `+err.Error())
		return
	}
	p, ok := platformimport.PlatformByID(req.Platform)
	if !ok {
		h.bad(ctx, "platform: want langsmith, langfuse, braintrust or datadog")
		return
	}
	if req.Target != ImportToBenchmark && req.Target != ImportToInbox {
		h.bad(ctx, "target: want benchmark or inbox")
		return
	}
	if req.Target == ImportToInbox && req.Kind != platformimport.KindTraces {
		h.bad(ctx, "the failure inbox takes traces: set kind to traces")
		return
	}
	for _, f := range []string{req.BaseURL, req.Dataset, req.Project, req.Query, req.Mapping.Input, req.Mapping.Expected, req.Mapping.Rubric} {
		if len(f) > 500 || strings.ContainsAny(f, "\r\n") {
			h.bad(ctx, "base_url, dataset, project, query and mapping fields are one line of at most 500 characters")
			return
		}
	}
	base := strings.TrimSpace(req.BaseURL)
	if base == "" {
		base = p.BaseURL
	}
	if h.env.Services.Hosted {
		if err := CheckPullURL(base); err != nil {
			h.bad(ctx, "base_url: "+err.Error())
			return
		}
	}
	var dir string
	if req.Target == ImportToBenchmark {
		var ok bool
		if dir, ok = h.benchmarkDir(ctx, req.Benchmark); !ok {
			return
		}
	}
	work, cancel := context.WithTimeout(context.Background(), HostedPullTimeout)
	defer cancel()
	creds := map[string]string{}
	for _, sp := range p.Secrets {
		name := strings.TrimSpace(req.Secrets[sp.Role])
		if name == "" {
			name = sp.Default
		}
		v := h.secret(work, name)
		if v == "" {
			lib.SendErrorLink(ctx, h.env.Mode, fasthttp.StatusConflict, lib.ErrTypeConflict,
				"the "+p.Name+" "+strings.ToLower(sp.Label)+" is not set: add a secret named "+name+" under Settings > Secrets", PullLinkSecrets)
			return
		}
		creds[sp.Role] = v
	}
	limit := req.Limit
	if req.Target == ImportToInbox && (limit <= 0 || limit > MaxInboxImport) {
		limit = MaxInboxImport
	}
	res, err := platformimport.Fetch(work, h.client(), platformimport.Request{Platform: req.Platform, Kind: req.Kind, BaseURL: base,
		Dataset: req.Dataset, Project: req.Project, Query: req.Query, Limit: limit, Creds: creds})
	if err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadGateway, lib.ErrTypeUnavailable, err.Error())
		return
	}
	out := PlatformImportResult{Platform: req.Platform, Kind: req.Kind, Target: req.Target, Read: len(res.Records),
		Truncated: res.Truncated, Warnings: append([]string{}, res.Warnings...), Preview: []PlatformImportPreview{}}
	if res.Truncated {
		out.Warnings = append(out.Warnings, "more records exist than this import reads; import again with a narrower dataset or query")
	}
	rows := make([]dataset.Row, 0, len(res.Records))
	for _, rec := range res.Records {
		rows = append(rows, platformimport.ToRow(rec, req.Kind, req.Platform, req.Mapping))
	}
	for i, r := range rows {
		if i >= 5 {
			break
		}
		pv := PlatformImportPreview{SourceID: res.Records[i].ID, Input: preview(dataset.Question(r), 300), Chat: len(r.Messages) > 0}
		if len(r.Expected) > 0 {
			pv.Expected = preview(r.Expected[0], 200)
		} else {
			pv.Expected = preview(r.Reference, 200)
		}
		out.Preview = append(out.Preview, pv)
	}
	if len(rows) == 0 {
		out.Next = "Nothing to import: the " + req.Kind + " is empty."
		lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, out)
		return
	}
	source := p.Name + " " + req.Kind
	if req.Dataset != "" {
		source += " " + req.Dataset
	} else if req.Project != "" {
		source += " " + req.Project
	}
	if len(source) > 200 {
		source = source[:200]
	}
	switch req.Target {
	case ImportToBenchmark:
		mode := req.Mode
		if mode == "" {
			mode = DatasetAdd
		}
		if mode != DatasetAdd && mode != DatasetUpsert {
			h.bad(ctx, "mode: want add or upsert")
			return
		}
		wr, status := h.ds.writeRows(ctx, req.Benchmark, dir, rows, mode, req.Match, source, req.DryRun)
		if wr == nil {
			return
		}
		out.Benchmark, out.Next = wr, wr.Next
		lib.SendJSON(ctx, h.env.Mode, status, out)
	case ImportToInbox:
		in, err := h.toInbox(work, req, res.Records, source)
		if err != nil {
			lib.SendError(ctx, h.env.Mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "failure inbox: "+err.Error())
			return
		}
		out.Inbox = in
		switch {
		case req.DryRun:
			out.Next = "Check the preview, then import."
		default:
			out.Next = "Open the failure inbox to turn the traces worth keeping into tasks."
		}
		lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, out)
	}
}

func preview(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// benchmarkDir checks the target benchmark (exists, editable, visible to
// the caller) and returns its folder.
func (h *platformImportHandler) benchmarkDir(ctx *fasthttp.RequestCtx, id string) (string, bool) {
	if h.env.Services.Projects == nil {
		h.ds.build.needProjects(ctx)
		return "", false
	}
	if !validID(id) {
		h.bad(ctx, "benchmark: name the question-and-answer benchmark the rows go into")
		return "", false
	}
	if isBuiltinSuite(h.env.Services.Suites, id) {
		sendBuiltinReadOnly(ctx, h.env.Mode, id)
		return "", false
	}
	pol, err := datapolicy.Open(h.env.Services.Projects.Root()).Get(id)
	if err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "read the access list: "+err.Error())
		return "", false
	}
	if !pol.Allows(callerOf(ctx).viewer()) {
		sendRestricted(ctx, h.env.Mode, id)
		return "", false
	}
	dir, err := h.env.Services.Projects.Resolve(projectfs.AreaBenchmarks, id)
	if err != nil {
		h.ds.build.sendStoreError(ctx, "benchmark "+id, err)
		return "", false
	}
	if fi, err := os.Stat(filepath.Join(dir, "suite.yaml")); err != nil || !fi.Mode().IsRegular() {
		sendNotFound(ctx, h.env.Mode, "benchmark", id)
		return "", false
	}
	return dir, true
}

// toInbox pushes each trace into the org's failure inbox as the webhook
// does: parsed, redacted, deduplicated by signature.
func (h *platformImportHandler) toInbox(ctx context.Context, req PlatformImportRequest, recs []platformimport.Record, source string) (*PlatformImportInbox, error) {
	out := &PlatformImportInbox{Items: []string{}, Problems: []string{}}
	if req.DryRun {
		return out, nil
	}
	root := h.ds.root()
	if root == "" {
		return nil, errors.New("no project folder on this server")
	}
	dir := filepath.Join(root, traceinbox.Dir)
	cfg, err := traceinbox.LoadConfig(filepath.Join(dir, traceinbox.SourcesFile))
	if err != nil {
		return nil, err
	}
	benchRoot := filepath.Join(root, string(projectfs.AreaBenchmarks))
	ib := traceinbox.New(dir, cfg, benchRoot)
	ib.ProjectRoot = root
	src := &traceinbox.Source{Name: req.Platform + "-import", Kind: traceinbox.KindWebhook}
	for _, rec := range recs {
		reason := "imported from " + source
		if rec.Error != "" {
			reason += ": " + preview(rec.Error, 160)
		}
		r, err := ib.Ingest(ctx, src, traceinbox.IngestRequest{Trace: platformimport.ChatTrace(rec), Format: "chat", Reason: reason, Source: req.Platform})
		if err != nil {
			if len(out.Problems) < 50 {
				out.Problems = append(out.Problems, rec.ID+": "+err.Error())
			}
			continue
		}
		if r.Created {
			out.Created++
		} else {
			out.Repeated++
		}
		out.Items = append(out.Items, r.ItemID)
	}
	return out, nil
}
