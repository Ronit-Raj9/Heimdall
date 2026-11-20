package k8s

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// probeGateway is a gateway with the optional Bifrost capabilities the
// planner looks up (engine/bifrostfeatures.go).
type probeGateway struct{ schemas.Gateway }

func (probeGateway) PromptCacheConfigured(context.Context, string) (bool, error) { return true, nil }
func (probeGateway) ComplexityAnalyzerState(context.Context) (string, error)     { return "on", nil }

// 2-D-03: the egress wrapper must not hide the inner gateway's optional
// capabilities from the planner.
func TestWrappedGatewayKeepsOptionalCapabilities(t *testing.T) {
	gw := WrapGateway(probeGateway{}, &Driver{})
	if _, ok := schemas.GatewayAs[interface {
		PromptCacheConfigured(context.Context, string) (bool, error)
	}](gw); !ok {
		t.Fatal("prompt-cache probe hidden by the Kubernetes wrapper")
	}
	if _, ok := schemas.GatewayAs[interface {
		ComplexityAnalyzerState(context.Context) (string, error)
	}](gw); !ok {
		t.Fatal("complexity probe hidden by the Kubernetes wrapper")
	}
	if _, ok := schemas.GatewayAs[schemas.TeamGateway](gw); ok {
		t.Fatal("capability the inner gateway lacks was reported")
	}
}
