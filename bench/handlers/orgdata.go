package handlers

// Data lifecycle of an organization (lane X16, audits N10 and N11): delete
// the whole organization (owner, signed in, typed slug; the data goes in a
// background job and the audit log stays), and the hash-chained audit log's
// export (JSONL or CSV) and integrity check. Platform routes: the
// dispatcher has resolved {org} and checked tenancy/policy_dataops.go.

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/tenancy"
	"github.com/valyala/fasthttp"
)

// OrgDataService is optionally implemented by the OrgService: org deletion
// and the audit chain. Without it the routes answer 503.
type OrgDataService interface {
	// ScheduleOrgDeletion records the deletion, removes every way into the
	// org in the same transaction (with audit as its audit row), and starts
	// the background removal of its data. ErrOrgDeleting when a deletion
	// is already recorded.
	ScheduleOrgDeletion(ctx context.Context, orgID, requestedBy string, audit tenancy.AuditEntry) (OrgDeletionInfo, error)
	// EachAuditEntry calls fn for every audit row of the org in chain order
	// (rows from before the chain first), stopping at fn's first error.
	EachAuditEntry(ctx context.Context, orgID string, fn func(AuditExportRow) error) error
	// VerifyAudit checks every link of the org's audit chain.
	VerifyAudit(ctx context.Context, orgID string) (AuditVerifyReport, error)
}

// ErrOrgDeleting answers a second deletion request (409).
var ErrOrgDeleting = errors.New("this organization is already being deleted")

