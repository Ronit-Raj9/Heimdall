package handlers

import (
	"context"
	"strings"
)

// auditSearchPages bounds how many store pages one ?q= request reads.
const auditSearchPages = 20

// auditSearch is GET /orgs/{org}/audit?q=: the rows whose actor, action,
// target, address or details contain text (any case), newest first. It
// reads the log page by page (q.Limit rows each) from q's cursor and stops
// after the first page that brings the matches to q.Limit, so a page of
// matches can hold up to two pages' worth of rows; next continues the
// search where it stopped, and is empty at the end of the log.
func auditSearch(ctx context.Context, orgs OrgService, orgID string, q AuditQuery, text string) ([]AuditRow, string, error) {
	needle := strings.ToLower(text)
	var out []AuditRow
	for range auditSearchPages {
		rows, next, err := orgs.Audit(ctx, orgID, q)
		if err != nil {
			return nil, "", err
		}
		for _, r := range rows {
			if auditRowMatches(r, needle) {
				out = append(out, r)
			}
		}
		if next == "" || len(out) >= q.Limit {
			return out, next, nil
		}
		q.Cursor = next
	}
	return out, q.Cursor, nil
}

func auditRowMatches(r AuditRow, needle string) bool {
	for _, f := range []string{r.ActorType, r.ActorID, r.Action, r.TargetType, r.TargetID, r.IP, string(r.Details)} {
		if strings.Contains(strings.ToLower(f), needle) {
			return true
		}
	}
	return false
}
