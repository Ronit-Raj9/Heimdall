package toolproxy

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseRoutes(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantErr bool
		check   func(t *testing.T, rs *Routes)
	}{
		{
			name: "defaults filled",
			in:   `{"run_id":"r_1","routes":[{"service":"fs","upstream":"http://fs:8080/"}]}`,
			check: func(t *testing.T, rs *Routes) {
				r := rs.Routes[0]
				if rs.RunID != "r_1" || r.Protocol != ProtocolMCP || r.MCPPath != "/mcp" || r.Upstream != "http://fs:8080" {
					t.Fatalf("got %+v", rs)
				}
			},
		},
		{
			name: "http route and relative mcp path",
			in:   `{"routes":[{"service":"api","upstream":"http://api:80","protocol":"http"},{"service":"m","upstream":"https://m:1","mcp_path":"rpc"}]}`,
			check: func(t *testing.T, rs *Routes) {
				if rs.Routes[0].Protocol != ProtocolHTTP || rs.Routes[1].MCPPath != "/rpc" {
					t.Fatalf("got %+v", rs)
				}
			},
		},
		{name: "not json", in: `{`, wantErr: true},
		{name: "no routes", in: `{"routes":[]}`, wantErr: true},
		{name: "bad service id", in: `{"routes":[{"service":"../x","upstream":"http://x:1"}]}`, wantErr: true},
		{name: "duplicate service", in: `{"routes":[{"service":"a","upstream":"http://a:1"},{"service":"a","upstream":"http://a:2"}]}`, wantErr: true},
		{name: "bad upstream", in: `{"routes":[{"service":"a","upstream":"ftp://a"}]}`, wantErr: true},
		{name: "bad protocol", in: `{"routes":[{"service":"a","upstream":"http://a:1","protocol":"grpc"}]}`, wantErr: true},
		{
			name: "external route",
			in:   `{"routes":[{"service":"acme","upstream":"https://api.acme.test/v1/","protocol":"external","external":{"mode":"replay","cassette":"/cfg/external/acme.cassette.jsonl","allow":[{"methods":["GET"],"path_prefix":"/rates"}]}}]}`,
			check: func(t *testing.T, rs *Routes) {
				r := rs.Routes[0]
				if r.Protocol != ProtocolExternal || r.External == nil || r.External.Mode != "replay" || r.Upstream != "https://api.acme.test/v1" || r.External.Allow[0].PathPrefix != "/rates" {
					t.Fatalf("got %+v", r)
				}
			},
		},
		{name: "external without block", in: `{"routes":[{"service":"a","upstream":"https://a","protocol":"external"}]}`, wantErr: true},
		{name: "external replay without cassette", in: `{"routes":[{"service":"a","upstream":"https://a","protocol":"external","external":{"mode":"replay"}}]}`, wantErr: true},
		{name: "external record without record_to", in: `{"routes":[{"service":"a","upstream":"https://a","protocol":"external","external":{"mode":"record"}}]}`, wantErr: true},
		{name: "external bad mode", in: `{"routes":[{"service":"a","upstream":"https://a","protocol":"external","external":{"mode":"sometimes"}}]}`, wantErr: true},
		{name: "external relative prefix", in: `{"routes":[{"service":"a","upstream":"https://a","protocol":"external","external":{"mode":"live","allow":[{"path_prefix":"rates"}]}}]}`, wantErr: true},
		{name: "external upstream with query", in: `{"routes":[{"service":"a","upstream":"https://a/?k=1","protocol":"external","external":{"mode":"live"}}]}`, wantErr: true},
		{name: "external negative cap", in: `{"routes":[{"service":"a","upstream":"https://a","protocol":"external","external":{"mode":"live","max_calls":-1}}]}`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rs, err := ParseRoutes([]byte(tt.in))
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.check != nil {
				tt.check(t, rs)
			}
		})
	}
}

func TestLoadRoutes(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "routes.json")
	if err := os.WriteFile(p, []byte(`{"routes":[{"service":"fs","upstream":"http://fs:8080"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{name: "present", path: p},
		{name: "missing", path: filepath.Join(dir, "nope.json"), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := LoadRoutes(tt.path); (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
