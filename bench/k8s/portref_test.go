package k8s

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// 2-D-04: a gateway Service whose targetPort names a container port must
// resolve, and the egress policy must carry that name.
func TestResolveGatewayNamedTargetPort(t *testing.T) {
	f := newFakeAPI(t)
	f.objs["/api/v1/namespaces/heimdall-gw/services/bifrost"]["spec"].(map[string]any)["ports"] =
		[]any{map[string]any{"port": 8080, "targetPort": "http"}}
	d := testDriver(t, f)
	gw, err := d.ResolveGateway(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if gw.targetPort != (PortRef{Name: "http"}) {
		t.Fatalf("targetPort = %+v", gw.targetPort)
	}
	b, _ := json.Marshal(gatewayPolicy(nil, nil, gw.namespace, gw.selector, gw.targetPort))
	if !strings.Contains(string(b), `"port":"http"`) {
		t.Fatalf("policy lost the named port: %s", b)
	}
}

func TestPortRefJSON(t *testing.T) {
	for in, want := range map[string]PortRef{`8080`: Port(8080), `"8080"`: Port(8080), `"http"`: {Name: "http"}} {
		var p PortRef
		if err := json.Unmarshal([]byte(in), &p); err != nil || p != want {
			t.Fatalf("%s: %+v %v", in, p, err)
		}
	}
	b, _ := json.Marshal(ServicePort{Port: 80})
	if strings.Contains(string(b), "targetPort") {
		t.Fatalf("unset targetPort written: %s", b)
	}
}
