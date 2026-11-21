import "@/lib/i18n";

import type { Grid, Triage, Value } from "@/lib/types/bench";
import fs from "node:fs";
import path from "node:path";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { runLocations } from "./triage.utils";
import { TriageDrillDown, TriageRunEvidence } from "./triageDrillDown";

// A plain anchor stands in for the router's Link, so the target URL is visible in the markup.
vi.mock("@tanstack/react-router", () => ({
	Link: ({ to, search, children, ...rest }: { to: string; search: { pair: string }; children: React.ReactNode }) => (
		<a href={`${to}?pair=${search.pair}`} {...rest}>
			{children}
		</a>
	),
}));

const fixtureDir = path.resolve(__dirname, "../../public/fixtures");
const triage = JSON.parse(fs.readFileSync(path.join(fixtureDir, "experiments.triage.json"), "utf8")) as Triage;
const grid = JSON.parse(fs.readFileSync(path.join(fixtureDir, "experiments.grid.json"), "utf8")) as Grid;
const loop = triage.groups.find((g) => g.pattern === "pattern_loop")!;

const reasonOf = (reason: string): Value => ({ state: "observed", v: 1, evidence: "gateway", reason });

describe("TriageDrillDown", () => {
	const reasons: Record<string, Value> = {
		r_qolshdu5jg: reasonOf(
			'events: r_qolshdu5jg:gateway:7,r_qolshdu5jg:gateway:9,r_qolshdu5jg:gateway:11; Bash "pytest -q" repeated 3 times in a row',
		),
	};
	const html = renderToStaticMarkup(
		<TriageDrillDown
			group={loop}
			arms={["A", "B"]}
			locations={runLocations(grid)}
			onClose={() => undefined}
			renderEvidence={(runId) => <TriageRunEvidence runId={runId} value={reasons[runId]} isLoading={false} isError={false} />}
		/>,
	);

	it("lists every run showing the pattern, arm by arm", () => {
		expect(html).toContain('data-testid="triage-drilldown-pattern_loop"');
		expect(html).toContain("Runs showing Loop");
		const rows = [...html.matchAll(/data-testid="triage-run-(r_[a-z0-9]+)"/g)].map((m) => m[1]);
		expect(rows).toEqual([...loop.runs.A, ...loop.runs.B]);
	});

	it("links each run to its matched pair from the grid", () => {
		// r_qolshdu5jg is t03 clean, arm A trial 3, pair p_gszjh69z4k in the grid fixture.
		expect(html).toMatch(/href="\/runs\?pair=p_gszjh69z4k"[^>]*data-testid="triage-compare-r_qolshdu5jg"/);
		expect(html).toMatch(/data-testid="triage-run-r_qolshdu5jg".*?t03.*?clean · attempt 3/);
	});

	it("shows the triggering event ids and the summary from the reason", () => {
		const evidence = html.match(/data-testid="triage-evidence-r_qolshdu5jg".*?<\/div>/)?.[0] ?? "";
		expect(evidence.match(/data-testid="triage-event-id"/g)).toHaveLength(3);
		expect(evidence).toContain(">r_qolshdu5jg:gateway:9<");
		expect(evidence).toContain("Bash &quot;pytest -q&quot; repeated 3 times in a row");
		expect(html).toMatch(/data-testid="triage-evidence-r_lsrv6xcv2u".*?No event ids recorded for this run\./);
	});

	it("says when a run is not in the grid and when nothing shows the pattern", () => {
		const lonely = { ...loop, runs: { A: ["r_missing"], B: [] } };
		const out = renderToStaticMarkup(
			<TriageDrillDown group={lonely} arms={["A", "B"]} locations={new Map()} onClose={() => undefined} renderEvidence={() => null} />,
		);
		expect(out).toContain('data-testid="triage-nopair-r_missing"');
		const empty = renderToStaticMarkup(
			<TriageDrillDown
				group={{ ...loop, runs: {} }}
				arms={["A", "B"]}
				locations={new Map()}
				onClose={() => undefined}
				renderEvidence={() => null}
			/>,
		);
		expect(empty).toContain('data-testid="triage-drilldown-empty"');
	});
});

describe("TriageRunEvidence", () => {
	const cases = [
		{ name: "loading", props: { value: undefined, isLoading: true, isError: false }, want: "Loading this run&#x27;s measurements" },
		{ name: "error", props: { value: undefined, isLoading: false, isError: true }, want: "Could not load this run&#x27;s measurements." },
		{
			name: "caps the list",
			props: {
				value: reasonOf(`events: ${Array.from({ length: 12 }, (_, i) => `r_x:gateway:${i + 1}`).join(",")}; repeated`),
				isLoading: false,
				isError: false,
			},
			want: "+2 more",
		},
	];
	for (const c of cases) {
		it(c.name, () => {
			expect(renderToStaticMarkup(<TriageRunEvidence runId="r_x" {...c.props} />)).toContain(c.want);
		});
	}
});