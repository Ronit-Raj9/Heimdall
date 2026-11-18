import type { ExperimentStatus } from "@/lib/types/bench";
import { describe, expect, it } from "vitest";
import { filterExperiments } from "./experimentList";

const exp = (exp_id: string, name: string, frozen_at: string, extra: Partial<ExperimentStatus> = {}) =>
	({ exp_id, name, frozen_at, state: "done", suite: "coding-core", ...extra }) as ExperimentStatus;

const all = [
	exp("e_old", "smoke", "2026-11-10T00:00:00Z"),
	exp("e_new", "Frameworks", "2026-11-14T00:00:00Z", { state: "running" }),
	exp("e_mid", "memory-trap", "2026-11-12T00:00:00Z", { suite: "support-desk" }),
];

describe("filterExperiments", () => {
	it("sorts newest first with no filter", () => {
		expect(filterExperiments(all, {}).map((e) => e.exp_id)).toEqual(["e_new", "e_mid", "e_old"]);
	});
	it("matches the name or the id, ignoring case", () => {
		expect(filterExperiments(all, { q: "frame" }).map((e) => e.exp_id)).toEqual(["e_new"]);
		expect(filterExperiments(all, { q: "E_OLD" }).map((e) => e.exp_id)).toEqual(["e_old"]);
	});
	it("filters by state and suite", () => {
		expect(filterExperiments(all, { state: "running" }).map((e) => e.exp_id)).toEqual(["e_new"]);
		expect(filterExperiments(all, { suite: "support-desk" }).map((e) => e.exp_id)).toEqual(["e_mid"]);
	});
});