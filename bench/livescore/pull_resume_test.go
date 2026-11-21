package livescore

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// Regression for 8-F-03: rows already scored do not count toward the
// per-pull cap, so a backlog denser than the cap still moves forward.
func TestListRowsSkipsScoredBeforeCap(t *testing.T) {
	at := time.Date(2026, 11, 18, 6, 0, 0, 0, time.UTC)
	rows := make([]Row, MaxRowsPerPull+1)
	for i := range rows {
		rows[i] = Row{ID: fmt.Sprintf("r%05d", i), Timestamp: at, Status: "success", VirtualKeyID: ptr("vk-a")}
	}
	// A minimal log list in a stable order (the shared fake gateway re-sorts
	// every row on every page, too slow for a cap-sized backlog).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		off, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		lim, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		off = min(off, len(rows))
		page := rows[off:min(off+lim, len(rows))]
		_ = json.NewEncoder(w).Encode(map[string]any{"logs": page})
	}))
	t.Cleanup(srv.Close)
	v := &Version{Name: "v1", VirtualKeys: []string{"vk-a"}}
	cl := newClient(srv.URL, "", nil)
	got, trunc, err := cl.listRows(context.Background(), v, at.Add(-time.Minute), at.Add(time.Minute), nil)
	if err != nil || !trunc || len(got) != MaxRowsPerPull {
		t.Fatalf("first page: %d rows, truncated %v, err %v; want the capped page", len(got), trunc, err)
	}
	scored := map[string]bool{}
	for _, r := range got {
		scored[r.ID] = true
	}
	got, trunc, err = cl.listRows(context.Background(), v, at.Add(-time.Minute), at.Add(time.Minute), func(id string) bool { return scored[id] })
	if err != nil || trunc || len(got) != 1 || got[0].ID != rows[MaxRowsPerPull].ID {
		t.Fatalf("second read: %d rows, truncated %v, err %v; want only the unread row", len(got), trunc, err)
	}
}

// QC2-26-02: rows the selector rejects (same model name, other provider)
// do not count toward the cap, so the eligible row after them is reached.
func TestListRowsDropsRejectedBeforeCap(t *testing.T) {
	at := time.Date(2026, 11, 18, 6, 0, 0, 0, time.UTC)
	rows := make([]Row, MaxRowsPerPull+1)
	for i := range rows {
		rows[i] = Row{ID: fmt.Sprintf("r%05d", i), Timestamp: at, Status: "success", Provider: "other", Model: "m1"}
	}
	rows[MaxRowsPerPull].Provider = "openai"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		off, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		lim, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		off = min(off, len(rows))
		_ = json.NewEncoder(w).Encode(map[string]any{"logs": rows[off:min(off+lim, len(rows))]})
	}))
	t.Cleanup(srv.Close)
	v := &Version{Name: "v1", Models: []string{"openai/m1"}}
	got, trunc, err := newClient(srv.URL, "", nil).listRows(context.Background(), v, at.Add(-time.Minute), at.Add(time.Minute), nil)
	if err != nil || trunc || len(got) != 1 || got[0].ID != rows[MaxRowsPerPull].ID {
		t.Fatalf("%d rows, truncated %v, err %v; want only the eligible row", len(got), trunc, err)
	}
}

// Regression for 8-F-04: a pull that appended its sessions but never saved
// its state must not make the next pull append the same sessions again.
func TestPullAfterLostCheckpointAppendsNoDuplicates(t *testing.T) {
	g, srv := newFakeGateway(t, testToken)
	start := time.Date(2026, 11, 18, 6, 0, 0, 0, time.UTC)
	fill(g, start, 5, func(int) bool { return false })
	now := start.Add(2 * time.Hour)
	s, _, _ := newTestScorer(t, testConfig(srv.URL), &now)
	if _, err := s.Pull(context.Background()); err != nil {
		t.Fatalf("pull: %v", err)
	}
	first, err := s.Store.Sessions()
	if err != nil || len(first) == 0 {
		t.Fatalf("sessions after first pull: %d, %v", len(first), err)
	}
	// The crash: the state write never happened.
	if err := os.Remove(filepath.Join(s.Store.dir, StateFile)); err != nil {
		t.Fatal(err)
	}
	res, err := s.Pull(context.Background())
	if err != nil {
		t.Fatalf("retry pull: %v", err)
	}
	again, err := s.Store.Sessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != len(first) || res.Sessions["v1"]+res.Sessions["v2"] != 0 {
		t.Fatalf("retry scored %v; sessions %d, want %d", res.Sessions, len(again), len(first))
	}
	st, _ := s.Store.State()
	if st.Seq != first[len(first)-1].Seq {
		t.Errorf("state seq %d, want the highest recorded %d", st.Seq, first[len(first)-1].Seq)
	}
	// A file already holding a duplicate line is read once per id.
	if err := s.Store.appendSessions(first[:1]); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Store.Sessions(); len(got) != len(first) {
		t.Errorf("duplicate line read back: %d sessions, want %d", len(got), len(first))
	}
}
