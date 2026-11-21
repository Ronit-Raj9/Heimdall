package main

import "testing"

func TestGatewayLogsURL(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	cases := []struct {
		name string
		m    map[string]string
		want string
	}{
		{"nothing set", nil, ""},
		{"shared pool on", map[string]string{sharedRunnerSecEnv: "s"}, defaultGatewayLogs},
		{"explicit", map[string]string{gatewayLogsURLEnv: "http://gw:9000"}, "http://gw:9000"},
		{"explicit off", map[string]string{gatewayLogsURLEnv: "off", sharedRunnerSecEnv: "s"}, ""},
	}
	for _, c := range cases {
		if got := gatewayLogsURL(env(c.m)); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}
