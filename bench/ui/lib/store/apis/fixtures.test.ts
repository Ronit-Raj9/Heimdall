import {
	RECEIPT_STEPS,
	type Grid,
	type PairResponse,
	type RunEventsResponse,
	type RunReceiptsResponse,
	type RunResponse,
	type VerdictResponse,
} from "@/lib/types/bench";
import fs from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import { BENCH_API_BASE, FIXTURE_ROUTES, detectFixtureMode, resolveFixture } from "./fixtures";

const FIXTURE_DIR = path.resolve(__dirname, "../../../public/fixtures");
const readFixture = <T>(file: string): T => JSON.parse(fs.readFileSync(path.join(FIXTURE_DIR, file), "utf8")) as T;
const fixtureFiles = fs.readdirSync(FIXTURE_DIR).filter((file) => file.endsWith(".json"));

describe("detectFixtureMode", () => {
	const cases = [
		{ name: "query flag", search: "?fixture=1", env: undefined, want: true },
		{ name: "query flag among others", search: "?exp=e_x&fixture=1", env: undefined, want: true },
		{ name: "env flag", search: "", env: "1", want: true },
		{ name: "neither", search: "?fixture=0", env: "0", want: false },
		{ name: "empty", search: "", env: undefined, want: false },
	];
	for (const c of cases) {
		it(c.name, () => {
			expect(detectFixtureMode(c.search, c.env)).toBe(c.want);
		});
	}
});

describe("resolveFixture", () => {
	const cases = [
		{ method: "GET", url: "/health", want: "health.json" },
		{ method: "GET", url: `${BENCH_API_BASE}/catalog`, want: "catalog.json" },
		{ method: "POST", url: "/experiments/plan", want: "experiments.plan.json" },
		{ method: "POST", url: "/experiments", want: "experiments.create.json" },
		{ method: "post", url: "/experiments/e_x/start", want: "experiments.start.json" },
		{ method: "GET", url: "/experiments/e_x", want: "experiments.get.json" },
		{ method: "GET", url: "/experiments/e_x/verdict?x=1", want: "experiments.verdict.json" },
		{ method: "GET", url: "/runs/r_abc", want: "runs.r_abc.json" },
		{ method: "GET", url: "/runs/r_abc/events", want: "runs.r_abc.events.json" },
		{ method: "GET", url: "/pairs/p_abc", want: "pairs.p_abc.json" },
		{ method: "POST", url: "/audit", want: "audit.json" },
		{ method: "GET", url: "/experiments/plan/extra", want: null },
		{ method: "DELETE", url: "/health", want: null },
		{ method: "GET", url: "/runs/..%2F..%2Fsecret", want: null },
	];
	for (const c of cases) {
		it(`${c.method} ${c.url}`, () => {
			expect(resolveFixture(c.method, c.url)).toBe(c.want);
		});
	}
});

describe("fixture files", () => {
	it("cover every route", () => {
		for (const route of FIXTURE_ROUTES) {
			const [prefix, suffix] = route.file.split("{id}");
			const found = fixtureFiles.some((file) =>
				suffix === undefined ? file === prefix : file.startsWith(prefix) && file.endsWith(suffix),
			);
			expect(found, `${route.method} ${route.pattern} needs ${route.file}`).toBe(true);
		}
	});

	it("are all labelled synthetic", () => {
		for (const file of fixtureFiles) {
			expect(readFixture<{ mode: string }>(file).mode, file).toBe("synthetic");
		}
	});

	it("link the pair to run fixtures and grid cells", () => {
		const grid = readFixture<Grid>("experiments.grid.json");
		const pairFile = fixtureFiles.find((file) => file.startsWith("pairs."));
		expect(pairFile).toBeDefined();
		const pair = readFixture<PairResponse>(pairFile!);
		expect(pair.runs.map((run) => run.arm)).toEqual(["A", "B"]);
		for (const { run, slot } of pair.runs) {
			expect(slot.pair_id).toBe(pair.pair_id);
			const detail = readFixture<RunResponse>(`runs.${run.run_id}.json`);
			expect(detail.run.slot_id).toBe(slot.slot_id);
			for (const kind of ["events", "diff", "receipts"]) {
				expect(fixtureFiles).toContain(`runs.${run.run_id}.${kind}.json`);
			}
			const cell = grid.rows.flatMap((row) => row.cells).find((c) => c.run_id === run.run_id);
			expect(cell?.slot_id).toBe(slot.slot_id);
		}
	});

	it("follow the Go contract for verdicts, events and receipts", () => {
		const { verdict } = readFixture<VerdictResponse>("experiments.verdict.json");
		expect(["improved", "regressed", "inconclusive", "invalid"]).toContain(verdict.decision);
		expect(verdict.effect?.per_task).toHaveLength(verdict.effect?.n_tasks ?? -1);
		for (const file of fixtureFiles.filter((f) => f.endsWith(".events.json"))) {
			for (const event of readFixture<RunEventsResponse>(file).events) {
				expect(event.event_id).toBe(`${event.run_id}:${event.source}:${event.seq}`);
			}
		}
		for (const file of fixtureFiles.filter((f) => f.endsWith(".receipts.json"))) {
			for (const receipt of readFixture<RunReceiptsResponse>(file).receipts) {
				expect(Object.keys(receipt.steps).sort()).toEqual([...RECEIPT_STEPS].sort());
			}
		}
	});
});