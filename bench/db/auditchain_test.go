package db_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/maximhq/bifrost/bench/db"
	"github.com/maximhq/bifrost/bench/db/dbtest"
)

func TestAuditChainLinksAndVerifies(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, d *db.DB) {
		ctx := context.Background()
		// A row from before the chain existed: seq 0, no hash.
		if _, err := d.Exec(ctx, `INSERT INTO audit_log (id, org_id, actor_type, actor_id, action, at, details) VALUES ('aud_legacy', 'org_a', 'user', 'u', 'old.thing', 1, '{}')`); err != nil {
			t.Fatal(err)
		}
		var rows []db.AuditEntry
		for i, action := range []string{"member.add", "token.create", "experiment.start"} {
			e, err := d.AppendAudit(ctx, db.AuditEntry{OrgID: "org_a", ActorType: db.ActorUser, ActorID: "usr_1", Action: action,
				Details: json.RawMessage(`{ "n" : ` + string(rune('1'+i)) + ` }`)})
			if err != nil {
				t.Fatal(err)
			}
			rows = append(rows, e)
		}
		// Another org has its own chain starting at 1.
		other, err := d.AppendAudit(ctx, db.AuditEntry{OrgID: "org_b", ActorType: db.ActorSystem, Action: "org.create"})
		if err != nil || other.Seq != 1 || other.PrevHash != "" {
			t.Fatalf("org_b first row %+v %v", other, err)
		}
		if rows[0].Seq != 1 || rows[1].Seq != 2 || rows[1].PrevHash != rows[0].Hash || rows[2].PrevHash != rows[1].Hash {
			t.Fatalf("chain links %+v", rows)
		}
		if string(rows[0].Details) != `{"n":1}` {
			t.Fatalf("details not compacted: %s", rows[0].Details)
		}
		rep, err := d.VerifyAuditChain(ctx, "org_a")
		if err != nil || !rep.OK || rep.Entries != 4 || rep.Chained != 3 || rep.Legacy != 1 || rep.HeadSeq != 3 || rep.HeadHash != rows[2].Hash {
			t.Fatalf("verify %+v %v", rep, err)
		}
		// The listing carries the chain fields (rows of one millisecond
		// list in id order, so look the head up by id).
		page, err := d.ListAudit(ctx, db.AuditFilter{OrgID: "org_a"})
		if err != nil || len(page.Entries) != 4 {
			t.Fatalf("list %+v %v", page, err)
		}
		for _, e := range page.Entries {
			if e.ID == rows[2].ID && (e.Hash != rows[2].Hash || e.Seq != 3) {
				t.Fatalf("listed head %+v", e)
			}
		}

		// Tampering with a stored row breaks the chain at that row.
		if _, err := d.Exec(ctx, `UPDATE audit_log SET actor_id = 'someone-else' WHERE id = ?`, rows[1].ID); err != nil {
			t.Fatal(err)
		}
		rep, err = d.VerifyAuditChain(ctx, "org_a")
		if err != nil || rep.OK || rep.BrokenAt != 2 || !strings.Contains(rep.Reason, "changed") {
			t.Fatalf("tampered verify %+v %v", rep, err)
		}
		// Removing a row is a gap.
		if _, err := d.Exec(ctx, `DELETE FROM audit_log WHERE id = ?`, rows[1].ID); err != nil {
			t.Fatal(err)
		}
		rep, _ = d.VerifyAuditChain(ctx, "org_a")
		if rep.OK || rep.BrokenAt != 3 || !strings.Contains(rep.Reason, "missing") {
			t.Fatalf("gap verify %+v", rep)
		}
	})
}

func TestAuditChainConcurrentWritersNeverFork(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, d *db.DB) {
		ctx := context.Background()
		var wg sync.WaitGroup
		errs := make(chan error, 20)
		for i := 0; i < 20; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := d.AppendAudit(ctx, db.AuditEntry{OrgID: "org_c", ActorType: db.ActorSystem, Action: "x.y"})
				errs <- err
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		rep, err := d.VerifyAuditChain(ctx, "org_c")
		if err != nil || !rep.OK || rep.Chained != 20 {
			t.Fatalf("verify %+v %v", rep, err)
		}
	})
}

func TestAuditVerifierOffline(t *testing.T) {
	var prev string
	var rows []db.AuditEntry
	for i := int64(1); i <= 3; i++ {
		e := db.AuditEntry{ID: "aud_" + string(rune('a'+i)), OrgID: "org_x", ActorType: "user", Action: "a.b", Seq: i, PrevHash: prev, Details: json.RawMessage(`{}`)}
		e.Hash = db.AuditHash(e)
		prev = e.Hash
		rows = append(rows, e)
	}
	cases := []struct {
		name   string
		mutate func([]db.AuditEntry) []db.AuditEntry
		ok     bool
		broken int64
	}{
		{"intact", func(r []db.AuditEntry) []db.AuditEntry { return r }, true, 0},
		{"reordered", func(r []db.AuditEntry) []db.AuditEntry { return []db.AuditEntry{r[0], r[2], r[1]} }, false, 3},
		{"other org", func(r []db.AuditEntry) []db.AuditEntry { r[1].OrgID = "org_y"; return r }, false, 2},
		{"legacy after chain", func(r []db.AuditEntry) []db.AuditEntry {
			return append(r, db.AuditEntry{ID: "aud_z", OrgID: "org_x"})
		}, false, 4},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var v db.AuditVerifier
			for _, e := range c.mutate(append([]db.AuditEntry(nil), rows...)) {
				v.Add(e)
			}
			r := v.Report()
			if r.OK != c.ok || r.BrokenAt != c.broken {
				t.Fatalf("report %+v", r)
			}
		})
	}
}
