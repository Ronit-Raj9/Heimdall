package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/promptlogs"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

type promptLogSettings struct {
	mu sync.Mutex
	m  map[string]string
}

func (s *promptLogSettings) GetSetting(_ context.Context, org, key string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[org+"/"+key]
	return v, ok, nil
}

func (s *promptLogSettings) SetSetting(_ context.Context, org, key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[org+"/"+key] = value
	return nil
}

// promptLogGateway is a gateway log store holding rows of two orgs.
func promptLogGateway(t *testing.T) (*httptest.Server, *[]string) {
	rows := map[string]string{"l1": "o1", "l2": "o1", "l3": "o2"}
	var mu sync.Mutex
	var deleted []string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/logs", func(w http.ResponseWriter, r *http.Request) {
		org := r.URL.Query().Get("metadata_" + schemas.PromptLogOrgMetadataKey)
		mu.Lock()
		logs := []map[string]any{}
		for id, o := range rows {
			if o == org {
				logs = append(logs, map[string]any{"id": id, "metadata": map[string]any{schemas.PromptLogOrgMetadataKey: o}})
			}
		}
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"logs": logs, "pagination": map[string]any{"total_count": len(logs)}})
	})
	mux.HandleFunc("DELETE /api/logs", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			IDs []string `json:"ids"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		mu.Lock()
		for _, id := range in.IDs {
			delete(rows, id)
			deleted = append(deleted, id)
		}
		mu.Unlock()
		_, _ = w.Write([]byte(`{}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &deleted
}

func promptLogsRouter(org string) fasthttp.RequestHandler {
	env := OrgEnv{Mode: schemas.ModeLive, Services: Services{OrgID: org}}
	h := &promptLogsHandler{env: env}
	r := router.New()
	r.GET(PromptLoggingPath, env.R(h.getPolicy))
	r.PUT(PromptLoggingPath, env.W(h.putPolicy))
	r.GET(PromptLogsPath, env.R(h.list))
	r.DELETE(PromptLogsPath, env.W(h.purge))
	return r.Handler
}

func TestPromptLogsPerOrg(t *testing.T) {
	srv, deleted := promptLogGateway(t)
	st := &promptLogSettings{m: map[string]string{}}
	SetPromptLogs(st, promptlogs.NewClient(srv.URL, "tok"))
	t.Cleanup(func() { SetPromptLogs(nil, nil) })
	o1, o2 := promptLogsRouter("o1"), promptLogsRouter("o2")

	code, body := secretsCall(o1, "GET", PromptLoggingPath, "")
	var got PromptLogging
	_ = json.Unmarshal([]byte(body), &got)
	if code != 200 || !got.KeepContent || got.RetentionDays != schemas.PromptLogRetentionDefault || !got.GatewayLogs || got.MaxRetentionDays != 30 {
		t.Fatalf("default policy: %d %s", code, body)
	}
	if code, _ := secretsCall(o1, "PUT", PromptLoggingPath, `{"keep_content":false,"retention_days":0}`); code != 400 {
		t.Errorf("retention 0: %d", code)
	}
	if code, _ := secretsCall(o1, "PUT", PromptLoggingPath, `{"keep_content":false,"retention_days":3,"extra":1}`); code != 400 {
		t.Errorf("unknown field: %d", code)
	}
	if code, body := secretsCall(o1, "PUT", PromptLoggingPath, `{"keep_content":false,"retention_days":3}`); code != 200 {
		t.Fatalf("save: %d %s", code, body)
	}
	if p := OrgPromptLogPolicy(context.Background(), "o1"); p.KeepContent || p.RetentionDays != 3 {
		t.Errorf("o1 policy = %+v", p)
	}
	if p := OrgPromptLogPolicy(context.Background(), "o2"); p != schemas.DefaultPromptLogPolicy() {
		t.Errorf("o2 sees o1's policy: %+v", p)
	}

	code, body = secretsCall(o1, "GET", PromptLogsPath+"?limit=10", "")
	if code != 200 || !strings.Contains(body, `"l1"`) || !strings.Contains(body, `"l2"`) || strings.Contains(body, `"l3"`) {
		t.Fatalf("o1 list: %d %s", code, body)
	}
	if code, _ := secretsCall(o1, "DELETE", PromptLogsPath+"?before=yesterday", ""); code != 400 {
		t.Errorf("bad before: %d", code)
	}
	code, body = secretsCall(o1, "DELETE", PromptLogsPath, "")
	if code != 200 || !strings.Contains(body, `"deleted":2`) {
		t.Fatalf("o1 delete: %d %s", code, body)
	}
	for _, id := range *deleted {
		if id == "l3" {
			t.Fatalf("o1's delete removed o2's row: %v", *deleted)
		}
	}
	code, body = secretsCall(o2, "GET", PromptLogsPath, "")
	if code != 200 || !strings.Contains(body, `"l3"`) {
		t.Errorf("o2 list after o1 delete: %d %s", code, body)
	}
}

func TestPromptLogsWithoutGateway(t *testing.T) {
	SetPromptLogs(nil, nil)
	h := promptLogsRouter("o1")
	if code, _ := secretsCall(h, "GET", PromptLogsPath, ""); code != 503 {
		t.Errorf("list without a gateway: %d", code)
	}
	if code, _ := secretsCall(h, "DELETE", PromptLogsPath, ""); code != 503 {
		t.Errorf("delete without a gateway: %d", code)
	}
	if code, _ := secretsCall(h, "PUT", PromptLoggingPath, `{"keep_content":true,"retention_days":5}`); code != 503 {
		t.Errorf("save without a store: %d", code)
	}
	code, body := secretsCall(h, "GET", PromptLoggingPath, "")
	if code != 200 || !strings.Contains(body, `"gateway_logs":false`) {
		t.Errorf("policy without a gateway: %d %s", code, body)
	}
}
