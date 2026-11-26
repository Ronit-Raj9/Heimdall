package handlers

import (
	"encoding/json"
	"net"
	"strings"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"
)

// runsClientW4m4 mounts only the runs handler over index and suites.
func runsClientW4m4(t *testing.T, index ReadIndex, suites SuiteSource) *testClient {
	t.Helper()
	r := router.New()
	NewRunsHandler(testMode, index).WithSuites(suites).RegisterRoutes(r, lib.BodyLimit(testMode, lib.MaxBodyBytes))
	ln := fasthttputil.NewInmemoryListener()
	srv := &fasthttp.Server{Handler: r.Handler}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Shutdown() })
	return &testClient{client: &fasthttp.Client{Dial: func(string) (net.Conn, error) { return ln.Dial() }}}
}

// TestGetRunCarriesOutputKind checks that the run page gets the output kind
// the run list computes: the suite's kind, or the task's own.
func TestGetRunCarriesOutputKind(t *testing.T) {
	c := runsClientW4m4(t, runListIndex(), runListSuites())
	for id, want := range map[string]string{"r_t01_A": "answer", "r_t02_A": "state"} {
		resp := c.do(t, "GET", APIPrefix+"/runs/"+id, nil, nil)
		if resp.StatusCode() != fasthttp.StatusOK {
			t.Fatalf("GET %s = %d: %s", id, resp.StatusCode(), resp.Body())
		}
		var out schemas.RunDetail
		if err := json.Unmarshal(resp.Body(), &out); err != nil {
			t.Fatal(err)
		}
		if out.Output != want {
			t.Errorf("%s output = %q, want %q", id, out.Output, want)
		}
	}
	// Without a suite source the kind is left out, never guessed.
	resp := runsClientW4m4(t, runListIndex(), nil).do(t, "GET", APIPrefix+"/runs/r_t01_A", nil, nil)
	if strings.Contains(string(resp.Body()), `"output"`) {
		t.Errorf("run without suites carries output: %s", resp.Body())
	}
}
