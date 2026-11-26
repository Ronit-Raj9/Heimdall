import "@/lib/i18n";

import i18n from "@/lib/i18n";
import type { RunControls } from "@/lib/store/apis/runControlApi";
import { describe, expect, it } from "vitest";
import { controlsPatch } from "./runControls";

const controls = (over: Partial<RunControls> = {}): RunControls =>
	({
		exp_id: "e_1",
		state: { mode: "running", seq: 1 },
		paused: false,
		in_flight: 1,
		models: ["claude-sonnet"],
		concurrency: 4,
		total_usd: 0.4,
		policy: [],
		log: [],
		log_entries: 0,
		log_verified: true,
		...over,
	}) as RunControls;

describe("controlsPatch (p05-3)", () => {
	it("sends no spend cap when the box repeats the cap in effect", () => {
		const r = controlsPatch(controls(), "", "0.40", [], "");
		expect(r.patch).not.toHaveProperty("total_usd");
		expect(r.dirty).toBe(false);
	});
	it("sends a concurrency that differs from the one in effect", () => {
		const r = controlsPatch(controls(), "2", "", [], "");
		expect(r.patch).toEqual({ concurrency: 2 });
		expect(r.dirty).toBe(true);
	});
	it("does not send the concurrency already in effect", () => {
		expect(controlsPatch(controls(), "4", "", [], "").dirty).toBe(false);
	});
	it("says 1 change in the singular", () => {
		expect(i18n.t("controls.verified", { ns: "w4m3", count: 1 })).toBe("1 change, verified");
	});
});