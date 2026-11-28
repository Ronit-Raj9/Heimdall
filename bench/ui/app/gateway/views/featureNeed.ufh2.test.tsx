import "@/lib/i18n";

import { render } from "@/lib/testing/render";
import type { GwNeed } from "@/lib/types/gateway";
import { describe, expect, it } from "vitest";
import { NeedView, needState } from "./featureNeed";

// The needs GET /gateway/features sends (bench/handlers/gatewaytests_ufh2_test.go checks the server side).
const overhead: GwNeed = {
	feature: "gateway_overhead",
	model_calls: 50,
	fixed_trials: 1,
	max_tasks: 25,
	calls_per_run: 4,
	calls_from: "default",
	default_tasks: 13,
	default_trials: 1,
};
const router: GwNeed = { feature: "complexity_router", runs_per_setup: 10, paired_tasks: 5, default_tasks: 5, default_trials: 2 };
const mcp: GwNeed = { feature: "mcp_tools", runs_per_setup: 10, paired_tasks: 5, poison_runs: 10, default_tasks: 10, default_trials: 1 };

describe("Feature test need", () => {
	it("warns, not refuses, the 3 task overhead test that cannot reach 50 calls", () => {
		const s = needState(overhead, 3, 1, false);
		expect(s.status).toBe("unlikely");
		const html = render(<NeedView state={s} />);
		expect(html).toContain("Needs about 50 model calls: about 13 tasks with this agent.");
		expect(html).toContain("4 calls per run assumed, no history yet.");
		expect(html).toContain("3 tasks make about 12 of the 50 model calls needed. Pick 13 tasks.");
		expect(html).not.toContain('role="alert"');
	});

	it("uses the agent's measured calls per run", () => {
		const html = render(
			<NeedView state={needState({ ...overhead, calls_per_run: 10, calls_from: "history", default_tasks: 5 }, 5, 1, false)} />,
		);
		expect(html).toContain("about 5 tasks with this agent.");
		expect(html).toContain("10 calls per run measured.");
		expect(html).not.toContain("feature-test-need-unlikely");
	});

	it("refuses a size that can never reach the runs or tasks needed", () => {
		const s = needState(router, 3, 1, false);
		expect(s.status).toBe("impossible");
		const html = render(<NeedView state={s} />);
		expect(html).toContain("Needs 10 runs per setup: 5 tasks, 2 trials.");
		expect(html).toContain("3 tasks cannot give a result: 5 are needed.");
		expect(html).toContain('role="alert"');
		expect(needState(router, 5, 1, false).status).toBe("impossible");
		expect(render(<NeedView state={needState(router, 5, 1, false)} />)).toContain("5 runs per setup cannot give a result: 10 are needed.");
	});

	it("the default size of each need reaches it", () => {
		for (const n of [overhead, router, mcp]) expect(needState(n, n.default_tasks, n.default_trials, true).status).toBe("ok");
		expect(render(<NeedView state={needState(mcp, 10, 1, true)} />)).toContain("Needs 10 runs per setup: 10 tasks, 1 trial.");
	});

	it("says nothing for a feature with no need", () => {
		expect(render(<NeedView state={needState(undefined, 3, 1, false)} />)).toBe("");
	});
});