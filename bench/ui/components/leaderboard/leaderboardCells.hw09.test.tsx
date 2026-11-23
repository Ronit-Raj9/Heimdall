import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { ReliabilityCell, SuccessCell } from "./leaderboardCells";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

// Fix wave 3, N295 and N245: one definition per cell.
describe("leaderboard cells say one thing each", () => {
	it("counts tasks solved under the task mean, not runs", () => {
		// 8 tasks, one never solved, 11 runs with 10 passes: 87.5% is the mean over tasks.
		const html = renderToStaticMarkup(
			<SuccessCell
				entry={{
					rank: 1,
					success: { state: "observed", v: 0.875, bounds: [0.6, 1] },
					passes: { num: 10, den: 11, unknown: 0 },
					tasks: 8,
					tasks_solved: 7,
				}}
			/>,
		);
		expect(html).toContain("87.5%");
		expect(html).toContain("7 of 8 tasks solved");
		expect(html).not.toContain("10 of 11 solved");
	});

	it("says reliability was not measured with one try per task, never solved all 1 tries", () => {
		const html = renderToStaticMarkup(
			<ReliabilityCell
				entry={{
					rank: 1,
					reliability: { state: "unknown", v: null, reason: "pass^k needs 2 or more trials per task; its experiments ran 1" },
					trials: 1,
				}}
			/>,
		);
		expect(html).toContain(">Not measured<");
		expect(html).toContain("needs 2 or more tries per task; this ran 1");
		expect(html).not.toContain("solved all");
		expect(html).toContain('data-state="unknown"');
	});

	it("keeps solved all k tries under a known value", () => {
		const html = renderToStaticMarkup(<ReliabilityCell entry={{ rank: 1, reliability: { state: "observed", v: 0.5 }, trials: 3 }} />);
		expect(html).toContain(">solved all 3 tries<");
	});
});