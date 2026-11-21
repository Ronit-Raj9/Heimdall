import "@/lib/i18n";

import type { RunDetail, ToolCall } from "@/lib/types/bench";
import fs from "node:fs";
import path from "node:path";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { AnswerBlock, RunScenarioEvidence, ServicesList, SessionsTimeline, StateFiles, ToolCallTable } from "./runScenario";

const FIXTURES = path.resolve(__dirname, "../../public/fixtures");
/** A plain diff run, as the fixtures hold it: no scenario evidence at all. */
const plain = JSON.parse(fs.readFileSync(path.join(FIXTURES, "runs.r_fmw1z7ouim.json"), "utf8")) as RunDetail;

const calls: ToolCall[] = [
	{
		seq: 1,
		server: "crm",
		tool: "find_ticket",
		protocol: "mcp",
		arguments: '{"id": 4182}',
		args_sha256: "sha256:1111111111111111111111111111111111111111111111111111111111111111",
		result: "<script>alert('tool result')</script> ticket open",
		result_sha256: "sha256:2222222222222222222222222222222222222222222222222222222222222222",
		is_error: false,
		started: "2026-11-15T09:00:01Z",
		latency_ms: 42,
	},
	{
		seq: 2,
		session: "chat-2",
		server: "crm",
		tool: "refund",
		protocol: "mcp",
		args_sha256: "sha256:3333",
		result: "tool unavailable",
		result_sha256: "sha256:4444",
		is_error: true,
		status: 503,
		fault: "error",
		nonce: "n-1",
		started: "2026-11-15T09:00:02Z",
		latency_ms: 1250,
		undelivered: true,
	},
];

const toolRun: RunDetail = {
	...plain,
	tool_calls: calls,
	services: [
		{ id: "crm", role: "mcp", image: "bench/crm-mcp:1", image_id: "sha256:abc", ready: true, proxied: true },
		{ id: "ledger-db", role: "data", image: "postgres:16", ready: false, error: "ready check timed out after 60 s" },
	],
	state: [
		{
			service: "crm",
			id: "tickets",
			path: "state/crm/tickets",
			sha256: "sha256:5555555555555555555555555555555555555555555555555555555555555555",
			bytes: 4300,
		},
		{ service: "ledger-db", id: "dump", path: "state/ledger-db/dump", sha256: "", bytes: 0, error: "export larger than 16 MiB" },
	],
};

const sessionRun: RunDetail = {
	...plain,
	sessions: [
		{
			id: "question",
			index: 3,
			exit_code: 0,
			timed_out: true,
			started: "2026-11-15T09:10:00Z",
			ended: "2026-11-15T09:15:00Z",
			stdout_path: "agent.stream.3.jsonl",
		},
		{
			id: "chat-1",
			index: 1,
			exit_code: 0,
			timed_out: false,
			started: "2026-11-15T09:00:00Z",
			ended: "2026-11-15T09:01:30Z",
			stdout_path: "agent.stream.1.jsonl",
		},
		{
			id: "chat-2",
			index: 2,
			exit_code: 2,
			timed_out: false,
			started: "2026-11-15T09:02:00Z",
			ended: "2026-11-15T09:03:00Z",
			stdout_path: "agent.stream.2.jsonl",
		},
	],
	answer: { text: "Thursday at <b>7</b>, the Thai place", source: "file", sha256: "sha256:6666", truncated: true },
};

describe("RunScenarioEvidence", () => {
	it("renders nothing for a plain diff run: no empty boxes", () => {
		expect(plain.tool_calls).toBeUndefined();
		expect(renderToStaticMarkup(<RunScenarioEvidence detail={plain} arm="A" />)).toBe("");
	});

	it("shows only the parts a tool run recorded", () => {
		const html = renderToStaticMarkup(<RunScenarioEvidence detail={toolRun} arm="A" />);
		for (const id of ["run-tools-A", "run-services-A", "run-state-A"]) expect(html).toContain(`data-testid="${id}"`);
		for (const id of ["run-sessions-A", "run-answer"]) expect(html).not.toContain(`data-testid="${id}"`);
	});

	it("shows only the parts a session run recorded", () => {
		const html = renderToStaticMarkup(<RunScenarioEvidence detail={sessionRun} arm="B" />);
		expect(html).toContain('data-testid="run-sessions-B"');
		expect(html).toContain('data-testid="run-answer"');
		expect(html).not.toContain('data-testid="run-tools-B"');
		expect(html).not.toContain('data-testid="run-state-B"');
	});
});

