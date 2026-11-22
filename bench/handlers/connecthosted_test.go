package handlers

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/connect"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/schemas"
)

// N104: a hosted server's Detect refuses an internal compose name before any
// lookup and names no address.
func TestConnectDetectHostedRefusesInternalName(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := projectfs.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	env := OrgEnv{Mode: schemas.Mode("live"), Services: Services{Projects: st, Hosted: true}}
	var hosted bool
	h := &connectHandler{env: env, verify: connect.Verify, detect: func(ctx context.Context, in connect.Input, opt connect.Options) *connect.Proposal {
		hosted = opt.Hosted
		return connect.Detect(ctx, in, opt)
	}}
	r := router.New()
	r.POST(APIPrefix+"/connect/detect", h.detectRoute)
	code, body := abCall(r, "POST", APIPrefix+"/connect/detect", ConnectDetectRequest{Input: "http://heimdall-postgres-1:5432"})
	if code != 200 {
		t.Fatalf("detect %d %s", code, body)
	}
	if !hosted {
		t.Fatal("the handler did not pass Hosted to Detect")
	}
	var det ConnectDetectResponse
	if err := json.Unmarshal(body, &det); err != nil {
		t.Fatal(err)
	}
	if len(det.Failures) != 1 || det.Failures[0].Code != connect.FailPrivateAddress || !strings.Contains(det.Failures[0].Message, "is not a public internet address") {
		t.Fatalf("failures %+v", det.Failures)
	}
	if strings.Contains(string(body), "resolves to") || det.Best() != nil {
		t.Fatalf("answer %s", body)
	}
}
