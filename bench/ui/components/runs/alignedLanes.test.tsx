import "@/lib/i18n";

import type { AlignedStep, BenchEvent, PairResponse, RunEventsResponse } from "@/lib/types/bench";
import fs from "node:fs";
import path from "node:path";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { AlignedLanes } from "./alignedLanes";
import { indexEvents } from "./runs.utils";

const FIXTURES = path.resolve(__dirname, "../../public/fixtures");
const read = <T,>(file: string): T => JSON.parse(fs.readFileSync(path.join(FIXTURES, file), "utf8")) as T;

const pair = read<PairResponse>("pairs.p_ltosmymyqy.json");
const eventsA = read<RunEventsResponse>("runs.r_fmw1z7ouim.events.json").events;
const eventsB = read<RunEventsResponse>("runs.r_pww2l7yame.events.json").events;

function render(overrides: Partial<Parameters<typeof AlignedLanes>[0]> = {}): string {
	return renderToStaticMarkup(
		<AlignedLanes
			alignment={pair.alignment}
			arms={["A", "B"]}
			eventsByArm={{ A: indexEvents(eventsA), B: indexEvents(eventsB) }}
			firstDivergence={pair.first_divergence}
			{...overrides}
		/>,
	);
}

describe("AlignedLanes", () => {
	it("renders one row per aligned step with both lanes", () => {
		const html = render();
		for (const step of pair.alignment) {
			expect(html).toContain(`data-testid="aligned-step-${step.index}"`);
			expect(html).toContain(`data-testid="aligned-step-${step.index}-A"`);
			expect(html).toContain(`data-testid="aligned-step-${step.index}-B"`);
		}
		expect(html).toContain(">Setup A<");
		expect(html).toContain(">Setup B<");
	});

	it("marks the first observed divergence as an observation, right before its step", () => {
		const html = render();
		expect(html.match(/data-testid="first-divergence-marker"/g)).toHaveLength(1);
		expect(html).toContain(">First divergence<");
		expect(html).toContain(">Where they split, not why.<");
		expect(html).toContain(pair.first_divergence!.note);
		const marker = html.indexOf('data-testid="first-divergence-marker"');
		const step = html.indexOf(`data-testid="aligned-step-${pair.first_divergence!.index}"`);
		const before = html.indexOf(`data-testid="aligned-step-${pair.first_divergence!.index - 1}"`);
		expect(before).toBeLessThan(marker);
		expect(marker).toBeLessThan(step);
		expect(html).toMatch(/data-testid="aligned-step-6"[^>]*data-diverged="true"[^>]*data-first-divergence="true"/);
		expect(html).toMatch(/data-testid="aligned-step-0"[^>]*data-diverged="false"/);
	});

	it("shows the event text of each lane and an empty cell where a lane has no event", () => {
		const html = render();
		expect(html).toContain("grep -n parse_date src/dates.py tests/");
		expect(html).toContain("edit src/dates.py");
		expect(html).toMatch(/data-testid="aligned-step-14-A"[^>]*><span[^>]*data-state="empty"[^>]*>no step</);
	});

	it("shows no marker without a divergence", () => {
		const html = render({ firstDivergence: null });
		expect(html).not.toContain("first-divergence-marker");
	});

	it("falls back to the event id while a lane's events load", () => {
		const html = render({ eventsByArm: { A: undefined, B: undefined } });
		expect(html).toContain(">r_fmw1z7ouim:gateway:1<");
	});

	it("renders event text inert, never as HTML", () => {
		const step: AlignedStep = { index: 0, type: "tool_call", target: "<img src=x>", events: { A: "r:gateway:1", B: null }, diverged: true };
		const event: BenchEvent = {
			event_id: "r:gateway:1",
			run_id: "r",
			slot_id: "s",
			pair_id: "p",
			seq: 1,
			turn_index: 0,
			ts: "2026-11-14T00:00:00Z",
			type: "tool_call",
			source: "gateway",
			evidence: "gateway",
			summary: "<script>alert(1)</script>",
		};
		const html = render({ alignment: [step], eventsByArm: { A: new Map([[event.event_id, event]]), B: new Map() }, firstDivergence: null });
		expect(html).not.toContain("<script>");
		expect(html).not.toContain("<img src=x>");
		expect(html).toContain("&lt;script&gt;alert(1)&lt;/script&gt;");
	});
});