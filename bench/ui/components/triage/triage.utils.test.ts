import type { Grid, Triage, TriageGroup } from "@/lib/types/bench";
import fs from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import { parseTriageReason, patternRuns, runLocations, share, shareDiffPoints, triageArms } from "./triage.utils";

const FIXTURE_DIR = path.resolve(__dirname, "../../public/fixtures");
const readFixture = <T>(file: string): T => JSON.parse(fs.readFileSync(path.join(FIXTURE_DIR, file), "utf8")) as T;

const group = (per_arm: TriageGroup["per_arm"], runs: TriageGroup["runs"] = {}): TriageGroup => ({
	pattern: "pattern_loop",
	label: "Loop",
	description: "",
	per_arm,
	runs,
});

describe("parseTriageReason", () => {
	const cases = [
		{
			name: "event ids and summary",
			reason: 'events: r_a:gateway:3,r_a:gateway:4; Bash "pytest -q" repeated 3 times in a row',
			want: { eventIds: ["r_a:gateway:3", "r_a:gateway:4"], summary: 'Bash "pytest -q" repeated 3 times in a row' },
		},
		{
			name: "summary with its own semicolons",
			reason: "events: r_a:agent_stream:2; success claimed after 1 of 1 failed tool results; first: Read",
			want: { eventIds: ["r_a:agent_stream:2"], summary: "success claimed after 1 of 1 failed tool results; first: Read" },
		},
		{ name: "ids only", reason: "events: r_a:gateway:1", want: { eventIds: ["r_a:gateway:1"], summary: "" } },
		{
			name: "observed 0 summary",
			reason: "no repeated tool call in 4 calls",
			want: { eventIds: [], summary: "no repeated tool call in 4 calls" },
		},
		{ name: "empty", reason: undefined, want: { eventIds: [], summary: "" } },
	];
	for (const c of cases) {
		it(c.name, () => {
			expect(parseTriageReason(c.reason)).toEqual(c.want);
		});
	}
});

describe("share and difference", () => {
	const cases = [
		{ name: "B higher", g: group({ A: { num: 1, den: 4, unknown: 0 }, B: { num: 3, den: 4, unknown: 2 } }), want: 50 },
		{ name: "B lower", g: group({ A: { num: 2, den: 4, unknown: 0 }, B: { num: 0, den: 2, unknown: 0 } }), want: -50 },
		{ name: "nothing observed in A", g: group({ A: { num: 0, den: 0, unknown: 5 }, B: { num: 1, den: 2, unknown: 0 } }), want: null },
		{ name: "arm missing", g: group({ A: { num: 0, den: 3, unknown: 0 } }), want: null },
	];
	for (const c of cases) {
		it(c.name, () => {
			expect(shareDiffPoints(c.g, "A", "B")).toBe(c.want);
		});
	}

	it("never turns unknowns into a 0 share", () => {
		expect(share({ num: 0, den: 0, unknown: 4 })).toBeNull();
		expect(share({ num: 0, den: 4, unknown: 4 })).toBe(0);
	});
});

describe("runs and locations", () => {
	it("lists runs arm by arm, sorted", () => {
		const g = group({}, { B: ["r_2", "r_1"], A: ["r_3"] });
		expect(patternRuns(g, ["A", "B"])).toEqual([
			{ arm: "A", runId: "r_3" },
			{ arm: "B", runId: "r_1" },
			{ arm: "B", runId: "r_2" },
		]);
		expect(triageArms([g])).toEqual(["A", "B"]);
	});

	it("links every fixture run to its grid pair", () => {
		const triage = readFixture<Triage>("experiments.triage.json");
		const grid = readFixture<Grid>("experiments.grid.json");
		expect(triage.exp_id).toBe(grid.exp_id);
		const locations = runLocations(grid);
		for (const g of triage.groups) {
			for (const [arm, runIds] of Object.entries(g.runs)) {
				expect(runIds.length, `${g.pattern} ${arm}`).toBe(g.per_arm[arm]?.num);
				for (const runId of runIds) {
					const where = locations.get(runId);
					expect(where?.arm, `${g.pattern} ${runId}`).toBe(arm);
					expect(where?.pairId).toMatch(/^p_/);
				}
			}
		}
	});
});