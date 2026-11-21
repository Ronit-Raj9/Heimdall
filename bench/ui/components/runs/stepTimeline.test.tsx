import "@/lib/i18n";

import { runStepsPath } from "@/lib/store/apis/runStepsApi";
import type { AgentEventStatus, RunSteps, StepRecord } from "@/lib/types/bench";
import fs from "node:fs";
import path from "node:path";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { AgentEventsLine, ObserverStatusLine, StepTimeline } from "./stepTimeline";
import {
	MAX_DEPTH,
	MAX_EVENT_ENTRIES,
	clipText,
	countedSteps,
	eventEntries,
	filterSteps,
	groupStepsByTurn,
	knownChannel,
	knownCorroboration,
	knownEventType,
	presentSources,
	safeCount,
	stepDepths,
	stepTime,
} from "./stepTimeline.utils";

const FIXTURES = path.resolve(__dirname, "../../public/fixtures");

const row = (over: Partial<StepRecord> & Pick<StepRecord, "seq" | "kind" | "source" | "turn">): StepRecord => ({
	ts: `2026-11-15T10:00:${String(over.seq).padStart(2, "0")}Z`,
	proof: "verified",
	summary: `row ${over.seq}`,
	...over,
});

const steps: StepRecord[] = [
	row({ seq: 1, kind: "command", source: "process_observer", turn: 0, proof: "strong", summary: "agent boot" }),
	row({ seq: 2, kind: "model_turn", source: "gateway", turn: 1, counted: true, summary: "model answered" }),
	row({
		seq: 3,
		kind: "tool_request",
		source: "gateway",
		turn: 1,
		tool_name: "javascript:alert(1)",
		summary: "<script>alert('pwned')</script><img src=x onerror=alert(2)>",
		execution: "executed_with_proof",
		witnessed_by: [4],
	}),
	row({ seq: 4, kind: "command", source: "process_observer", turn: 1, proof: "strong", summary: "pytest -q", exit_code: 1, witness_of: 3 }),
	row({
		seq: 5,
		kind: "tool_execution",
		source: "agent_stream",
		turn: 1,
		proof: "partial",
		summary: "pytest passed",
		witness_of: 3,
		conflict: "observer saw exit 1, agent reports exit 0",
	}),
	row({ seq: 6, kind: "file_change", source: "workspace_observer", turn: 1, proof: "strong", path: "<b>src/x.py</b>", op: "modify" }),
	row({ seq: 7, kind: "model_turn", source: "gateway", turn: 2, counted: true }),
	row({ seq: 8, kind: "tool_request", source: "gateway", turn: 2, tool_name: "fetch", execution: "no_witness" }),
];

const data: RunSteps = {
	run_id: "r_x",
	available: true,
	steps,
	observers: [
		{
			observer: "process",
			state: "observed",
			interval_ms: 50,
			observer_coverage: "full",
			records: 2,
			dropped: 3,
			pids_allocated: 5,
			pids_seen: 4,
		},
		{ observer: "workspace", state: "missing", reason: "inotify watch limit reached", records: 0 },
	],
};

describe("groupStepsByTurn", () => {
	it("groups by turn in first-seen order, model rows first", () => {
		const out = [...steps];
		// A model row that arrives after a row it caused still leads its group.
		out.splice(1, 2, steps[2], steps[1]);
		const groups = groupStepsByTurn(out);
		expect(groups.map((g) => g.turn)).toEqual([0, 1, 2]);
		expect(groups[1].model.map((s) => s.seq)).toEqual([2]);
		expect(groups[1].caused.map((s) => s.seq)).toEqual([3, 4, 5, 6]);
		expect(groups[0].model).toEqual([]);
	});

	it("keeps sessions apart", () => {
		const groups = groupStepsByTurn([
			row({ seq: 1, kind: "model_turn", source: "gateway", turn: 1, session: 1 }),
			row({ seq: 2, kind: "model_turn", source: "gateway", turn: 1, session: 2 }),
		]);
		expect(groups.map((g) => g.key)).toEqual(["1:1", "2:1"]);
	});
});

