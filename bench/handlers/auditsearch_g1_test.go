package handlers

import (
	"context"
	"strconv"
	"testing"
)

// pagedAudit is an OrgService whose audit log is two orgs' rows, served
// two rows a page.
type pagedAudit struct {
	OrgService
	rows map[string][]AuditRow
}

func (p pagedAudit) Audit(_ context.Context, orgID string, q AuditQuery) ([]AuditRow, string, error) {
	all := p.rows[orgID]
	start, _ := strconv.Atoi(q.Cursor)
	end := min(start+2, len(all))
	next := ""
	if end < len(all) {
		next = strconv.Itoa(end)
	}
	return all[start:end], next, nil
}

func TestAuditSearch(t *testing.T) {
	src := pagedAudit{rows: map[string][]AuditRow{
		"org_a": {{ID: "1", Action: "member.add"}, {ID: "2", Action: "share.create", TargetID: "ev_1"}, {ID: "3", Action: "key.create"},
			{ID: "4", Action: "Share.revoke"}, {ID: "5", Action: "member.remove"}},
		"org_b": {{ID: "9", Action: "share.create"}},
	}}
	rows, next, err := auditSearch(context.Background(), src, "org_a", AuditQuery{Limit: 1}, "share")
	if err != nil || len(rows) != 1 || rows[0].ID != "2" || next != "2" {
		t.Fatalf("first page %v %q %v", rows, next, err)
	}
	rows, next, _ = auditSearch(context.Background(), src, "org_a", AuditQuery{Limit: 1, Cursor: next}, "SHARE")
	if len(rows) != 1 || rows[0].ID != "4" || next != "4" {
		t.Fatalf("second page %v %q", rows, next)
	}
	rows, next, _ = auditSearch(context.Background(), src, "org_a", AuditQuery{Limit: 1, Cursor: next}, "share")
	if len(rows) != 0 || next != "" {
		t.Fatalf("end %v %q", rows, next)
	}
	// Another organization's rows never match.
	rows, _, _ = auditSearch(context.Background(), src, "org_a", AuditQuery{Limit: 10}, "9")
	if len(rows) != 0 {
		t.Fatalf("cross org %v", rows)
	}
}
