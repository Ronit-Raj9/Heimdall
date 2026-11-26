package bifrost

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// fakePromptRepo is Bifrost's Prompt Repository API as v2.5.1 answers it.
type fakePromptRepo struct {
	mu          sync.Mutex
	prompts     map[string]string // id -> name
	bodies      []map[string]any
	failVersion bool
	deleted     []string
	gone        map[string]bool // listed, but already deleted: DELETE answers 404
}

func (f *fakePromptRepo) gateway(t *testing.T) *Gateway {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/prompt-repo/prompts", func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		f.mu.Lock()
		defer f.mu.Unlock()
		f.bodies = append(f.bodies, b)
		id := "pr-1"
		f.prompts[id], _ = b["name"].(string)
		_ = json.NewEncoder(w).Encode(map[string]any{"prompt": map[string]any{"id": id, "name": b["name"]}})
	})
	mux.HandleFunc("POST /api/prompt-repo/prompts/{id}/versions", func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		f.mu.Lock()
		defer f.mu.Unlock()
		f.bodies = append(f.bodies, b)
		if f.failVersion {
			http.Error(w, `{"error":"bad"}`, http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"version": map[string]any{"version_number": 1}})
	})
	mux.HandleFunc("GET /api/prompt-repo/prompts", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		var list []map[string]any
		for id, name := range f.prompts {
			list = append(list, map[string]any{"id": id, "name": name})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"prompts": list})
	})
	mux.HandleFunc("DELETE /api/prompt-repo/prompts/{id}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		id := r.PathValue("id")
		f.deleted = append(f.deleted, id)
		if _, ok := f.prompts[id]; !ok || f.gone[id] {
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
			return
		}
		delete(f.prompts, id)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	g, err := New(testConfig(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func runPrompt() schemas.BifrostPrompt {
	return schemas.BifrostPrompt{Name: "support", Version: 3, Messages: []map[string]any{{"role": "system", "content": "Be brief."}}, ModelParams: map[string]any{"temperature": 0.2}}
}

func TestCreateRunPromptBodies(t *testing.T) {
	f := &fakePromptRepo{prompts: map[string]string{}}
	id, v, err := f.gateway(t).CreateRunPrompt(context.Background(), "r_1", runPrompt())
	if err != nil {
		t.Fatal(err)
	}
	if id != "pr-1" || v != 1 {
		t.Fatalf("id %q version %d", id, v)
	}
	if len(f.bodies) != 2 || f.bodies[0]["name"] != "hm-prompt-r_1" {
		t.Fatalf("bodies = %v", f.bodies)
	}
	b := f.bodies[1]
	if b["commit_message"] != "Heimdall run r_1" || b["provider"] != "" || b["model"] != "" {
		t.Fatalf("version body = %v", b)
	}
	if msgs, _ := b["messages"].([]any); len(msgs) != 1 {
		t.Fatalf("messages = %v", b["messages"])
	}
	if mp, _ := b["model_params"].(map[string]any); mp["temperature"] != 0.2 {
		t.Fatalf("model_params = %v", b["model_params"])
	}
}

func TestCreateRunPromptDeletesOnVersionFailure(t *testing.T) {
	f := &fakePromptRepo{prompts: map[string]string{}, failVersion: true}
	if _, _, err := f.gateway(t).CreateRunPrompt(context.Background(), "r_1", runPrompt()); err == nil {
		t.Fatal("want an error")
	}
	if len(f.deleted) != 1 || f.deleted[0] != "pr-1" || len(f.prompts) != 0 {
		t.Fatalf("deleted %v left %v", f.deleted, f.prompts)
	}
}

func TestRemoveRunPromptIgnores404(t *testing.T) {
	f := &fakePromptRepo{prompts: map[string]string{"pr-1": "hm-prompt-r_1", "pr-2": "hm-prompt-r_2", "own": "support", "pr-9": "hm-prompt-r_1"}, gone: map[string]bool{"pr-9": true}}
	g := f.gateway(t)
	if err := g.RemoveRunPrompt(context.Background(), "r_1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.prompts["pr-1"]; ok || len(f.prompts) != 3 {
		t.Fatalf("left %v", f.prompts)
	}
	if err := g.RemoveRunPrompt(context.Background(), "r_1"); err != nil {
		t.Fatalf("second remove: %v", err)
	}
}