describe("filters and labels", () => {
	it("filters by source", () => {
		expect(filterSteps(steps, new Set()).length).toBe(steps.length);
		const noGateway = filterSteps(steps, new Set(["gateway"]));
		expect(noGateway.every((s) => s.source !== "gateway")).toBe(true);
		expect(noGateway.map((s) => s.seq)).toEqual([1, 4, 5, 6]);
	});

	it("lists present sources in display order, unknown ones last", () => {
		const extra = [...steps, row({ seq: 9, kind: "agent_event", source: "zz_new" as StepRecord["source"], turn: 2 })];
		expect(presentSources(extra)).toEqual(["gateway", "process_observer", "workspace_observer", "agent_stream", "zz_new"]);
	});

	it("counts the counted rows", () => {
		expect(countedSteps(steps)).toBe(2);
	});

	it("clips text and neutralises control and bidi characters", () => {
		expect(clipText("a".repeat(300), 10)).toBe(`${"a".repeat(9)}…`);
		expect(clipText("x‮yz\nnext")).toBe("x�yz next");
		expect(clipText(undefined)).toBe("");
		expect(stepTime("2026-11-15T10:00:05Z")).toBe("10:00:05");
		expect(stepTime("not a time")).toBe("not a time");
	});

	it("builds the request path with the id escaped", () => {
		expect(runStepsPath("r_abc")).toBe("/runs/r_abc/steps");
		expect(runStepsPath("../x")).toBe("/runs/..%2Fx/steps");
	});
});

describe("StepTimeline", () => {
	it("renders turns, badges, conflict and the steps label", () => {
		const html = renderToStaticMarkup(<StepTimeline data={data} arm="A" />);
		expect(html).toContain('data-testid="step-timeline-A"');
		expect(html).toContain("Actions Heimdall saw: 2");
		expect(html).toContain("Before the model&#x27;s first reply");
		expect(html).toContain("Model reply 1");
		expect(html).toContain("Model reply 2");
		// Turn 1's model row comes before the rows it caused.
		expect(html.indexOf('data-testid="step-row-A-2"')).toBeLessThan(html.indexOf('data-testid="step-row-A-3"'));
		expect(html).toContain('data-testid="step-row-A-3-execution"');
		expect(html).toContain("ran, Heimdall saw it");
		expect(html).toContain("no sign it ran");
		expect(html).not.toContain('data-testid="step-row-A-4-execution"');
		expect(html).toContain('data-testid="step-row-A-5-conflict"');
		expect(html).toContain("observer saw exit 1, agent reports exit 0");
		expect(html).toContain("confirmed by #4");
		expect(html).toContain("confirms #3");
		for (const label of ["Bifrost (model calls)", "command watcher", "file watcher", "agent&#x27;s own log"]) expect(html).toContain(label);
		expect(html).toContain("proof: verified");
		expect(html).toContain("8 of 8 rows shown");
	});

	it("renders agent-written strings as inert text", () => {
		const html = renderToStaticMarkup(<StepTimeline data={data} arm="A" />);
		expect(html).not.toContain("<script>");
		expect(html).not.toContain("<img");
		expect(html).not.toContain("<b>");
		expect(html).toContain("&lt;script&gt;alert(&#x27;pwned&#x27;)&lt;/script&gt;");
		expect(html).toContain("&lt;b&gt;src/x.py&lt;/b&gt;");
		expect(html).not.toContain("href=");
		expect(html).toContain("javascript:alert(1)");
	});

	it("hides the rows of switched-off sources", () => {
		const html = renderToStaticMarkup(<StepTimeline data={data} arm="A" initialHidden={["gateway"]} />);
		expect(html).not.toContain('data-testid="step-row-A-2"');
		expect(html).not.toContain('data-testid="step-row-A-8"');
		expect(html).toContain('data-testid="step-row-A-4"');
		expect(html).toContain("4 of 8 rows shown");
		expect(html).toMatch(/aria-pressed="false"[^>]*data-testid="step-filter-A-gateway"/);
		expect(html).toMatch(/aria-pressed="true"[^>]*data-testid="step-filter-A-process_observer"/);
		// Turn 2 has only gateway rows, so its group is gone.
		expect(html).not.toContain('data-testid="step-turn-A-0-2"');

		const none = renderToStaticMarkup(
			<StepTimeline data={data} arm="A" initialHidden={["gateway", "process_observer", "workspace_observer", "agent_stream"]} />,
		);
		expect(none).toContain('data-testid="step-timeline-A-filtered"');
		expect(none).toContain("No rows from the selected sources.");
	});

	it("says when a run has no timeline", () => {
		const html = renderToStaticMarkup(<StepTimeline data={{ run_id: "r_old", available: false, steps: [], observers: [] }} arm="B" />);
		expect(html).toContain('data-testid="step-timeline-B-unavailable"');
		expect(html).toContain("No step-by-step record for this run");
		expect(html).not.toContain("step-filter-");
	});

	it("reports truncation and skipped lines", () => {
		const html = renderToStaticMarkup(<StepTimeline data={{ ...data, truncated: true, skipped_lines: 2 }} arm="A" />);
		expect(html).toContain('data-testid="step-timeline-A-truncated"');
		expect(html).toContain("2 lines of the timeline file did not parse");
	});

	it("renders the fixtures", () => {
		const read = (file: string) => JSON.parse(fs.readFileSync(path.join(FIXTURES, file), "utf8")) as RunSteps;
		const withSteps = renderToStaticMarkup(<StepTimeline data={read("runs.r_fmw1z7ouim.steps.json")} arm="A" />);
		expect(withSteps).toContain("Actions Heimdall saw: 3");
		expect(withSteps).toContain('data-testid="step-row-A-12-reported"');
		expect(withSteps).toMatch(/data-testid="step-row-A-14"[^>]*data-depth="2"/);
		const missing = renderToStaticMarkup(<StepTimeline data={read("runs.r_pww2l7yame.steps.json")} arm="B" />);
		expect(missing).toContain("Actions Heimdall saw: 2");
		expect(missing).toContain("inotify watch limit reached");
	});
});

