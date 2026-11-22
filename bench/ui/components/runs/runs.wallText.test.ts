import { describe, expect, it } from "vitest";
import type { Value } from "@/lib/types/bench";
import { wallText } from "./runs.utils";

const obs = (v: number): Value => ({ state: "observed", v, evidence: "engine" }) as unknown as Value;

describe("wallText", () => {
	it("reads the observed wall_time_s metric first", () => {
		expect(wallText({ wall_time_s: obs(41) })).toBe("41.0 s");
		expect(wallText({ wall_time_s: obs(41), wall_ms: obs(5000) })).toBe("41.0 s");
	});
	it("falls back to the old metric names", () => {
		expect(wallText({ wall_ms: obs(2500) })).toBe("2.5 s");
		expect(wallText({ wall_s: obs(3) })).toBe("3.0 s");
	});
	it("falls back to the time from start to seal", () => {
		expect(wallText({}, { started: "2026-11-22T10:00:00Z", sealed: "2026-11-22T10:01:05Z" })).toBe("1m 05s");
		expect(
			wallText(
				{ wall_time_s: { state: "unknown", reason: "x", evidence: "engine" } as unknown as Value },
				{ started: "2026-11-22T10:00:00Z", sealed: "2026-11-22T10:00:41Z" },
			),
		).toBe("41.0 s");
	});
	it("shows a dash when nothing is known", () => {
		expect(wallText({})).toBe("-");
		expect(wallText({}, { started: "2026-11-22T10:00:00Z" })).toBe("-");
	});
});