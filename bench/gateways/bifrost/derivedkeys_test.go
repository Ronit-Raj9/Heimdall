package bifrost

import (
	"context"
	"testing"
)

// TestRevokeStaleKeepsDerivedKeys (W3 customer support): a service's or a
// judge's key, named after its run plus one letter and a 6 character suffix,
// stays while its run is live and goes with it.
func TestRevokeStaleKeepsDerivedKeys(t *testing.T) {
	f := newFakeBifrost(t)
	f.vks = []map[string]any{
		{"id": "a", "name": "r_active01"},
		{"id": "b", "name": "r_active01sab12cd"}, // the live run's service key
		{"id": "c", "name": "r_active01jzz9900"}, // its judge's key
		{"id": "d", "name": "r_stale001sab12cd"}, // a service key of a gone run
	}
	g := newTestGateway(t, f)
	n, err := g.RevokeStale(context.Background(), map[string]bool{"r_active01": true})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || len(f.vks) != 3 {
		t.Fatalf("n = %d, left %v", n, f.vks)
	}
	for _, vk := range f.vks {
		if vk["name"] == "r_stale001sab12cd" {
			t.Fatal("stale service key kept")
		}
	}
	if ownerRun("r_ab") != "" || ownerRun("r_active01sab12cd") != "r_active01" {
		t.Fatal("ownerRun")
	}
}
