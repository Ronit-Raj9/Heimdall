import { describe, expect, it } from "vitest";
import { EMPTY_WATCH_FORM, evaluationLabels, utcOfDaily, watchSchedule } from "./modelWatchForm";

const ev = (id: string, created_at: string) => ({ id, created_at, spec: { name: "codex score", arms: [{ label: "A", agent: "codex", model: "gpt-6" }] } }) as never;

describe("model watch options (Q-293)", () => {
	it("no two evaluation options read the same", () => {
		const labels = evaluationLabels([ev("a", "2026-11-22T08:00:00Z"), ev("b", "2026-11-22T09:30:00Z"), ev("c", "2026-11-22T09:30:00Z"), ev("d", "2026-11-21T09:30:00Z")]);
		expect(labels.get("a")).toBe("codex score, 22 Nov, gpt-6, 08:00 UTC");
		expect(labels.get("b")).toBe("codex score, 22 Nov, gpt-6, 09:30 UTC (b)");
		expect(labels.get("d")).toBe("codex score, 21 Nov, gpt-6");
		expect(new Set(labels.values()).size).toBe(4);
	});
});

describe("model watch time zone (Q-272)", () => {
	it("gives the UTC time of the daily check and sends the zone", () => {
		expect(utcOfDaily("UTC")).toBeNull();
		expect(utcOfDaily("Asia/Kolkata", new Date("2026-11-27T00:00:00Z"))).toBe("00:30");
		const f = { ...EMPTY_WATCH_FORM, providers: "openai", evaluation: "ev", current: "A", usdPerModel: "1", maxUsdPerDay: "2", timezone: "Asia/Kolkata" };
		expect(watchSchedule(f, []).schedule?.timezone).toBe("Asia/Kolkata");
	});
});
