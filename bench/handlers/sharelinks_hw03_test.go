package handlers

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/sharelinks"
)

// memShareIndex records PutShareToken calls (fix wave 3, N293).
type memShareIndex struct {
	mu   sync.Mutex
	rows map[string][2]string // token -> org id, share id
}

func (m *memShareIndex) PutShareToken(_ context.Context, token, orgID, shareID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.rows == nil {
		m.rows = map[string][2]string{}
	}
	m.rows[token] = [2]string{orgID, shareID}
	return nil
}

// TestShareOldTokenStillOpensAndReturnsCanonical (N293): with a share index
// (hosted), a new link's token is the opaque s_ form and is written to the
// index; a link opened with its old org.id.signature token still opens and
// answers canonical_token (indexed too), the s_ form answers none; revoking
// makes the s_ form 410.
func TestShareOldTokenStillOpensAndReturnsCanonical(t *testing.T) {
	r, root := shareRouter(t)
	ix := &memShareIndex{}
	RegisterShareIndex(filepath.Dir(root), ix)

	res := shareCall(r, "POST", APIPrefix+"/shares", map[string]any{"kind": "evaluation", "target": "ev_1"})
	if res.StatusCode() != 201 {
		t.Fatalf("create: %d %s", res.StatusCode(), res.Body())
	}
	var sv ShareView
	if err := json.Unmarshal(res.Body(), &sv); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(sv.Token, "s_") || strings.Contains(sv.Token, "local") || sv.Path != "/s/"+sv.Token {
		t.Fatalf("hosted link token %q path %q", sv.Token, sv.Path)
	}
	if got := ix.rows[sv.Token]; got[1] != sv.ID {
		t.Fatalf("index rows %+v", ix.rows)
	}

	var item struct {
		SharedItem
		Canonical string `json:"canonical_token"`
	}
	res = shareCall(r, "GET", APIPrefix+"/shared/"+sv.Token, nil)
	if res.StatusCode() != 200 || json.Unmarshal(res.Body(), &item) != nil || item.Canonical != "" {
		t.Fatalf("open v2: %d %s", res.StatusCode(), res.Body())
	}

	old, err := sharelinks.Open(root).Token(auth.LocalOrgID, sv.ID)
	if err != nil {
		t.Fatal(err)
	}
	ix.rows = nil
	res = shareCall(r, "GET", APIPrefix+"/shared/"+old, nil)
	item.Canonical = ""
	if res.StatusCode() != 200 || json.Unmarshal(res.Body(), &item) != nil || item.Canonical != sv.Token {
		t.Fatalf("open old: %d %s", res.StatusCode(), res.Body())
	}
	if got := ix.rows[sv.Token]; got[1] != sv.ID {
		t.Fatalf("old link visit did not index its new token: %+v", ix.rows)
	}

	if res := shareCall(r, "POST", APIPrefix+"/shares/"+sv.ID+"/revoke", nil); res.StatusCode() != 200 {
		t.Fatalf("revoke: %d", res.StatusCode())
	}
	if res := shareCall(r, "GET", APIPrefix+"/shared/"+sv.Token, nil); res.StatusCode() != 410 {
		t.Fatalf("revoked v2: %d %s", res.StatusCode(), res.Body())
	}
	if res := shareCall(r, "GET", APIPrefix+"/shared/s_"+strings.Repeat("A", 43), nil); res.StatusCode() != 404 {
		t.Fatalf("unknown v2: %d", res.StatusCode())
	}
}

// TestShareLocalKeepsOldToken: without an index (local mode) links keep the
// org.id.signature form and no canonical token is offered.
func TestShareLocalKeepsOldToken(t *testing.T) {
	r, _ := shareRouter(t)
	res := shareCall(r, "POST", APIPrefix+"/shares", map[string]any{"kind": "evaluation", "target": "ev_1"})
	var sv ShareView
	if err := json.Unmarshal(res.Body(), &sv); err != nil || strings.HasPrefix(sv.Token, "s_") || strings.Count(sv.Token, ".") != 2 {
		t.Fatalf("local token %q %v", sv.Token, err)
	}
	res = shareCall(r, "GET", APIPrefix+"/shared/"+sv.Token, nil)
	if res.StatusCode() != 200 || strings.Contains(string(res.Body()), "canonical_token") {
		t.Fatalf("local open: %d %s", res.StatusCode(), res.Body())
	}
}
