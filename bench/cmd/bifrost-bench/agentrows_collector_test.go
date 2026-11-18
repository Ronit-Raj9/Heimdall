package main

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/environments"
)

// TestEgressRowAllowsEventCollector: an agent that exports its OpenTelemetry
// spans to the per-run collector the bench pointed it at (OTEL_EXPORTER_OTLP_*
// is http://toolproxy:9200) has not left the run's network, so the egress row
// passes while the collector ran. With the collector off, the same traffic
// is egress. Records are the observer's own from a Pydantic AI check run.
func TestEgressRowAllowsEventCollector(t *testing.T) {
	otlp := []environments.NetWatchRecord{
		{T: environments.NetWatchDNS, Name: "toolproxy"},
		{T: environments.NetWatchDNSAnswer, Name: "toolproxy", IPs: []string{"172.21.0.3"}},
		{T: environments.NetWatchSYN, Dst: "172.21.0.3", Port: 9200},
		{T: environments.NetWatchHTTP, Dst: "172.21.0.3", Port: 9200, Method: "POST", Path: "/otlp/v1/traces", Host: "toolproxy:9200"},
	}
	egress := func(in *checkInput) checkRow {
		for _, r := range checkRows(in) {
			if r.ID == rowEgress {
				return r
			}
		}
		t.Fatal("no egress row")
		return checkRow{}
	}

	in := goodCheckInput()
	in.net = append(in.net, otlp...)
	if r := egress(in); r.Status != checkPass {
		t.Errorf("collector on: egress %s %q, want PASS", r.Status, r.Detail)
	}

	in = goodCheckInput()
	in.net = append(in.net, otlp...)
	in.ev.AgentEvents.Collector = "off"
	if r := egress(in); r.Status != checkFail || !strings.Contains(r.Detail, "toolproxy") {
		t.Errorf("collector off: egress %s %q, want FAIL naming toolproxy", r.Status, r.Detail)
	}

	in = goodCheckInput()
	in.net = append(in.net, otlp...)
	in.ev.AgentEvents = nil
	if r := egress(in); r.Status != checkFail {
		t.Errorf("no collector status: egress %s %q, want FAIL", r.Status, r.Detail)
	}
}
