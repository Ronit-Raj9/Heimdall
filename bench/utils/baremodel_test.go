package utils

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestIsBareModel(t *testing.T) {
	for _, tc := range []struct {
		name string
		a    *schemas.AgentConfig
		want bool
	}{
		{"nil", nil, false},
		{"bare", &schemas.AgentConfig{Adapter: "command", Env: map[string]string{BareModelEnv: "1"}}, true},
		{"flag off", &schemas.AgentConfig{Adapter: "command", Env: map[string]string{BareModelEnv: "0"}}, false},
		{"no env", &schemas.AgentConfig{Adapter: "command"}, false},
		{"remote", &schemas.AgentConfig{Adapter: "remote_http", Env: map[string]string{BareModelEnv: "1"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsBareModel(tc.a); got != tc.want {
				t.Fatalf("IsBareModel = %v, want %v", got, tc.want)
			}
		})
	}
}
