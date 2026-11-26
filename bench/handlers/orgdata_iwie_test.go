package handlers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/tenancy"
	"github.com/valyala/fasthttp"
)

// auditOrgFakeIwie keeps the audit rows of two organizations and records
// which organization each export read.
type auditOrgFakeIwie struct {
	auditExportFakeW4m8
	byOrg map[string][]AuditExportRow
	asked []string
}

func (f *auditOrgFakeIwie) EachAuditEntry(_ context.Context, orgID string, fn func(AuditExportRow) error) error {
	f.asked = append(f.asked, orgID)
	for _, r := range f.byOrg[orgID] {
		if err := fn(r); err != nil {
			return err
		}
	}
	return nil
}

// Group E security review (I20): the audit export filters only narrow the
// caller's own organization's log. No query names another organization's
// rows, and every filtered answer is a subset of the unfiltered one.
func TestAuditExportFiltersNeverWiden(t *testing.T) {
	fake := &auditOrgFakeIwie{byOrg: map[string][]AuditExportRow{
		"org_1": {
			{ID: "a1", ActorType: "user", ActorID: "u_1", Action: "x", TargetType: "suite", TargetID: "alpha"},
			{ID: "a2", ActorType: "token", ActorID: "t_1", Action: "y", TargetType: "agent", TargetID: "bravo"},
		},
		"org_2": {
			{ID: "b1", ActorType: "user", ActorID: "u_9", Action: "x", TargetType: "suite", TargetID: "org_1"},
		},
	}}
	h := NewOrgsHandler(schemas.ModeLive, fake, nil)
	export := func(query string) []string {
		t.Helper()
		var ctx fasthttp.RequestCtx
		ctx.Request.SetRequestURI("/api/orgs/org_1/audit/export?" + query)
		tenancy.SetOrg(&ctx, &tenancy.Org{ID: "org_1", Slug: "acme"})
		h.exportAudit(&ctx)
		if ctx.Response.StatusCode() != fasthttp.StatusOK {
			t.Fatalf("%q: status %d: %s", query, ctx.Response.StatusCode(), ctx.Response.Body())
		}
		var ids []string
		sc := bufio.NewScanner(bytes.NewReader(ctx.Response.Body()))
		for sc.Scan() {
			var r AuditExportRow
			if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
				t.Fatalf("line %q: %v", sc.Text(), err)
			}
			ids = append(ids, r.ID)
		}
		return ids
	}
	all := export("")
	for _, q := range []string{"", "action=x", "q=org_1", "q=u_9", "actor_type=user", "org=org_2", "org_id=org_2", "heimdall_org=org_2", "action=x&org=org_2&q=suite"} {
		got := export(q)
		for _, id := range got {
			if !slices.Contains(all, id) {
				t.Errorf("%q gave row %s, not in the unfiltered export %v", q, id, all)
			}
		}
	}
	for _, org := range fake.asked {
		if org != "org_1" {
			t.Fatalf("an export read the log of %s", org)
		}
	}
}
