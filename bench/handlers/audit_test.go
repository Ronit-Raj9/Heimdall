package handlers

import (
	"testing"
)

func TestAuditRoutesByContentType(t *testing.T) {
	tests := []struct {
		name, contentType, body string
		status                  int
		csv, json               bool
	}{
		{"csv", "text/csv", "task,a_pass,a_n,b_pass,b_n\nt1,1,3,2,3\n", 200, true, false},
		{"csv without type", "", "task,a_pass,a_n,b_pass,b_n\nt1,1,3,2,3\n", 200, true, false},
		{"json", "application/json; charset=utf-8", `{"rows":[{"task":"t1","a_pass":1,"a_n":3,"b_pass":2,"b_n":3}],"alpha":0.05}`, 200, false, true},
		{"json unknown field", "application/json", `{"rows":[],"nope":1}`, 400, false, false},
		{"empty", "text/csv", " ", 400, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := fullDeps()
			a := &fakeAuditor{}
			d.auditor = a
			resp := newTestClient(t, d).do(t, "POST", "/api/bench/audit", []byte(tt.body), authed(tt.contentType))
			if resp.StatusCode() != tt.status {
				t.Fatalf("status = %d, want %d: %s", resp.StatusCode(), tt.status, resp.Body())
			}
			if a.gotCSV != tt.csv || a.gotJSON != tt.json {
				t.Errorf("csv %v json %v, want %v %v", a.gotCSV, a.gotJSON, tt.csv, tt.json)
			}
			if tt.status == 200 && decodeBody(t, resp)["decision"] != "inconclusive" {
				t.Errorf("body = %s", resp.Body())
			}
		})
	}
}
