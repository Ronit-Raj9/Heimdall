import { describe, expect, it } from "vitest";
import { modelChoice, runLine } from "./weeklySync";

const t = ((key: string, o?: Record<string, unknown>) => `${key} ${JSON.stringify(o ?? {})}`) as never;

describe("weekly sync", () => {
	it("splits provider and model, empty is the default", () => {
		expect(modelChoice("openai/gpt-5.4-mini")).toEqual({ provider: "openai", model: "gpt-5.4-mini" });
		expect(modelChoice("")).toEqual({});
		expect(modelChoice("openai")).toEqual({});
	});
	it("shows counts for a started run and the reason for a refused one", () => {
		const started = runLine({ at: "2026-11-30T03:00:00Z", started: 2, skipped: 1, spent_usd: 1.5, status: "started" }, t);
		expect(started).toContain("sync.runRow");
		expect(started).toContain('"spent":"1.50"');
		const refused = runLine({ at: "2026-11-30T03:00:00Z", started: 0, skipped: 0, spent_usd: 0, status: "refused", reason: "gone" }, t);
		expect(refused).toContain('"reason":"gone"');
	});
});