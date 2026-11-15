package adapters

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestExternalVars(t *testing.T) {
	cases := []struct {
		name     string
		services []schemas.ServiceHandle
		want     map[string]string
	}{
		{name: "no external apis", want: map[string]string{"external.apis_json": "{}"}},
		{
			name: "one external api beside an mcp server",
			services: append(mcpServices(), schemas.ServiceHandle{
				Spec: schemas.ServiceSpec{ID: "weather", Role: schemas.ServiceRoleExternal}, Host: "toolproxy", URL: "http://toolproxy:9200/x/weather",
			}),
			want: map[string]string{
				"external.apis_json":   `{"weather":{"url":"http://toolproxy:9200/x/weather"}}`,
				"external.weather.url": "http://toolproxy:9200/x/weather",
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			vars := map[string]string{}
			if err := externalVars(vars, &schemas.RunContext{Services: c.services}); err != nil {
				t.Fatal(err)
			}
			if len(vars) != len(c.want) {
				t.Fatalf("vars = %v, want %v", vars, c.want)
			}
			for k, v := range c.want {
				if vars[k] != v {
					t.Errorf("%s = %q, want %q", k, vars[k], v)
				}
			}
		})
	}
}
