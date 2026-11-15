import "@/lib/i18n";

import { runStepsPath } from "@/lib/store/apis/runStepsApi";
import type { RunSteps, StepRecord } from "@/lib/types/bench";
import fs from "node:fs";
import path from "node:path";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { ObserverStatusLine, StepTimeline } from "./stepTimeline";
import { clipText, countedSteps, filterSteps, groupStepsByTurn, presentSources, stepTime } from "./stepTimeline.utils";

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
		expect(html).toContain("Steps (model turns): 2");
		expect(html).toContain("Before the first model response");
		expect(html).toContain("Turn 1");
		expect(html).toContain("Turn 2");
		// Turn 1's model row comes before the rows it caused.
		expect(html.indexOf('data-testid="step-row-A-2"')).toBeLessThan(html.indexOf('data-testid="step-row-A-3"'));
		expect(html).toContain('data-testid="step-row-A-3-execution"');
		expect(html).toContain("executed, witnessed");
		expect(html).toContain("no witness");
		expect(html).not.toContain('data-testid="step-row-A-4-execution"');
		expect(html).toContain('data-testid="step-row-A-5-conflict"');
		expect(html).toContain("observer saw exit 1, agent reports exit 0");
		expect(html).toContain("witnessed by #4");
		expect(html).toContain("witnesses #3");
		for (const label of ["gateway", "process observer", "workspace observer", "agent stream"]) expect(html).toContain(label);
		expect(html).toContain("proof verified");
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
		expect(html).toContain("No step timeline recorded for this run");
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
		expect(withSteps).toContain("Steps (model turns): 3");
		const missing = renderToStaticMarkup(<StepTimeline data={read("runs.r_pww2l7yame.steps.json")} arm="B" />);
		expect(missing).toContain("Steps (model turns): 2");
		expect(missing).toContain("inotify watch limit reached");
	});
});

describe("ObserverStatusLine", () => {
	it("shows state, interval, coverage, drops and the missing reason", () => {
		const html = renderToStaticMarkup(<ObserverStatusLine observers={data.observers} arm="A" locale="en" />);
		expect(html).toContain("Process observer");
		expect(html).toContain("every 50 ms");
		expect(html).toContain("full");
		expect(html).toContain("3 dropped");
		expect(html).toContain("4 of 5 PIDs seen");
		expect(html).toContain('data-state="missing"');
		expect(html).toContain("inotify watch limit reached");
		expect(html).toMatch(/data-testid="step-observer-A-process-0"[^>]*data-degraded="true"/);
	});

	it("says when no observer status was recorded", () => {
		const html = renderToStaticMarkup(<ObserverStatusLine observers={[]} arm="A" locale="en" />);
		expect(html).toContain("No observer status recorded for this run.");
	});
});