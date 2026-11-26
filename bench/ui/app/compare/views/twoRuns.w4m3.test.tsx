import "@/lib/i18n";

import { okQuery, render } from "@/lib/testing/render";
import type { RunDetail } from "@/lib/types/bench";
import { describe, expect, it, vi } from "vitest";
import { TwoRuns, sharedPair } from "./twoRuns";

const detail = (id: string, pair: string, arm: string) =>
	({
		slot: { slot_id: `s_${id}`, pair_id: pair, exp_id: "e_1", arm, task: "t1", condition: "clean", trial: 1 },
		run: { run_id: id, slot_id: `s_${id}`, pair_id: pair, exp_id: "e_1", attempt: 1, outcome: "passed" },
		metrics: {},
	}) as unknown as RunDetail;

const runs = vi.hoisted(() => ({ byId: {} as Record<string, unknown> }));
vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("@/lib/store/apis/benchApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/benchApi")>()),
	useGetRunQuery: (id: string) => okQuery(runs.byId[id]),
}));

describe("sharedPair (p06-1)", () => {
	it("is the pair id only when both runs share it", () => {
		expect(sharedPair(detail("r1", "p1", "A"), detail("r2", "p1", "B"))).toBe("p1");
		expect(sharedPair(detail("r1", "p1", "A"), detail("r2", "p2", "B"))).toBeNull();
		expect(sharedPair(detail("r1", "", "A"), detail("r2", "", "B"))).toBeNull();
		expect(sharedPair(undefined, detail("r2", "p2", "B"))).toBeNull();
	});
});

describe("TwoRuns (p06-1)", () => {
	it("shows two runs of different pairs side by side", () => {
		runs.byId = { r1: detail("r1", "p1", "A"), r2: detail("r2", "p2", "B") };
		const html = render(<TwoRuns a="r1" b="r2" onPair={() => undefined} />);
		expect(html).toContain("Two runs, side by side");
		expect(html).toContain('href="/runs/r1"');
		expect(html).toContain('href="/runs/r2"');
	});
});