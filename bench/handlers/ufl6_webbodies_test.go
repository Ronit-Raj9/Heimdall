package handlers

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/fasthttp/router"
	"github.com/valyala/fasthttp"
)

// ufl6Bodies are the request bodies the Connect page sends, kept in one file
// that the web test (ui/lib/store/apis/ufl6ConnectApi.test.ts) checks too.
func ufl6Bodies(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	b, err := os.ReadFile("testdata/ufl6_web_bodies.json")
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func ufl6Post(r *router.Router, path string, body []byte) (int, []byte) {
	var ctx fasthttp.RequestCtx
	ctx.Request.Header.SetMethod("POST")
	ctx.Request.SetRequestURI(path)
	ctx.Request.SetBody(body)
	r.Handler(&ctx)
	return ctx.Response.StatusCode(), append([]byte(nil), ctx.Response.Body()...)
}

func strictDecode(t *testing.T, raw []byte, out any) {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		t.Fatalf("web body %s: %v", raw, err)
	}
}

func TestUFL6WebBodiesReachHandlers(t *testing.T) {
	bodies := ufl6Bodies(t)
	r, _ := connectRouter(t, nil)

	var own ConnectVerifyRequest
	strictDecode(t, bodies["verify_own_task"], &own)
	if own.Suite != "smoke" || own.Task != "t1" || own.SkipRealTask {
		t.Fatalf("own task %+v", own)
	}
	var conn ConnectVerifyRequest
	strictDecode(t, bodies["verify_connection_only"], &conn)
	if !conn.SkipRealTask || conn.Suite != "" {
		t.Fatalf("connection only %+v", conn)
	}
	for _, k := range []string{"verify_own_task", "verify_connection_only", "verify_default"} {
		code, body := ufl6Post(r, APIPrefix+"/connect/verify", bodies[k])
		// No agent is saved here: 404 names it; a 400 would mean the body was refused.
		if code == fasthttp.StatusBadRequest {
			t.Fatalf("%s refused: %s", k, body)
		}
	}

	code, body := ufl6Post(r, APIPrefix+"/connect/explain", bodies["explain"])
	var ex ConnectExplainResponse
	if code != 200 || json.Unmarshal(body, &ex) != nil || !ex.Found || ex.Failure == nil || ex.Failure.Code != "rate_limit" {
		t.Fatalf("explain %d %s", code, body)
	}

	tr := connectTracesRouter(t, t.TempDir(), true)
	code, body = ufl6Post(tr, AgentFileCheckPath, bodies["agent_file"])
	var fc AgentFileCheckResult
	if code != 200 || json.Unmarshal(body, &fc) != nil || !fc.OK || fc.ConfigYAML == "" && fc.Request == nil {
		t.Fatalf("agent file %d %s", code, body)
	}
}