describe("ObserverStatusLine", () => {
	it("shows state, interval, coverage, drops and the missing reason", () => {
		const html = renderToStaticMarkup(<ObserverStatusLine observers={data.observers} arm="A" locale="en" />);
		expect(html).toContain("Command watcher");
		expect(html).toContain("every 50 ms");
		expect(html).toContain("full");
		expect(html).toContain("3 dropped");
		expect(html).toContain("saw 4 of 5 processes");
		expect(html).toContain('data-state="missing"');
		expect(html).toContain("inotify watch limit reached");
		expect(html).toMatch(/data-testid="step-observer-A-process-0"[^>]*data-degraded="true"/);
	});

	it("says when no observer status was recorded", () => {
		const html = renderToStaticMarkup(<ObserverStatusLine observers={[]} arm="A" locale="en" />);
		expect(html).toContain("No record of how Heimdall watched this run.");
	});
});

describe("agent-reported rows", () => {
	const reported = (over: Partial<StepRecord> & Pick<StepRecord, "seq" | "kind" | "turn">): StepRecord =>
		row({ source: "agent_reported", proof: "partial", channel: "otlp", ...over });

	const withReports: StepRecord[] = [
		...steps,
		reported({ seq: 9, kind: "agent_event", turn: 2, event_type: "plan", report_id: "span-1", corroboration: "agent_reported_only" }),
		reported({ seq: 10, kind: "agent_event", turn: 2, event_type: "subagent_start", parent_seq: 9, channel: "native" }),
		reported({
			seq: 11,
			kind: "tool_execution",
			turn: 2,
			event_type: "tool_call",
			parent_seq: 10,
			corroboration: "corroborated",
			witness_of: 8,
		}),
		reported({
			seq: 12,
			kind: "agent_event",
			turn: 2,
			event_type: "<img src=x onerror=alert(1)>{{count}}",
			report_id: "javascript:alert(3)",
			parent_seq: 11,
			corroboration: "conflicts",
			channel: "<b>pigeon</b>",
			summary: "<script>alert('sub')</script>",
		}),
	];
	const reportData: RunSteps = { ...data, steps: withReports };

	it("computes nesting depth from the parent chain", () => {
		const depths = stepDepths(withReports);
		expect(depths.get(1)).toBe(0);
		expect(depths.get(9)).toBe(0);
		expect(depths.get(10)).toBe(1);
		expect(depths.get(11)).toBe(2);
		expect(depths.get(12)).toBe(3);
	});

	it("guards depth against cycles, missing parents and long chains", () => {
		const cycle = stepDepths([
			reported({ seq: 1, kind: "agent_event", turn: 0, parent_seq: 2 }),
			reported({ seq: 2, kind: "agent_event", turn: 0, parent_seq: 1 }),
			reported({ seq: 3, kind: "agent_event", turn: 0, parent_seq: 3 }),
		]);
		expect(cycle.get(1)).toBe(1);
		expect(cycle.get(2)).toBe(1);
		expect(cycle.get(3)).toBe(0);

		const missing = stepDepths([reported({ seq: 5, kind: "agent_event", turn: 0, parent_seq: 99 })]);
		expect(missing.get(5)).toBe(0);

		const chain = Array.from({ length: 20 }, (_, i) =>
			reported({ seq: i + 1, kind: "agent_event", turn: 0, parent_seq: i === 0 ? undefined : i }),
		);
		const deep = stepDepths(chain);
		expect(deep.get(1)).toBe(0);
		expect(deep.get(5)).toBe(4);
		expect(deep.get(20)).toBe(MAX_DEPTH);
	});

	it("knows only the listed types, channels and corroboration states", () => {
		expect(knownEventType("subagent_end")).toBe("subagent_end");
		expect(knownEventType("llm")).toBeUndefined();
		expect(knownEventType("toString")).toBeUndefined();
		expect(knownChannel("stdout")).toBe("stdout");
		expect(knownChannel("carrier pigeon")).toBeUndefined();
		expect(knownCorroboration("conflicts")).toBe("conflicts");
		expect(knownCorroboration("toString")).toBeUndefined();
		expect(knownCorroboration(undefined)).toBeUndefined();
	});

	it("lists agent_reported last in the source filter and filters it", () => {
		expect(presentSources(withReports)).toEqual(["gateway", "process_observer", "workspace_observer", "agent_stream", "agent_reported"]);
		expect(filterSteps(withReports, new Set(["agent_reported"])).map((s) => s.seq)).toEqual([1, 2, 3, 4, 5, 6, 7, 8]);

		const html = renderToStaticMarkup(<StepTimeline data={reportData} arm="A" />);
		expect(html).toMatch(/aria-pressed="true"[^>]*data-testid="step-filter-A-agent_reported"/);
		expect(html).toContain("12 of 12 rows shown");

		const hidden = renderToStaticMarkup(<StepTimeline data={reportData} arm="A" initialHidden={["agent_reported"]} />);
		expect(hidden).toMatch(/aria-pressed="false"[^>]*data-testid="step-filter-A-agent_reported"/);
		expect(hidden).not.toContain('data-testid="step-row-A-9"');
		expect(hidden).toContain('data-testid="step-row-A-8"');
		expect(hidden).toContain("8 of 12 rows shown");
	});

	it("renders the reported badge, channel, corroboration and nesting", () => {
		const html = renderToStaticMarkup(<StepTimeline data={reportData} arm="A" />);
		// Distinct from the agent stream badge: its own testid, dashed, with the channel.
		expect(html).toMatch(/class="[^"]*border-dashed[^"]*"[^>]*data-testid="step-row-A-9-reported"/);
		expect(html).not.toContain('data-testid="step-row-A-5-reported"');
		expect(html).toContain("agent says");
		expect(html).toMatch(/data-testid="step-row-A-9-reported-channel">OTLP</);
		expect(html).toMatch(/data-testid="step-row-A-10-reported-channel">native</);
		expect(html).toMatch(/data-testid="step-row-A-9-event-type">plan</);
		expect(html).toMatch(/data-testid="step-row-A-10-event-type">subagent start</);
		expect(html).toMatch(/data-testid="step-row-A-9-corroboration" data-tone="warn"/);
		expect(html).toContain("agent&#x27;s word only");
		expect(html).toMatch(/data-testid="step-row-A-11-corroboration" data-tone="good"/);
		expect(html).toMatch(/data-testid="step-row-A-12-corroboration" data-tone="bad"/);
		expect(html).not.toContain('data-testid="step-row-A-10-corroboration"');
		expect(html).toMatch(/data-testid="step-row-A-9"[^>]*data-depth="0"/);
		expect(html).toMatch(/style="margin-left:1.25rem"[^>]*data-testid="step-row-A-10"[^>]*data-depth="1"/);
		expect(html).toMatch(/style="margin-left:3.75rem"[^>]*data-testid="step-row-A-12"[^>]*data-depth="3"/);
		expect(html).toContain("inside #11");
		expect(html).toMatch(/data-testid="step-row-A-9-report-id">report id <span[^>]*>span-1</);
		// Reported rows are not counted (the label of the count is tested above).
		expect(countedSteps(withReports)).toBe(countedSteps(steps));
		expect(html).not.toMatch(/data-testid="step-row-A-9"[^>]*>(?:(?!<\/li>).)*counted</);
	});

	it("renders agent-chosen type, channel and id as inert text", () => {
		const html = renderToStaticMarkup(<StepTimeline data={reportData} arm="A" />);
		expect(html).not.toContain("<img");
		expect(html).not.toContain("<b>");
		expect(html).not.toContain("<script>");
		expect(html).not.toContain("href=");
		expect(html).toContain("&lt;img src=x onerror=alert(1)&gt;{{count}}");
		expect(html).toContain("&lt;b&gt;pigeon&lt;/b&gt;");
		expect(html).toContain("javascript:alert(3)");
		expect(html).toContain("&lt;script&gt;alert(&#x27;sub&#x27;)&lt;/script&gt;");
	});
});

