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
describe("EvaluationTable toolbar (J6.list)", () => {
	const make = (id: string, name: string, status: Evaluation["status"], created: string, bench: string) =>
		({ ...ev, id, spec: { ...ev.spec, name, benchmarks: [bench] }, status, created_at: created }) as Evaluation;
	const list = [
		make("ev_a", "codex score", "done", "2026-11-01T00:00:00Z", "bfcl"),
		make("ev_b", "codex score", "failed", "2026-11-02T00:00:00Z", "tau2"),
	];

	it("shows the toolbar and the benchmarks under each name", () => {
		const html = render(<EvaluationTable evaluations={list} />);
		expect(html).toContain('data-testid="evaluations-search"');
		expect(html).toContain('data-testid="evaluations-status-filter"');
		expect(html).toContain('data-testid="evaluations-sort"');
		expect(html).toMatch(/data-testid="evaluation-benchmarks-ev_a"[^>]*>bfcl</);
	});

	it("searches by benchmark and filters by status", () => {
		const base = { q: "", status: "all", mode: "all", sort: "newest" } as const;
		const searched = render(<EvaluationTable evaluations={list} filters={{ ...base, q: "tau2" }} />);
		expect(searched).toContain('data-testid="evaluation-row-ev_b"');
		expect(searched).not.toContain('data-testid="evaluation-row-ev_a"');
		const failed = render(<EvaluationTable evaluations={list} filters={{ ...base, status: "failed" }} />);
		expect(failed).toContain('data-testid="evaluation-row-ev_b"');
		expect(failed).not.toContain('data-testid="evaluation-row-ev_a"');
	});

	it("sorts oldest first", () => {
		const html = render(<EvaluationTable evaluations={list} filters={{ q: "", status: "all", mode: "all", sort: "oldest" }} />);
		expect(html.indexOf("evaluation-row-ev_a")).toBeLessThan(html.indexOf("evaluation-row-ev_b"));
	});

	it("says so when nothing matches", () => {
		const html = render(<EvaluationTable evaluations={list} filters={{ q: "nothing", status: "all", mode: "all", sort: "newest" }} />);
		expect(html).toContain("No evaluations match. Clear the search or filters.");
	});
});