// OrgDeletionInfo is POST /orgs/{org}/delete (202).
type OrgDeletionInfo struct {
	OrgID       string     `json:"org_id"`
	Slug        string     `json:"slug"`
	Status      string     `json:"status"` // scheduled | running | done | failed
	RequestedBy string     `json:"requested_by,omitempty"`
	RequestedAt time.Time  `json:"requested_at"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
	Error       string     `json:"error,omitempty"`
}

// DeleteOrgRequest is the body of POST /orgs/{org}/delete: the org's slug,
// typed by the owner.
type DeleteOrgRequest struct {
	Confirm string `json:"confirm"`
}

// AuditExportRow is one line of the audit export. Its JSON is the stored
// row, so `bifrost-bench org audit-verify` recomputes every hash from it.
type AuditExportRow struct {
	ID         string          `json:"id"`
	OrgID      string          `json:"org_id"`
	Seq        int64           `json:"seq"`
	PrevHash   string          `json:"prev_hash"`
	Hash       string          `json:"hash"`
	ActorType  string          `json:"actor_type"`
	ActorID    string          `json:"actor_id"`
	Action     string          `json:"action"`
	TargetType string          `json:"target_type"`
	TargetID   string          `json:"target_id"`
	IP         string          `json:"ip"`
	At         time.Time       `json:"at"`
	Details    json.RawMessage `json:"details"`
}

// AuditCSVHeader is the header row of the CSV export.
var AuditCSVHeader = []string{"seq", "id", "org_id", "at", "actor_type", "actor_id", "action", "target_type", "target_id", "ip", "details", "prev_hash", "hash"}

// CSV is the row as AuditCSVHeader columns; at is RFC 3339 with
// milliseconds, details compact JSON.
func (r AuditExportRow) CSV() []string {
	details := string(r.Details)
	if details == "" {
		details = "{}"
	}
	return []string{strconv.FormatInt(r.Seq, 10), r.ID, r.OrgID, r.At.UTC().Format("2006-01-02T15:04:05.000Z07:00"), r.ActorType, r.ActorID,
		r.Action, r.TargetType, r.TargetID, r.IP, details, r.PrevHash, r.Hash}
}

// AuditVerifyReport is GET /orgs/{org}/audit/verify.
type AuditVerifyReport struct {
	OK       bool   `json:"ok"`
	Entries  int    `json:"entries"`
	Chained  int    `json:"chained"`
	Legacy   int    `json:"legacy"` // rows written before the chain existed
	HeadSeq  int64  `json:"head_seq"`
	HeadHash string `json:"head_hash,omitempty"`
	BrokenAt int64  `json:"broken_at,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// registerDataRoutes adds the org data routes to the org handler's routes.
func (h *OrgsHandler) registerDataRoutes(r *router.Router, p string, middlewares []lib.Middleware) {
	r.POST(p+"/{org}/delete", lib.ChainMiddlewares(h.deleteOrg, middlewares...))
	r.GET(p+"/{org}/audit/export", lib.ChainMiddlewares(h.exportAudit, middlewares...))
	r.GET(p+"/{org}/audit/verify", lib.ChainMiddlewares(h.verifyAudit, middlewares...))
}

// dataService is the optional org data service, or 503.
func (h *OrgsHandler) dataService(ctx *fasthttp.RequestCtx) (OrgDataService, *tenancy.Org, bool) {
	o, ok := h.target(ctx)
	if !ok {
		return nil, nil, false
	}
	svc, ok := h.orgs.(OrgDataService)
	if !ok {
		sendUnavailable(ctx, h.mode, "organization data service")
		return nil, nil, false
	}
	return svc, o, true
}

// deleteOrg handles POST /orgs/{org}/delete: 202 once the deletion is
// recorded and nobody can reach the org any more; the data goes in the
// background.
func (h *OrgsHandler) deleteOrg(ctx *fasthttp.RequestCtx) {
	svc, o, ok := h.dataService(ctx)
	if !ok {
		return
	}
	p := auth.PrincipalFrom(ctx)
	if p.Kind != auth.KindUser || p.UserID == "" || p.Role != auth.RoleOwner {
		lib.SendError(ctx, h.mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden, "only an owner, signed in, can delete an organization; API tokens cannot")
		return
	}
	var req DeleteOrgRequest
	if !decodeStrict(ctx, h.mode, &req, "delete organization") {
		return
	}
	if req.Confirm != o.Slug {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "type the organization's slug ("+o.Slug+") to confirm the deletion")
		return
	}
	wctx, cancel := workContext(ctx, readTimeout)
	defer cancel()
	e := tenancy.EntryFor(ctx, o.ID, "org.delete.requested", "org", o.ID, map[string]any{"slug": o.Slug, "name": o.Name})
	info, err := svc.ScheduleOrgDeletion(wctx, o.ID, p.UserID, e)
	if errors.Is(err, ErrOrgDeleting) {
		lib.SendError(ctx, h.mode, fasthttp.StatusConflict, lib.ErrTypeConflict, err.Error())
		return
	}
	if err != nil {
		h.sendErr(ctx, "delete organization", err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusAccepted, info)
}

// exportAudit handles GET /orgs/{org}/audit/export?format=jsonl|csv: the
// whole log in chain order as a download.
func (h *OrgsHandler) exportAudit(ctx *fasthttp.RequestCtx) {
	format := string(ctx.QueryArgs().Peek("format"))
	if format == "" {
		format = "jsonl"
	}
	if format != "jsonl" && format != "csv" {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "format: jsonl or csv")
		return
	}
	svc, o, ok := h.dataService(ctx)
	if !ok {
		return
	}
	wctx, cancel := workContext(ctx, 2*time.Minute)
	defer cancel()
	var buf bytes.Buffer
	var write func(AuditExportRow) error
	cw := csv.NewWriter(&buf)
	if format == "csv" {
		if err := cw.Write(AuditCSVHeader); err != nil {
			h.sendErr(ctx, "audit export", err)
			return
		}
		write = func(r AuditExportRow) error { return cw.Write(r.CSV()) }
	} else {
		enc := json.NewEncoder(&buf)
		write = func(r AuditExportRow) error {
			if len(r.Details) == 0 {
				r.Details = json.RawMessage("{}")
			}
			return enc.Encode(r)
		}
	}
	if err := svc.EachAuditEntry(wctx, o.ID, write); err != nil {
		h.sendErr(ctx, "audit export", err)
		return
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		h.sendErr(ctx, "audit export", err)
		return
	}
	name := "audit-" + o.Slug + "-" + time.Now().UTC().Format("20060102T150405Z") + "." + format
	ctype := "application/x-ndjson"
	if format == "csv" {
		ctype = "text/csv; charset=utf-8"
	}
	ctx.Response.Header.Set("Content-Disposition", `attachment; filename="`+name+`"`)
	ctx.Response.Header.Set("Cache-Control", "no-store")
	ctx.SetContentType(ctype)
	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.SetBody(buf.Bytes())
}

// verifyAudit handles GET /orgs/{org}/audit/verify.
func (h *OrgsHandler) verifyAudit(ctx *fasthttp.RequestCtx) {
	svc, o, ok := h.dataService(ctx)
	if !ok {
		return
	}
	wctx, cancel := workContext(ctx, 2*time.Minute)
	defer cancel()
	rep, err := svc.VerifyAudit(wctx, o.ID)
	if err != nil {
		h.sendErr(ctx, "audit verify", err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, rep)
}
