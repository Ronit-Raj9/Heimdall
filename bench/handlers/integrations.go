package handlers

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/ci"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/webhooks"
	"github.com/valyala/fasthttp"
)

// Web parity lane L19, Integrations (/integrations in the UI):
//   - outgoing webhooks fired when an experiment run ends (webhooks package;
//     cmd/bifrost-bench/integrations.go fires them from the job finish hook),
//   - export files written by the integrations.export job, listed and
//     downloaded here,
//   - a preview of the GitHub Actions workflow the CI install PR (lane L07,
//     POST /github/ci-install) adds to a repository.

// DefaultCISource is the GitHub repository the CI workflow builds Heimdall
// from (the same default as `bifrost-bench ci install`).
var DefaultCISource = "Ronit-Raj9/Heimdall"

// Export formats of the integrations.export job.
var IntegrationExportFormats = []string{"otlp", "eee", "jsonl", "csv", "harbor"}

// IntegrationsExportDir is where integrations.export writes its files: under
// the project's .heimdall folder, which the project files API never serves.
func IntegrationsExportDir(projectRoot string) string {
	return filepath.Join(projectRoot, ".heimdall", "exports")
}

// MaxExportFiles is how many export files a project keeps; the oldest go first.
const MaxExportFiles = 50

// IntegrationExport is one export file.
type IntegrationExport struct {
	Name      string    `json:"name"`
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"created_at"`
	Format    string    `json:"format"`
}

// IntegrationExportList is GET /integrations/exports.
type IntegrationExportList struct {
	Exports []IntegrationExport `json:"exports"`
	Formats []string            `json:"formats"`
}

// WebhookList is GET /integrations/webhooks.
type WebhookList struct {
	Webhooks []webhooks.View `json:"webhooks"`
	Events   []string        `json:"events"`
	Hosted   bool            `json:"hosted"`
}

// WebhookCreated is POST /integrations/webhooks: the secret is shown once.
type WebhookCreated struct {
	Webhook webhooks.View `json:"webhook"`
	Secret  string        `json:"secret"`
}

// WebhookDeliveries is GET /integrations/deliveries.
type WebhookDeliveries struct {
	Deliveries []webhooks.Delivery `json:"deliveries"`
}

// CIWorkflowPreview is GET /integrations/ci/workflow.
type CIWorkflowPreview struct {
	Path     string `json:"path"`
	Mode     string `json:"mode"`
	Workflow string `json:"workflow"`
}

type integrationsHandler struct {
	mode   schemas.Mode
	root   string
	hosted bool
}

func init() {
	RegisterOrgArea("integrations", func(env OrgEnv, r *router.Router) {
		h := &integrationsHandler{mode: env.Mode, hosted: env.Services.Hosted}
		if env.Cfg != nil {
			h.root = env.Cfg.Root
		}
		r.GET(APIPrefix+"/integrations/webhooks", env.R(h.listHooks))
		r.POST(APIPrefix+"/integrations/webhooks", env.W(h.createHook))
		r.PUT(APIPrefix+"/integrations/webhooks/{id}", env.W(h.updateHook))
		r.DELETE(APIPrefix+"/integrations/webhooks/{id}", env.W(h.deleteHook))
		r.POST(APIPrefix+"/integrations/webhooks/{id}/test", env.W(h.testHook))
		r.GET(APIPrefix+"/integrations/deliveries", env.R(h.deliveries))
		r.GET(APIPrefix+"/integrations/exports", env.R(h.listExports))
		r.GET(APIPrefix+"/integrations/exports/{name}", env.R(h.downloadExport))
		r.DELETE(APIPrefix+"/integrations/exports/{name}", env.W(h.deleteExport))
		r.GET(APIPrefix+"/integrations/ci/workflow", env.R(h.ciWorkflow))
	})
}

func (h *integrationsHandler) store(ctx *fasthttp.RequestCtx) (*webhooks.Store, bool) {
	if h.root == "" {
		sendUnavailable(ctx, h.mode, "integrations (no project folder)")
		return nil, false
	}
	return webhooks.Open(h.root), true
}

func (h *integrationsHandler) sendHookErr(ctx *fasthttp.RequestCtx, err error) {
	switch {
	case errors.Is(err, webhooks.ErrNotFound):
		lib.SendError(ctx, h.mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, "webhook not found; refresh the list")
	case errors.Is(err, webhooks.ErrInvalid):
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, err.Error())
	default:
		lib.SendError(ctx, h.mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "webhooks: "+err.Error())
	}
}

func (h *integrationsHandler) listHooks(ctx *fasthttp.RequestCtx) {
	st, ok := h.store(ctx)
	if !ok {
		return
	}
	hs, err := st.List()
	if err != nil {
		h.sendHookErr(ctx, err)
		return
	}
	ds, _ := st.Deliveries(0)
	out := WebhookList{Webhooks: []webhooks.View{}, Events: webhooks.Events, Hosted: h.hosted}
	for _, hk := range hs {
		v := hk.View()
		for i := range ds { // newest first
			if ds[i].HookID == hk.ID {
				d := ds[i]
				v.LastDelivery = &d
				break
			}
		}
		out.Webhooks = append(out.Webhooks, v)
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
}

func decodeHookInput(ctx *fasthttp.RequestCtx) (webhooks.Input, error) {
	var in webhooks.Input
	dec := json.NewDecoder(strings.NewReader(string(ctx.PostBody())))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return in, errors.New("body must be JSON {url, events, description, enabled}: " + err.Error())
	}
	return in, nil
}

func actorOf(ctx *fasthttp.RequestCtx) string {
	p := auth.PrincipalFrom(ctx)
	if p.UserID != "" {
		return p.UserID
	}
	return string(p.Kind)
}

func (h *integrationsHandler) createHook(ctx *fasthttp.RequestCtx) {
	st, ok := h.store(ctx)
	if !ok {
		return
	}
	in, err := decodeHookInput(ctx)
	if err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, err.Error())
		return
	}
	hk, err := st.Create(in, actorOf(ctx), !h.hosted)
	if err != nil {
		h.sendHookErr(ctx, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusCreated, WebhookCreated{Webhook: hk.View(), Secret: hk.Secret})
}

func (h *integrationsHandler) updateHook(ctx *fasthttp.RequestCtx) {
	st, ok := h.store(ctx)
	if !ok {
		return
	}
	id, ok := idParam(ctx, h.mode, "id", "webhook")
	if !ok {
		return
	}
	in, err := decodeHookInput(ctx)
	if err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, err.Error())
		return
	}
	hk, err := st.Update(id, in, !h.hosted)
	if err != nil {
		h.sendHookErr(ctx, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, hk.View())
}

func (h *integrationsHandler) deleteHook(ctx *fasthttp.RequestCtx) {
	st, ok := h.store(ctx)
	if !ok {
		return
	}
	id, ok := idParam(ctx, h.mode, "id", "webhook")
	if !ok {
		return
	}
	if err := st.Delete(id); err != nil {
		h.sendHookErr(ctx, err)
		return
	}
	ctx.SetStatusCode(fasthttp.StatusNoContent)
}

// testHook sends a ping event to one hook now (one attempt) and returns the delivery.
func (h *integrationsHandler) testHook(ctx *fasthttp.RequestCtx) {
	st, ok := h.store(ctx)
	if !ok {
		return
	}
	id, ok := idParam(ctx, h.mode, "id", "webhook")
	if !ok {
		return
	}
	hk, err := st.Get(id)
	if err != nil {
		h.sendHookErr(ctx, err)
		return
	}
	wctx, cancel := workContext(ctx, 20*time.Second)
	defer cancel()
	ev := webhooks.NewEvent(webhooks.EventPing, auth.PrincipalFrom(ctx).OrgID, map[string]string{"message": "Test delivery from Heimdall", "webhook_id": hk.ID})
	d := webhooks.Sender{AllowPrivate: !h.hosted, Attempts: 1}.Send(wctx, hk, ev)
	_ = st.Record(d)
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, d)
}

func (h *integrationsHandler) deliveries(ctx *fasthttp.RequestCtx) {
	st, ok := h.store(ctx)
	if !ok {
		return
	}
	limit := 50
	if v, err := strconv.Atoi(string(ctx.QueryArgs().Peek("limit"))); err == nil && v > 0 && v <= 200 {
		limit = v
	}
	ds, err := st.Deliveries(limit)
	if err != nil {
		h.sendHookErr(ctx, err)
		return
	}
	if ds == nil {
		ds = []webhooks.Delivery{}
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, WebhookDeliveries{Deliveries: ds})
}

// exportFormatOf names the format of an export file from its extension.
func exportFormatOf(name string) string {
	switch {
	case strings.HasSuffix(name, ".otlp.json"):
		return "otlp"
	case strings.HasSuffix(name, ".eee.jsonl"):
		return "eee"
	case strings.HasSuffix(name, ".runs.jsonl"):
		return "jsonl"
	case strings.HasSuffix(name, ".runs.csv"):
		return "csv"
	case strings.HasSuffix(name, ".harbor.tar.gz"):
		return "harbor"
	}
	return ""
}

// ListIntegrationExports lists a project's export files, newest first.
func ListIntegrationExports(projectRoot string) ([]IntegrationExport, error) {
	ents, err := os.ReadDir(IntegrationsExportDir(projectRoot))
	if errors.Is(err, os.ErrNotExist) {
		return []IntegrationExport{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []IntegrationExport{}
	for _, e := range ents {
		f := exportFormatOf(e.Name())
		if !e.Type().IsRegular() || f == "" || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, IntegrationExport{Name: e.Name(), Size: info.Size(), CreatedAt: info.ModTime().UTC(), Format: f})
	}
	slices.SortFunc(out, func(a, b IntegrationExport) int { return b.CreatedAt.Compare(a.CreatedAt) })
	return out, nil
}

func (h *integrationsHandler) listExports(ctx *fasthttp.RequestCtx) {
	if h.root == "" {
		sendUnavailable(ctx, h.mode, "integrations (no project folder)")
		return
	}
	xs, err := ListIntegrationExports(h.root)
	if err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "exports: "+err.Error())
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, IntegrationExportList{Exports: xs, Formats: IntegrationExportFormats})
}

// exportPath confines a {name} path parameter to the export folder.
func (h *integrationsHandler) exportPath(ctx *fasthttp.RequestCtx) (string, string, bool) {
	if h.root == "" {
		sendUnavailable(ctx, h.mode, "integrations (no project folder)")
		return "", "", false
	}
	name := pathParam(ctx, "name")
	if !validID(name) || strings.HasPrefix(name, ".") || exportFormatOf(name) == "" {
		sendNotFound(ctx, h.mode, "export", name)
		return "", "", false
	}
	p := filepath.Join(IntegrationsExportDir(h.root), name)
	info, err := os.Lstat(p)
	if err != nil || !info.Mode().IsRegular() {
		sendNotFound(ctx, h.mode, "export", name)
		return "", "", false
	}
	return p, name, true
}

func (h *integrationsHandler) downloadExport(ctx *fasthttp.RequestCtx) {
	p, name, ok := h.exportPath(ctx)
	if !ok {
		return
	}
	body, err := os.ReadFile(p)
	if err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "export: "+err.Error())
		return
	}
	ctype := map[string]string{"otlp": "application/json", "eee": "application/x-ndjson", "jsonl": "application/x-ndjson",
		"csv": "text/csv; charset=utf-8", "harbor": "application/gzip"}[exportFormatOf(name)]
	(&ExportHandler{mode: h.mode}).download(ctx, ctype, name, body)
}

func (h *integrationsHandler) deleteExport(ctx *fasthttp.RequestCtx) {
	p, _, ok := h.exportPath(ctx)
	if !ok {
		return
	}
	if err := os.Remove(p); err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "export: "+err.Error())
		return
	}
	ctx.SetStatusCode(fasthttp.StatusNoContent)
}

// ciWorkflow renders the workflow the CI install PR adds, so a user can read
// it before opening the PR (or copy it by hand).
func (h *integrationsHandler) ciWorkflow(ctx *fasthttp.RequestCtx) {
	q := ctx.QueryArgs()
	mode := string(q.Peek("mode"))
	if mode == "" {
		mode = ci.ModeRecorded
	}
	o := ci.WorkflowOptions{Mode: mode, Source: DefaultCISource, SourceRef: "main"}
	if ref := string(q.Peek("source_ref")); ref != "" {
		if !validID(ref) {
			lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "source_ref must be a branch, tag or commit (letters, digits, '.', '-', '_')")
			return
		}
		o.SourceRef = ref
	}
	if base := string(q.Peek("agent")); base != "" {
		if !validID(base) {
			lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "agent must be an agent id")
			return
		}
		o.Base = "agents/" + base + ".yaml"
	}
	wf, err := ci.GitHubWorkflow(o)
	if err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, err.Error())
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, CIWorkflowPreview{Path: ".github/workflows/heimdall.yml", Mode: mode, Workflow: wf})
}
