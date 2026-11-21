package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

func modelsRegister(s Services) func(*router.Router) {
	return func(r *router.Router) {
		env := OrgEnv{Mode: schemas.ModeLive, Services: s}
		orgAreas["models"](env, r)
	}
}

const secretValue = "sk-test-SECRETSECRET-9876"

func TestModelKeysCRUDNeverEchoesValue(t *testing.T) {
	reg := modelsRegister(Services{OrgID: "org_a", Keys: keystore.NewMemory()})
	resp := jobsRequest(t, reg, nil, "POST", APIPrefix+"/models/keys", `{"provider":"openai","label":"main","value":"`+secretValue+`","budget_usd":12.5}`)
	if resp.StatusCode() != fasthttp.StatusCreated {
		t.Fatalf("create: %d %s", resp.StatusCode(), resp.Body())
	}
	if strings.Contains(string(resp.Body()), "SECRET") {
		t.Fatalf("create echoed the value: %s", resp.Body())
	}
	var created ModelKeyResponse
	if err := json.Unmarshal(resp.Body(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Key.Last4 != "9876" || created.Key.BudgetUSD != 12.5 || created.Key.Status != "untested" {
		t.Fatalf("bad key %+v", created.Key)
	}
	id := created.Key.ID
	for _, uri := range []string{"/models/keys", "/models/keys/" + id, "/models"} {
		r := jobsRequest(t, reg, nil, "GET", APIPrefix+uri, "")
		if r.StatusCode() != 200 || strings.Contains(string(r.Body()), "SECRET") {
			t.Fatalf("GET %s: %d %s", uri, r.StatusCode(), r.Body())
		}
	}
	r := jobsRequest(t, reg, nil, "PUT", APIPrefix+"/models/keys/"+id, `{"provider":"openai","label":"renamed","budget_usd":3}`)
	if r.StatusCode() != 200 || !strings.Contains(string(r.Body()), `"renamed"`) || !strings.Contains(string(r.Body()), `"last4":"9876"`) {
		t.Fatalf("update: %d %s", r.StatusCode(), r.Body())
	}
	if r := jobsRequest(t, reg, nil, "POST", APIPrefix+"/models/keys", `{"provider":"bogus","value":"x"}`); r.StatusCode() != 400 {
		t.Fatalf("bad provider: %d", r.StatusCode())
	}
	if r := jobsRequest(t, reg, nil, "POST", APIPrefix+"/models/keys", `{"provider":"openai","value":"x","extra":1}`); r.StatusCode() != 400 {
		t.Fatalf("unknown field: %d", r.StatusCode())
	}
	if r := jobsRequest(t, reg, nil, "DELETE", APIPrefix+"/models/keys/"+id, ""); r.StatusCode() != 204 {
		t.Fatalf("delete: %d %s", r.StatusCode(), r.Body())
	}
	if r := jobsRequest(t, reg, nil, "GET", APIPrefix+"/models/keys/"+id, ""); r.StatusCode() != 404 {
		t.Fatalf("get deleted: %d", r.StatusCode())
	}
}

func TestModelKeysOrgIsolation(t *testing.T) {
	store := keystore.NewMemory()
	a := modelsRegister(Services{OrgID: "org_a", Keys: store})
	b := modelsRegister(Services{OrgID: "org_b", Keys: store})
	resp := jobsRequest(t, a, nil, "POST", APIPrefix+"/models/keys", `{"provider":"groq","value":"gsk_abcdefghijkl"}`)
	var created ModelKeyResponse
	_ = json.Unmarshal(resp.Body(), &created)
	if r := jobsRequest(t, b, nil, "GET", APIPrefix+"/models/keys/"+created.Key.ID, ""); r.StatusCode() != 404 {
		t.Fatalf("org b read org a's key: %d", r.StatusCode())
	}
	if r := jobsRequest(t, b, nil, "POST", APIPrefix+"/models/keys/"+created.Key.ID+"/test", ""); r.StatusCode() != 404 {
		t.Fatalf("org b tested org a's key: %d", r.StatusCode())
	}
}

func TestModelKeysUnavailable(t *testing.T) {
	reg := modelsRegister(Services{OrgID: "o"})
	r := jobsRequest(t, reg, nil, "GET", APIPrefix+"/models/keys", "")
	if r.StatusCode() != 503 || !strings.Contains(string(r.Body()), "HEIMDALL_MASTER_KEY") {
		t.Fatalf("no store: %d %s", r.StatusCode(), r.Body())
	}
	// The catalog still answers, so the picker can show providers.
	r = jobsRequest(t, reg, nil, "GET", APIPrefix+"/models", "")
	if r.StatusCode() != 200 || !strings.Contains(string(r.Body()), `"keys_ready":false`) {
		t.Fatalf("catalog: %d %s", r.StatusCode(), r.Body())
	}
}

func TestModelKeyTestCall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer good-key-123456789" {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"error":{"message":"Incorrect API key provided: ` + r.Header.Get("Authorization")[7:] + `"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"llama-3-8b"},{"id":"qwen-7b"}]}`))
	}))
	defer srv.Close()
	reg := modelsRegister(Services{OrgID: "o", Keys: keystore.NewMemory()})
	create := func(value string) string {
		resp := jobsRequest(t, reg, nil, "POST", APIPrefix+"/models/keys", `{"provider":"openai-compatible","base_url":"`+srv.URL+`/v1","value":"`+value+`"}`)
		var c ModelKeyResponse
		if err := json.Unmarshal(resp.Body(), &c); err != nil || c.Key.ID == "" {
			t.Fatalf("create: %s", resp.Body())
		}
		return c.Key.ID
	}
	good, bad := create("good-key-123456789"), create("bad-key-SECRETVALUE1")

	var tr ModelKeyTestResponse
	r := jobsRequest(t, reg, nil, "POST", APIPrefix+"/models/keys/"+good+"/test", "")
	if err := json.Unmarshal(r.Body(), &tr); err != nil || !tr.Result.OK || tr.Key.Status != "ok" || len(tr.Result.Models) != 2 || tr.Key.LastTestedAt == nil {
		t.Fatalf("good key: %s", r.Body())
	}
	r = jobsRequest(t, reg, nil, "POST", APIPrefix+"/models/keys/"+bad+"/test", "")
	if err := json.Unmarshal(r.Body(), &tr); err != nil || tr.Result.OK || tr.Key.Status != "invalid" {
		t.Fatalf("bad key: %s", r.Body())
	}
	if strings.Contains(string(r.Body()), "SECRETVALUE") {
		t.Fatalf("test result echoed the key: %s", r.Body())
	}
	// The default model is the working key's first listed... none listed, so
	// no suggestion for openai-compatible without a model list.
	r = jobsRequest(t, reg, nil, "GET", APIPrefix+"/models", "")
	if r.StatusCode() != 200 {
		t.Fatalf("catalog: %d", r.StatusCode())
	}
}

