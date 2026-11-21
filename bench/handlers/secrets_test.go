package handlers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

func secretsRouter(t *testing.T, ks keystore.Store) fasthttp.RequestHandler {
	t.Helper()
	env := OrgEnv{Mode: schemas.ModeLive, Services: Services{OrgID: "o1", Keys: ks}}
	h := &secretsHandler{env: env}
	r := router.New()
	r.GET(APIPrefix+"/secrets", env.R(h.list))
	r.POST(APIPrefix+"/secrets", env.W(h.create))
	r.PUT(APIPrefix+"/secrets/{id}", env.W(h.rotate))
	r.DELETE(APIPrefix+"/secrets/{id}", env.W(h.remove))
	return r.Handler
}

func secretsCall(h fasthttp.RequestHandler, method, path, body string) (int, string) {
	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.SetMethod(method)
	ctx.Request.SetRequestURI(path)
	if body != "" {
		ctx.Request.SetBodyString(body)
	}
	h(ctx)
	return ctx.Response.StatusCode(), string(ctx.Response.Body())
}

func TestSecretsCRUDNeverReturnsValue(t *testing.T) {
	ks, err := keystore.Open(nil, make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	h := secretsRouter(t, ks)
	const val = "tvly-super-secret-9876"
	st, body := secretsCall(h, "POST", APIPrefix+"/secrets", `{"name":"TAVILY_API_KEY","value":"`+val+`"}`)
	if st != fasthttp.StatusCreated {
		t.Fatalf("create: %d %s", st, body)
	}
	if strings.Contains(body, val) || strings.Contains(body, "9876") {
		t.Fatalf("create echoed the value: %s", body)
	}
	var s Secret
	_ = json.Unmarshal([]byte(body), &s)
	if s.ID == "" || s.Name != "TAVILY_API_KEY" {
		t.Fatalf("created = %+v", s)
	}
	if st, _ := secretsCall(h, "POST", APIPrefix+"/secrets", `{"name":"TAVILY_API_KEY","value":"x"}`); st != fasthttp.StatusConflict {
		t.Errorf("duplicate name: %d", st)
	}
	for _, bad := range []string{`{"name":"lower","value":"x"}`, `{"name":"HEIMDALL_MASTER_KEY","value":"x"}`, `{"name":"OK_NAME","value":""}`, `not json`} {
		if st, _ := secretsCall(h, "POST", APIPrefix+"/secrets", bad); st != fasthttp.StatusBadRequest {
			t.Errorf("%s: %d", bad, st)
		}
	}
	st, body = secretsCall(h, "GET", APIPrefix+"/secrets", "")
	if st != 200 || strings.Contains(body, val) || !strings.Contains(body, "TAVILY_API_KEY") {
		t.Fatalf("list: %d %s", st, body)
	}
	if st, body = secretsCall(h, "PUT", APIPrefix+"/secrets/"+s.ID, `{"value":"rotated-1234"}`); st != 200 || strings.Contains(body, "rotated") {
		t.Fatalf("rotate: %d %s", st, body)
	}
	if v, _ := ks.Reveal(context.Background(), "o1", s.ID); v != "rotated-1234" {
		t.Errorf("stored value = %q", v)
	}
	// A provider key is not reachable through the secrets area.
	pk, _ := ks.Create(context.Background(), "o1", keystore.KindProvider, keystore.Put{Provider: "openai", Value: "sk-x"}, "a")
	if st, _ := secretsCall(h, "DELETE", APIPrefix+"/secrets/"+pk.ID, ""); st != fasthttp.StatusNotFound {
		t.Errorf("delete provider key via secrets: %d", st)
	}
	if st, _ := secretsCall(h, "DELETE", APIPrefix+"/secrets/"+s.ID, ""); st != 200 {
		t.Errorf("delete: %d", st)
	}
	if st, _ := secretsCall(h, "DELETE", APIPrefix+"/secrets/"+s.ID, ""); st != fasthttp.StatusNotFound {
		t.Errorf("delete again: %d", st)
	}
}

func TestSecretsNoStore503(t *testing.T) {
	h := secretsRouter(t, nil)
	if st, body := secretsCall(h, "GET", APIPrefix+"/secrets", ""); st != fasthttp.StatusServiceUnavailable || !strings.Contains(body, "master key") {
		t.Errorf("no store: %d %s", st, body)
	}
}