describe("agent events status", () => {
	it("shows nothing when the run carries no status", () => {
		const html = renderToStaticMarkup(<StepTimeline data={data} arm="A" />);
		expect(html).not.toContain("step-agent-events-");
		expect(html).not.toContain("The agent reported nothing");
	});

	it("summarises kept events, channels, types and drops, marked agent-reported", () => {
		const status: AgentEventStatus = {
			collector: "on",
			received: 9,
			kept: 7,
			dropped: 2,
			channels: { native: 2, otlp: 5, "<b>pigeon</b>": 0 },
			kinds: { tool_call: 4, plan: 1, "{{count}}<img src=x>": 2 },
		};
		const html = renderToStaticMarkup(<StepTimeline data={{ ...data, agent_events: status }} arm="A" />);
		expect(html).toMatch(/data-testid="step-agent-events-A" data-collector="on" data-kept="7"/);
		expect(html).toMatch(/class="[^"]*border-dashed[^"]*"[^>]*data-testid="step-agent-events-A-badge"/);
		expect(html).toContain("Agent reported 7 events");
		expect(html).toMatch(/data-testid="step-agent-events-A-channels">via <span[^>]*>OTLP 5, native 2</);
		expect(html).toMatch(/data-testid="step-agent-events-A-kinds">types <span[^>]*>tool call 4, \{\{count\}\}&lt;img src=x&gt; 2, plan 1</);
		expect(html).toMatch(/data-testid="step-agent-events-A-dropped">2 dropped</);
		expect(html).not.toContain("pigeon");
		expect(html).not.toContain("<img");
		expect(html).not.toContain('data-testid="step-agent-events-A-collector"');
		expect(html).not.toContain("The agent reported nothing");
		// The status line sits above the rows.
		expect(html.indexOf("step-agent-events-A")).toBeLessThan(html.indexOf('data-testid="step-row-A-1"'));

		const one = renderToStaticMarkup(<AgentEventsLine status={{ collector: "on", received: 1, kept: 1 }} arm="B" locale="en" />);
		expect(one).toContain("Agent reported 1 event<");
		expect(one).not.toContain("step-agent-events-B-dropped");
	});

	it("says when the agent reported nothing", () => {
		const html = renderToStaticMarkup(<StepTimeline data={{ ...data, agent_events: { collector: "on", received: 0, kept: 0 } }} arm="A" />);
		expect(html).toContain("The agent reported nothing about itself (only Heimdall&#x27;s own records shown)");
		expect(html).not.toContain("Agent reported 0");
		expect(html).not.toContain("step-agent-events-A-channels");
		expect(html).not.toContain("step-agent-events-A-reason");
	});

	it("adds the reason, as plain text, when the collector is missing", () => {
		const status: AgentEventStatus = {
			collector: "missing",
			reason: "<script>alert('r')</script> port 4318 busy\nnext",
			received: 0,
			kept: 0,
		};
		const html = renderToStaticMarkup(<AgentEventsLine status={status} arm="A" locale="en" />);
		expect(html).toContain('data-collector="missing"');
		expect(html).toContain("The agent reported nothing");
		expect(html).toMatch(/data-testid="step-agent-events-A-collector" data-tone="bad">listener missing</);
		expect(html).toContain("&lt;script&gt;alert(&#x27;r&#x27;)&lt;/script&gt; port 4318 busy next");
		expect(html).not.toContain("<script>");
		expect(html).toMatch(/class="text-chart-error-ink" data-testid="step-agent-events-A-reason"/);
	});

	it("orders and caps the per-channel counts", () => {
		expect(eventEntries(undefined)).toEqual({ entries: [], rest: 0 });
		expect(eventEntries({ stdout: 1, otlp: 1, zz: 1, native: 3, bad: "x" as unknown as number, neg: -2 })).toEqual({
			entries: [
				["native", 3],
				["otlp", 1],
				["stdout", 1],
				["zz", 1],
			],
			rest: 0,
		});
		const many = Object.fromEntries(Array.from({ length: MAX_EVENT_ENTRIES + 3 }, (_, i) => [`k${i}`, 1]));
		expect(eventEntries(many).entries.length).toBe(MAX_EVENT_ENTRIES);
		expect(eventEntries(many).rest).toBe(3);
		expect(safeCount(Number.NaN)).toBe(0);
		expect(safeCount(4.7)).toBe(4);
	});
});