package deploy

import (
	"os"
	"slices"
	"testing"

	"gopkg.in/yaml.v3"
)

// The hosted server registers an org's own key at the fault proxy (PUT
// /orgslots) before it mints a gateway key for From issues builds and model
// checks. The first real hosted run failed with "lookup faultproxy: no such
// host": the server must share an internal network with the fault proxy and
// carry its control token.
func TestServerReachesFaultProxyControl(t *testing.T) {
	b, err := os.ReadFile("hosted/docker-compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		Services map[string]struct {
			Networks    any               `yaml:"networks"`
			Environment map[string]string `yaml:"environment"`
		} `yaml:"services"`
		Networks map[string]struct {
			Internal bool `yaml:"internal"`
		} `yaml:"networks"`
	}
	if err := yaml.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	names := func(v any) []string {
		var out []string
		switch n := v.(type) {
		case []any:
			for _, x := range n {
				out = append(out, x.(string))
			}
		case map[string]any:
			for k := range n {
				out = append(out, k)
			}
		}
		return out
	}
	srv, fp := c.Services["heimdall"], c.Services["faultproxy"]
	sn, fn := names(srv.Networks), names(fp.Networks)
	shared := ""
	for _, n := range sn {
		if slices.Contains(fn, n) && c.Networks[n].Internal {
			shared = n
		}
	}
	if shared == "" {
		t.Fatalf("heimdall %v and faultproxy %v share no internal network", sn, fn)
	}
	if slices.Contains(sn, "upstream") {
		t.Fatal("the server must not join upstream")
	}
	if srv.Environment["BENCH_FAULTPROXY_TOKEN"] == "" {
		t.Fatal("the server has no BENCH_FAULTPROXY_TOKEN")
	}
}
