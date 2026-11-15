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
