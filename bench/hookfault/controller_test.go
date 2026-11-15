package hookfault

import (
	"bytes"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

func config() schemas.AdapterFaultConfig {
	return schemas.AdapterFaultConfig{RunID: "r_1", Token: "runtime-token-12345678901234567890", AdminToken: "admin-token-12345678901234567890", Nonce: "nonce-1234567890123456", WallMS: 1000, Rule: schemas.AdapterFaultRule{Kind: "tool", Name: "search", Mode: "error"}, Hooks: []schemas.AdapterFaultHook{{Kind: "tool", Name: "search", Modes: []string{"error"}}}, Sessions: []string{"s"}}
}
func TestControllerValidation(t *testing.T) {
	if _, err := New(config(), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	bad := config()
	bad.Rule.Mode = "timeout"
	if _, err := New(bad, &bytes.Buffer{}); err == nil {
		t.Fatal("timeout without bound accepted")
	}
	if err := utils.ValidateAdapterFaultHooks(config().Hooks, config().Rule); err != nil {
		t.Fatal(err)
	}
}
