package packs

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"slices"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"
)

var available = []string{
	"hd-harbor-tb2", "hd-swe-verified", "hd-swe-ib-lcb", "hd-swe-ib-swe",
	"tau2", "agentdojo-workspace", "agentdojo-banking", "longmemeval",
	"browsecomp-plus-small", "gaia", "custom-thing",
}

func TestAllPacks(t *testing.T) {
	want := []string{"coding", "terminal", "support", "memory", "research", "voice", "safety", AllCompatible}
	var got []string
	for _, p := range All() {
		got = append(got, p.ID)
		if p.Name == "" || p.Description == "" || len(p.Benchmarks) == 0 {
			t.Errorf("pack %s incomplete: %+v", p.ID, p)
		}
	}
	if !slices.Equal(got, want) {
		t.Fatalf("ids %v want %v", got, want)
	}
	a := All()
	a[0].Benchmarks[0] = "mutated"
	if All()[0].Benchmarks[0] == "mutated" {
		t.Fatal("All shares its slices")
	}
}

func TestResolve(t *testing.T) {
	cases := map[string][]string{
		"coding":   {"hd-harbor-tb2", "hd-swe-verified", "hd-swe-ib-lcb"},
		"terminal": {"hd-harbor-tb2"},
		"support":  {"tau2", "agentdojo-banking", "agentdojo-workspace"},
		"memory":   {"longmemeval"},
		"research": {"gaia", "browsecomp-plus-small"},
		"voice":    nil,
		"safety":   {"agentdojo-banking", "agentdojo-workspace", "hd-swe-ib-swe"},
		"nope":     nil,
	}
	for id, want := range cases {
		if got := Resolve(id, available); !slices.Equal(got, want) {
			t.Errorf("Resolve(%s) = %v want %v", id, got, want)
		}
	}
	all := Resolve(AllCompatible, append(slices.Clone(available), "gaia", ""))
	if len(all) != len(available) || !slices.IsSorted(all) {
		t.Errorf("all-compatible = %v", all)
	}
}

func TestCapabilityOf(t *testing.T) {
	cases := []struct{ id, track, want string }{
		{"hd-harbor-tb2", "terminal", CapTerminal},
		{"hd-harbor-skills", "skills", CapTerminal},
		{"hd-swe-verified", "", CapCoding},
		{"hd-swe-ib-lcb", "coding", CapCoding},
		{"hd-swe-ib-swe", "coding", CapSafety},
		{"agentdojo-banking", "tool_use", CapSafety},
		{"tau2", "support", CapSupport},
		{"mcpmark", "mcp", CapSupport},
		{"longmemeval", "memory", CapMemory},
		{"locomo", "", CapMemory},
		{"gaia", "qa", CapResearch},
		{"browsecomp-plus-small", "research", CapResearch},
		{"voicebench", "voice", CapVoice},
		{"my-thing", "voice", CapVoice},
		{"my-thing", "ml_engineering", CapGPU},
		{"agent-team-run", "", CapTeam},
		{"my-thing", "", CapCoding},
		{"my-thing", "unknown-track", CapCoding},
	}
	for _, c := range cases {
		if got := CapabilityOf(c.id, c.track); got != c.want {
			t.Errorf("CapabilityOf(%q,%q) = %s want %s", c.id, c.track, got, c.want)
		}
	}
}

type fakeLister struct {
	sums []schemas.SuiteSummary
	err  error
}

func (f fakeLister) ListSummaries(context.Context) ([]schemas.SuiteSummary, error) {
	return f.sums, f.err
}

func serve(t *testing.T, l SuiteLister) (int, []byte) {
	t.Helper()
	r := router.New()
	NewHandler(schemas.Mode("local"), l).RegisterRoutes(r)
	ln := fasthttputil.NewInmemoryListener()
	go fasthttp.Serve(ln, r.Handler) //nolint:errcheck
	defer ln.Close()
	c := http.Client{Transport: &http.Transport{DialContext: func(context.Context, string, string) (net.Conn, error) { return ln.Dial() }}}
	resp, err := c.Get("http://x/api/bench/evaluation-packs")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var raw json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, raw
}

func TestHandler(t *testing.T) {
	sums := []schemas.SuiteSummary{
		{ID: "hd-harbor-tb2", AdmittedCount: 12, Builtin: true, Source: "library"},
		{ID: "hd-swe-verified", AdmittedCount: 3},
		{ID: "agentdojo-banking", AdmittedCount: 5},
		{ID: "hd-swe-rebench", Error: "broken"},
	}
	code, body := serve(t, fakeLister{sums: sums})
	if code != 200 {
		t.Fatalf("status %d: %s", code, body)
	}
	var views []PackView
	if err := json.Unmarshal(body, &views); err != nil {
		t.Fatal(err)
	}
	by := map[string]PackView{}
	for _, v := range views {
		by[v.ID] = v
	}
	coding := by["coding"]
	if !slices.Equal(coding.Benchmarks, []string{"hd-harbor-tb2", "hd-swe-verified"}) || coding.TotalReady != 15 || coding.ReadyTasks["hd-harbor-tb2"] != 12 {
		t.Errorf("coding = %+v", coding)
	}
	if v := by["voice"]; v.Benchmarks == nil || len(v.Benchmarks) != 0 {
		t.Errorf("voice = %+v", v)
	}
	if v := by["safety"]; !slices.Equal(v.Benchmarks, []string{"agentdojo-banking"}) || v.TotalReady != 5 {
		t.Errorf("safety = %+v", v)
	}
	if v := by[AllCompatible]; len(v.Benchmarks) != 3 {
		t.Errorf("all = %+v", v)
	}

	if code, _ := serve(t, fakeLister{err: errors.New("boom")}); code != 500 {
		t.Errorf("error status %d", code)
	}
	if code, _ := serve(t, nil); code != 503 {
		t.Errorf("nil status %d", code)
	}
}
