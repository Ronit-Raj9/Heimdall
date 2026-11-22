package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReadFile(t *testing.T) {
	var gotRef string
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/acme/widgets/contents/.heimdall/agent.yaml", func(w http.ResponseWriter, r *http.Request) {
		gotRef = r.URL.Query().Get("ref")
		if gotRef == "missing" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"Not Found"}`))
			return
		}
		enc := base64.StdEncoding.EncodeToString([]byte("id: bot\n"))
		_ = json.NewEncoder(w).Encode(map[string]any{"type": "file", "size": 8, "encoding": "base64", "content": enc[:4] + "\n" + enc[4:]})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := testConnector(srv.URL)
	b, err := c.ReadFile(context.Background(), "tok", "acme", "widgets", "main", ".heimdall/agent.yaml", 1024)
	if err != nil || string(b) != "id: bot\n" || gotRef != "main" {
		t.Fatalf("%v %q %q", err, b, gotRef)
	}
	_, err = c.ReadFile(context.Background(), "tok", "acme", "widgets", "missing", ".heimdall/agent.yaml", 1024)
	var apiErr *GitHubAPIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusNotFound {
		t.Fatalf("missing: %v", err)
	}
	if _, err := c.ReadFile(context.Background(), "tok", "acme", "widgets", "", ".heimdall/agent.yaml", 4); err == nil {
		t.Fatal("size cap ignored")
	}
	if _, err := c.ReadFile(context.Background(), "tok", "acme", "widgets", "", "../x", 0); err == nil {
		t.Fatal("bad path accepted")
	}
}
