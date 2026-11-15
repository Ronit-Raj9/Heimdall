package main

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// rowRemote is agent check's row for a remote_http agent (Remote coverage,
// docs/remote-execution.md): which of the three channels were active in the
// check run and what is therefore trusted, agent-reported or unknown.
const rowRemote = "remote_coverage"

// remoteRows returns the remote coverage row for a remote agent, nil for any
// other agent. It is pure.
func remoteRows(in *checkInput) []checkRow {
	if in == nil || in.agent == nil || in.agent.Remote == nil {
		return nil
	}
	row := checkRow{ID: rowRemote, Title: "remote agent: measured channels"}
	r := in.ev.Remote
	if r == nil {
		return []checkRow{row.set(checkWarn, "evidence.json has no remote coverage summary", "rebuild bifrost-bench: the run was recorded before remote coverage")}
	}
	c := r.Channels
	detail := fmt.Sprintf("model traffic through the gateway: %s; tools through the bench: %s; self-report: %s. trusted: %s. agent-reported: %s. unknown: %s",
		c.ModelTraffic, c.Tools, c.SelfReport, listOrNone(r.Trusted), listOrNone(r.AgentReported), listOrNone(r.Unknown))
	if r.Reported != nil && (r.Reported.Version != "" || r.Reported.Build != "") {
		detail += fmt.Sprintf(". self-declared version %q build %q", r.Reported.Version, r.Reported.Build)
	}
	switch {
	case c.Tools == schemas.RemoteChannelMissing:
		return []checkRow{row.set(checkFail, detail, "remote.tools is expose but no tool gate was published: check images.toolproxy is rebuilt with the gate and the task has a proxied service")}
	case c.ModelTraffic != schemas.RemoteChannelOn:
		return []checkRow{row.set(checkWarn, detail, "set remote.model_traffic gateway (and gateway.public_url for a deployment on another machine) to measure tokens and cost")}
	}
	return []checkRow{row.set(checkPass, detail, "")}
}

func listOrNone(items []string) string {
	if len(items) == 0 {
		return "none"
	}
	return strings.Join(items, ", ")
}

// remoteToolsCheck is doctor's row for the remote tool gate (Remote
// coverage): where a remote agent's published tool proxy would listen, and
// a warning when that is beyond loopback, where the bearer token and every
// tool call cross the network without TLS unless something wraps them. It is
// pure.
func remoteToolsCheck(listen, publicURL string) schemas.DoctorCheck {
	row := schemas.DoctorCheck{ID: "remote tools"}
	host, port, err := utils.SplitRemoteToolsListen(listen)
	if err != nil {
		row.Status, row.Detail = schemas.DoctorFail, err.Error()
		return row
	}
	if _, err := utils.RemoteToolsBaseURL(publicURL, host, max(port, 1)); err != nil {
		row.Status, row.Detail = schemas.DoctorFail, err.Error()
		return row
	}
	where := net.JoinHostPort(host, strconv.Itoa(port))
	if port == 0 {
		where = host + " (a free port per run)"
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		row.Status, row.Detail = schemas.DoctorPass, "a remote agent's tool gate (remote.tools expose) is published on "+where+": loopback, reached through a tunnel"
		return row
	}
	row.Status = schemas.DoctorWarn
	row.Detail = "a remote agent's tool gate is published on " + where + ", beyond loopback, with no TLS: the bearer token and every tool call cross the network in clear text unless a tunnel, VPN or TLS proxy wraps them"
	return row
}
