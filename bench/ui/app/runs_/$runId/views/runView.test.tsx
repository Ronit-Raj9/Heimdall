import "@/lib/i18n";

import { okQuery, render } from "@/lib/testing/render";
import type { RunDetail } from "@/lib/types/bench";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { RunCompareButton, RunStatCards, stepParam } from "./runView";

const api = vi.hoisted(() => ({ experiment: {} as unknown }));
vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("@/lib/store/apis/benchApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/benchApi")>()),
	useGetExperimentQuery: () => api.experiment,
}));

const detail = (debug?: RunDetail["run"]["debug"]) =>
	({
		sealed: true,
		slot: { exp_id: "e_1", pair_id: "p_1", condition: "clean", arm: "A", task: "t1" },
		run: { run_id: "r_1", debug, timing: {} },
		metrics: { steps: { state: "observed", v: 2, evidence: "gateway" } },
	}) as unknown as RunDetail;

beforeEach(() => {
	api.experiment = okQuery({ exp_id: "e_1", arms: ["A", "B"] });
});

describe("run page compare action (N27)", () => {
	it("compares Setup A and B for a two-setup experiment", () => {
		const html = render(<RunCompareButton detail={detail()} />);
		expect(html).toContain('data-testid="run-compare"');
		expect(html).toContain("/compare?");
	});
	it("offers no comparison for a one-setup experiment", () => {
		api.experiment = okQuery({ exp_id: "e_1", arms: ["A"] });
		expect(render(<RunCompareButton detail={detail()} />)).toBe("");
	});
	it("offers Compare with source, not Setup A and B, for a debug run", () => {
		const html = render(<RunCompareButton detail={detail({ mode: "replay", from_run: "r_0" })} />);
		expect(html).not.toContain('data-testid="run-compare"');
		expect(html).toContain('data-testid="run-compare-source"');
		expect(html).toContain("Compare with source");
		expect(html).toContain("tab=timeline");
	});
});

describe("run overview stats (N28)", () => {
	it("calls the steps metric Model turns and explains the count", () => {
		const html = render(<RunStatCards detail={detail()} />);
		expect(html).toContain("Model turns");
		expect(html).toContain("Step by step also counts tools.");
	});
});

describe("run overview stats beside the pair (M5-21)", () => {
	it("shows the other setup's value as a muted line, and tiles have no hover shadow (X-40)", () => {
		const other = { arm: "B", metrics: { cost_usd: { state: "observed", v: 0.0034 } }, run: { timing: {} } } as never;
		const html = render(<RunStatCards detail={detail()} others={[other]} />);
		expect(html).toContain("Setup B: $0.0034");
		expect(html).not.toContain("hover:shadow-md");
	});
});

describe("stepParam (N51)", () => {
	it("keeps a positive whole step only", () => {
		expect(stepParam({ step: 4 })).toBe(4);
		expect(stepParam({ step: "12" })).toBe(12);
		for (const bad of [0, -1, 1.5, "x", "3a", undefined]) expect(stepParam({ step: bad })).toBeUndefined();
	});
});