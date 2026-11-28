import "@/lib/i18n";

import { describe, expect, it, vi } from "vitest";
import { render } from "@/lib/testing/render";
import { ExtendPoolView } from "./extendPoolNote";
import type { ExtendPoolView as View } from "@/lib/types/extendPool";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

// The JSON GET /experiments/{id}/extensions sends (handlers/extendpool_n6.go).
const parent: View = {
	exp_id: "e_p",
	parent_pooled: false,
	extensions: [
		{ exp_id: "e_x1", name: "luna-ext", kind: "tasks", runs: 4, pooled: true },
		{ exp_id: "e_x2", name: "luna-ext-2", kind: "tasks", runs: 4, pooled: false, reason: "arm B config hash differs" },
	],
	pooled: {
		condition: "clean",
		metric: "verified_success",
		tasks: 4,
		runs: 8,
		added_runs: 4,
		mean_a: 0.25,
		mean_b: 0.75,
		d_bar: 0.5,
		interval: [-0.2, 1],
	},
};

describe("ExtendPoolView", () => {
	it("shows the pooled result, the runs added by Extend and why a follow-up is left out", () => {
		const html = render(<ExtendPoolView view={parent} />).replace(/<!-- -->/g, "");
		expect(html).toContain("Includes 4 runs added by Extend");
		expect(html).toContain("luna-ext");
		const result = html;
		expect(result).toContain("4 tasks, 8 runs");
		expect(result).toContain("25% vs 75%");
		expect(result).toContain("+50 points");
		expect(result).toContain("range -20 to +100");
		expect(html).toContain("arm B config hash differs");
	});

	it("tells a follow-up where its runs count", () => {
		const html = render(
			<ExtendPoolView view={{ exp_id: "e_x1", parent: "e_p", parent_pooled: true, extensions: [], pooled: null }} />,
		).replace(/<!-- -->/g, "");
		expect(html).toContain("Its runs count in");
	});

	it("says when a follow-up's runs do not count", () => {
		const html = render(
			<ExtendPoolView
				view={{ exp_id: "e_x2", parent: "e_p", parent_pooled: false, parent_reason: "suites differ", extensions: [], pooled: null }}
			/>,
		).replace(/<!-- -->/g, "");
		expect(html).toContain("not counted in e_p: suites differ");
	});
});