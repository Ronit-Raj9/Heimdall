package handlers

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/fasthttp/router"
	"github.com/valyala/fasthttp"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestDoctor(t *testing.T) {
	tests := []struct {
		name   string
		checks []schemas.DoctorCheck
		ok     bool
	}{
		{"all pass", []schemas.DoctorCheck{{ID: "docker", Status: schemas.DoctorPass}, {ID: "keys", Status: schemas.DoctorWarn}}, true},
		{"one fails", []schemas.DoctorCheck{{ID: "docker", Status: schemas.DoctorPass}, {ID: "gateway", Status: schemas.DoctorFail}}, false},
		{"none", nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := fullDeps()
			d.doctor = func(context.Context) []schemas.DoctorCheck { return tt.checks }
			resp := newTestClient(t, d).do(t, "GET", "/api/bench/doctor", nil, nil)
			body := decodeBody(t, resp)
			if resp.StatusCode() != 200 || body["ok"] != tt.ok {
				t.Fatalf("status %d body %s, want ok %v", resp.StatusCode(), resp.Body(), tt.ok)
			}
			if checks, _ := body["checks"].([]any); len(checks) != len(tt.checks) {
				t.Errorf("checks = %v", body["checks"])
			}
		})
	}
}

func TestHealth(t *testing.T) {
	body := decodeBody(t, newTestClient(t, testDeps{}).do(t, "GET", "/api/bench/health", nil, nil))
	if body["status"] != "ok" || body["version"] != "test" || body["mode"] != string(testMode) {
		t.Fatalf("health = %v", body)
	}
}

// 6-A-06: health says whether the server launches, so an offline server's UI
// does not advertise the experiment creation workflow.
func TestHealthLaunchCapability(t *testing.T) {
	for _, launch := range []bool{true, false} {
		r := router.New()
		NewHealthHandler(testMode, "test", nil).WithLaunch(launch).RegisterRoutes(r)
		ctx := &fasthttp.RequestCtx{}
		ctx.Request.Header.SetMethod("GET")
		ctx.Request.SetRequestURI("/api/bench/health")
		r.Handler(ctx)
		var body map[string]any
		if err := json.Unmarshal(ctx.Response.Body(), &body); err != nil {
			t.Fatal(err)
		}
		if body["launch"] != launch {
			t.Fatalf("launch = %v, want %v (body %s)", body["launch"], launch, ctx.Response.Body())
		}
	}
	body := decodeBody(t, newTestClient(t, testDeps{}).do(t, "GET", "/api/bench/health", nil, nil))
	if _, ok := body["launch"]; ok {
		t.Fatalf("unknown launch capability must be omitted: %v", body)
	}
}