func TestModelKeysHostedBaseURL(t *testing.T) {
	reg := modelsRegister(Services{OrgID: "o", Hosted: true, Keys: keystore.NewMemory()})
	for _, u := range []string{"http://api.example.com/v1", "https://127.0.0.1/v1", "https://10.0.0.5/v1", "https://localhost:8080/v1", "https://169.254.169.254/latest"} {
		r := jobsRequest(t, reg, nil, "POST", APIPrefix+"/models/keys", `{"provider":"openai-compatible","base_url":"`+u+`","value":"abcdefghijkl"}`)
		if r.StatusCode() != 400 {
			t.Errorf("%s: %d %s", u, r.StatusCode(), r.Body())
		}
	}
	if r := jobsRequest(t, reg, nil, "POST", APIPrefix+"/models/keys", `{"provider":"openai-compatible","base_url":"https://api.together.xyz/v1","value":"abcdefghijkl"}`); r.StatusCode() != 201 {
		t.Fatalf("public https: %d %s", r.StatusCode(), r.Body())
	}
}

func TestDefaultModelPicksCheapestUsable(t *testing.T) {
	keys := []ModelKeyView{
		{ID: "a", Provider: "anthropic", Status: "ok", Models: []string{}},
		{ID: "g", Provider: "groq", Status: "invalid", Models: []string{}},
		{ID: "o", Provider: "openai", Status: "ok", Models: []string{}},
		{ID: "m", Provider: "gemini", Status: "untested", Models: []string{}},
	}
	d := defaultModel(keys)
	if d == nil || d.KeyID != "o" || d.Model != "gpt-4o-mini" {
		t.Fatalf("default = %+v", d)
	}
	if defaultModel(nil) != nil {
		t.Fatal("default without keys")
	}
}
