import "@/lib/i18n";

import { render } from "@/lib/testing/render";
import type { Evaluation } from "@/lib/store/apis/evaluationsApi";
import { describe, expect, it, vi } from "vitest";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("@/lib/store/apis/evaluationsApi", async (orig) => ({ ...(await orig<object>()), useListEvaluationsQuery: vi.fn() }));

const { EvaluationTable } = await import("./evaluationListView");

const ev: Evaluation = {
	id: "ev_1",
	spec: {
		name: "Codex nightly",
		mode: "models",
		arms: [
			{ label: "A", agent: "codex", version: 2, model: "m1" },
			{ label: "B", agent: "codex", version: 2, model: "m2" },
		],
		benchmarks: ["a", "b"],
		tasks_per_benchmark: 5,
		trials: 2,
		faults: [],
	},
	status: "running",
	children: [],
	created_at: "2026-11-20T10:00:00Z",
};

describe("EvaluationTable", () => {
	it("shows name, agent, mode in plain words, count, status and a link", () => {
		const html = render(<EvaluationTable evaluations={[ev]} />);
		expect(html).toContain("Codex nightly");
		expect(html).toContain("codex v2");
		expect(html).toContain("Compare two models");
		expect(html).toContain("2 benchmarks");
		expect(html).toContain("Running");
		expect(html).toContain('href="/evaluations/ev_1"');
	});

	it("says so when empty", () => {
		expect(render(<EvaluationTable evaluations={[]} />)).toContain('data-testid="evaluations-empty"');
	});
});