describe("ToolCallTable", () => {
	it("lists calls in order with server, tool, result, fault and latency, details closed", () => {
		const html = renderToStaticMarkup(<ToolCallTable calls={calls} arm="A" />);
		expect(html).toContain('aria-label="Tool uses, Setup A"');
		expect(html).toContain("2 calls");
		expect(html).toContain("1 error");
		expect(html).toContain("1 problem injected on purpose");
		expect(html).toMatch(/data-testid="run-tool-call-1"[^>]*data-error="false"/);
		expect(html).toContain("find_ticket");
		expect(html).toMatch(/data-testid="run-tool-call-2-fault" data-tone="warn">.*injected error/);
		expect(html).toContain("HTTP 503");
		expect(html).toContain("not delivered");
		expect(html).toContain("session chat-2");
		expect(html).toContain("1,250 ms");
		expect(html).toMatch(/<th[^>]*scope="col"/);
		// Closed: a keyboard-reachable button says so, and the recorded text is not in the page yet.
		expect(html).toMatch(
			/<button type="button" aria-expanded="false" aria-controls="[^"]+" aria-label="Show arguments and result of call 1"/,
		);
		expect(html).not.toContain("ticket open");
	});

	it("opens a call's arguments and result as inert text", () => {
		const html = renderToStaticMarkup(<ToolCallTable calls={calls} arm="A" initialOpen={[1]} />);
		expect(html).toMatch(/aria-expanded="true" aria-controls="([^"]+)"[\s\S]*<tr[^>]* id="\1"/);
		expect(html).toContain("Hide arguments and result of call 1");
		const details = html.match(/data-testid="run-tool-call-1-details"[\s\S]*?<\/tr>/)?.[0] ?? "";
		expect(details).toContain("{&quot;id&quot;: 4182}");
		expect(details).not.toContain("<script>");
		expect(details).toContain("&lt;script&gt;alert(&#x27;tool result&#x27;)&lt;/script&gt; ticket open");
		expect(details).toContain("sha256 11111111…1111");
		expect(html).not.toContain('data-testid="run-tool-call-2-details"');
	});

	it("says when a call recorded no arguments", () => {
		const html = renderToStaticMarkup(<ToolCallTable calls={calls} arm="A" initialOpen={[2]} />);
		expect(html.match(/data-testid="run-tool-call-2-details"[\s\S]*?<\/tr>/)?.[0]).toContain("(none recorded)");
	});
});

describe("SessionsTimeline", () => {
	it("orders sessions by index and shows how each ended", () => {
		const html = renderToStaticMarkup(<SessionsTimeline sessions={sessionRun.sessions!} arm="B" />);
		expect(html).toContain("<ol");
		const order = [...html.matchAll(/data-testid="run-session-(\d)"/g)].map((m) => m[1]);
		expect(order).toEqual(["1", "2", "3"]);
		expect(html).toMatch(/data-testid="run-session-1" data-state="ok"/);
		expect(html).toMatch(/data-testid="run-session-2" data-state="failed"/);
		expect(html).toMatch(/data-testid="run-session-3" data-state="timed_out"/);
		expect(html).toContain("exit 2");
		expect(html).toContain("timed out");
		expect(html).toContain("90 s");
		expect(html).toContain("stream agent.stream.1.jsonl");
	});
});

describe("AnswerBlock", () => {
	it("shows the answer as text with its source, hash and truncation", () => {
		const html = renderToStaticMarkup(<AnswerBlock answer={sessionRun.answer!} />);
		expect(html).toContain('data-source="file"');
		expect(html).toContain("the answer file");
		expect(html).toContain("Thursday at &lt;b&gt;7&lt;/b&gt;, the Thai place");
		expect(html).not.toContain("<b>7</b>");
		expect(html).toContain("sha256 6666");
		expect(html).toContain("Cut at the size cap");
	});

	it("says when the answer came from the final message, and when it is missing", () => {
		expect(renderToStaticMarkup(<AnswerBlock answer={{ text: "42", source: "final_message", sha256: "" }} />)).toContain(
			"the agent&#x27;s final message (no answer file was written)",
		);
		const missing = renderToStaticMarkup(<AnswerBlock answer={{ text: "", source: "missing", sha256: "" }} />);
		expect(missing).toContain("missing: no answer file and no final message");
		expect(missing).not.toContain("<pre");
	});
});

describe("StateFiles and ServicesList", () => {
	it("lists exported state with size and hash, and a failed export's error", () => {
		const html = renderToStaticMarkup(<StateFiles files={toolRun.state!} arm="A" />);
		expect(html).toContain('aria-label="Exported state of setup A"');
		expect(html).toMatch(/data-testid="run-state-crm-tickets"[\s\S]*4.2 KB[\s\S]*55555555…5555/);
		expect(html).toContain("export failed: export larger than 16 MiB");
	});

	it("shows each service ready or not, with the error that kept it down", () => {
		const html = renderToStaticMarkup(<ServicesList services={toolRun.services!} arm="A" />);
		expect(html).toMatch(/data-testid="run-service-crm" data-ready="true"[\s\S]*>ready<[\s\S]*proxied/);
		expect(html).toMatch(/data-testid="run-service-ledger-db" data-ready="false"[\s\S]*not ready/);
		expect(html).toContain("ready check timed out after 60 s");
		expect(html).toContain("image postgres:16");
	});
});