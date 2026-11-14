package faultproxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestErrorEnvelopes(t *testing.T) {
	srv := &Server{logger: schemas.NoopLogger{}}
	cases := []struct {
		format string
		status int
		cat    string
		want   string // path=value checks rendered below
	}{
		{FormatAnthropic, 401, errAuth, "type=error error.type=authentication_error"},
		{FormatAnthropic, 529, "", "type=error error.type=overloaded_error"},
		{FormatAnthropic, 429, "", "type=error error.type=rate_limit_error"},
		{FormatAnthropic, 502, errUpstream, "type=error error.type=api_error"},
		{FormatOpenAI, 401, errAuth, "error.type=invalid_request_error error.code=invalid_api_key"},
		{FormatOpenAI, 500, "", "error.type=server_error error.code=<nil>"},
		{FormatOpenAI, 404, errNotFound, "error.type=invalid_request_error"},
		{FormatGemini, 503, "", "error.status=UNAVAILABLE error.code=503"},
		{FormatGemini, 429, "", "error.status=RESOURCE_EXHAUSTED error.code=429"},
		{FormatGemini, 401, errAuth, "error.status=UNAUTHENTICATED error.code=401"},
	}
	for _, tc := range cases {
		t.Run(tc.format+" "+tc.want, func(t *testing.T) {
			rec := httptest.NewRecorder()
			srv.writeError(rec, tc.format, tc.status, tc.cat, "msg")
			if rec.Code != tc.status {
				t.Fatalf("status %d", rec.Code)
			}
			var body map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			for _, check := range splitChecks(tc.want) {
				if got := lookup(body, check[0]); got != check[1] {
					t.Errorf("%s = %s, want %s (%s)", check[0], got, check[1], rec.Body.String())
				}
			}
			if lookup(body, "error.message") != "msg" {
				t.Errorf("message lost: %s", rec.Body.String())
			}
		})
	}
	if faultMessage(FormatOpenAI, http.StatusServiceUnavailable) == faultMessage(FormatOpenAI, 500) {
		t.Error("503 and 500 share a message")
	}
}

func splitChecks(s string) [][2]string {
	var out [][2]string
	for _, f := range strings.Fields(s) {
		k, v, _ := strings.Cut(f, "=")
		out = append(out, [2]string{k, v})
	}
	return out
}

// lookup renders the value at a dotted path, "<nil>" for JSON null.
func lookup(m map[string]any, path string) string {
	var cur any = m
	for _, k := range strings.Split(path, ".") {
		obj, ok := cur.(map[string]any)
		if !ok {
			return "<missing>"
		}
		if cur, ok = obj[k]; !ok {
			return "<missing>"
		}
	}
	switch v := cur.(type) {
	case nil:
		return "<nil>"
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case string:
		return v
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}
