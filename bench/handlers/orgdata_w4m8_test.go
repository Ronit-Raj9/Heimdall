package handlers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/tenancy"
	"github.com/valyala/fasthttp"
)

// auditExportFakeW4m8 is an org service with only the audit export
// implemented; the embedded interface is nil and never called.
type auditExportFakeW4m8 struct {
	OrgService
	rows []AuditExportRow
}

func (f *auditExportFakeW4m8) ScheduleOrgDeletion(context.Context, string, string, tenancy.AuditEntry) (OrgDeletionInfo, error) {
	return OrgDeletionInfo{}, nil
}

func (f *auditExportFakeW4m8) EachAuditEntry(_ context.Context, _ string, fn func(AuditExportRow) error) error {
	for _, r := range f.rows {
		if err := fn(r); err != nil {
			return err
		}
	}
	return nil
}

func (f *auditExportFakeW4m8) VerifyAudit(context.Context, string) (AuditVerifyReport, error) {
	return AuditVerifyReport{}, nil
}

func auditExportIDsW4m8(t *testing.T, query string) []string {
	t.Helper()
	fake := &auditExportFakeW4m8{rows: []AuditExportRow{
		{ID: "a1", Seq: 1, ActorType: "user", ActorID: "u_1", Action: "x", TargetType: "suite", TargetID: "alpha"},
		{ID: "a2", Seq: 2, ActorType: "token", ActorID: "t_1", Action: "y", TargetType: "agent", TargetID: "Bravo-Agent"},
		{ID: "a3", Seq: 3, ActorType: "user", ActorID: "u_2", Action: "x", TargetType: "agent", TargetID: "charlie"},
	}}
	h := NewOrgsHandler(schemas.ModeLive, fake, nil)
	var ctx fasthttp.RequestCtx
	ctx.Request.SetRequestURI("/api/orgs/org_1/audit/export?" + query)
	tenancy.SetOrg(&ctx, &tenancy.Org{ID: "org_1", Slug: "acme"})
	h.exportAudit(&ctx)
	if ctx.Response.StatusCode() != fasthttp.StatusOK {
		t.Fatalf("status %d: %s", ctx.Response.StatusCode(), ctx.Response.Body())
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

func TestAuditExportFilters(t *testing.T) {
	cases := []struct {
		query string
		want  string
	}{
		{"", "a1,a2,a3"},
		{"action=x", "a1,a3"},
		{"actor_type=token", "a2"},
		{"q=bravo", "a2"},
		{"q=CHAR", "a3"},
		{"action=x&q=alpha", "a1"},
		{"action=x&actor_type=token", ""},
	}
	for _, c := range cases {
		got := auditExportIDsW4m8(t, c.query)
		joined := ""
		for i, id := range got {
			if i > 0 {
				joined += ","
			}
			joined += id
		}
		if joined != c.want {
			t.Errorf("query %q: got %q, want %q", c.query, joined, c.want)
		}
	}
}
