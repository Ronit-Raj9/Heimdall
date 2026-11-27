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
		// No Benchmarks column (M3-51): the benchmarks are listed under the name.
		expect(html).toContain('data-testid="evaluation-benchmarks-ev_1"');
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
describe("EvaluationTable result and spend (N161)", () => {
	it("shows the stored result and spend, and a failure test as such", () => {
		const done: Evaluation = {
			...ev,
			id: "ev_ft",
			status: "done",
			spec: { ...ev.spec, name: "codex failure test on bfcl", mode: "score", arms: [ev.spec.arms[0]], faults: ["clean", "model-down"] },
			result: {
				arms: [{ label: "A", agent: "codex", version: 2, model: "m1", score: 0.56, runs: 30 }],
				cost_usd: 1.5,
				at: "2026-11-23T00:00:00Z",
			},
		};
		const html = render(<EvaluationTable evaluations={[done, ev]} />);
		expect(html).toContain(">Result<");
		expect(html).toContain(">Spend<");
		expect(html).toContain('data-testid="evaluation-result-ev_ft">56%');
		expect(html).toContain('data-testid="evaluation-spend-ev_ft">$1.50');
		expect(html).toContain('data-testid="evaluation-result-ev_1">-');
		expect(html).toContain("Failure test");
		expect(html).not.toContain("Score one agent");
	});
});
describe("EvaluationTable scheduled repeats (fix wave 3, N297)", () => {
	it("marks a scheduled repeat and adds the start date when two rows share a name", () => {
		const first = { ...ev, id: "ev_a", status: "done", created_at: "2026-11-21T10:00:00Z" } as Evaluation;
		const repeat = { ...ev, id: "ev_b", status: "done", schedule: "nightly", created_at: "2026-11-22T10:00:00Z" } as Evaluation;
		const other = { ...ev, id: "ev_c", status: "done", spec: { ...ev.spec, name: "Alone" } } as Evaluation;
		const html = render(<EvaluationTable evaluations={[first, repeat, other]} />);
		expect(html).toContain('data-testid="evaluation-scheduled-ev_b"');
		expect(html).not.toContain('data-testid="evaluation-scheduled-ev_a"');
		// No inline date in the name (M3-51): every row has Started; a repeat says Nightly there.
		expect(html).not.toContain("evaluation-name-date-");
		// Nightly sits in the repeat's own row, in its Started cell, and in no other row.
		expect(html).toMatch(/data-testid="evaluation-scheduled-ev_b"[^>]*>(<svg[^]*?<\/svg>)?Nightly<\/span>/);
		const rowOf = (id: string) => html.slice(html.indexOf(`data-testid="evaluation-row-${id}"`)).split("</tr>")[0];
		expect(rowOf("ev_b")).toContain("Nightly");
		expect(rowOf("ev_a")).not.toContain("Nightly");
		expect(rowOf("ev_c")).not.toContain("Nightly");
	});
});
describe("EvaluationTable lists (M1-18, M1-59, N1-2)", () => {
	it("marks the project's own test evaluations by name, id or test agent", async () => {
		const { isTestEvaluation } = await import("./evaluationListView");
		expect(isTestEvaluation(ev)).toBe(false);
		expect(isTestEvaluation({ ...ev, spec: { ...ev.spec, name: "zz-test-loop nightly" } } as Evaluation)).toBe(true);
		expect(isTestEvaluation({ ...ev, spec: { ...ev.spec, arms: [{ label: "A", agent: "zz-test-loop", model: "m" }] } } as Evaluation)).toBe(true);
	});

	it("pages 50 rows at a time and shows the range", () => {
		const many = Array.from({ length: 60 }, (_, i) => ({ ...ev, id: `ev_${i}`, status: "done" }) as Evaluation);
		const html = render(<EvaluationTable evaluations={many} />);
		expect(html.match(/data-testid="evaluation-row-/g)?.length).toBe(50);
		expect(html).toContain("1 to 50 of 60 evaluations");
		expect(html).toContain('data-testid="evaluations-pager"');
	});

	it("an empty list says so and offers the way in", () => {
		const empty = render(<EvaluationTable evaluations={[]} />);
		expect(empty).toContain("No evaluations yet");
		expect(empty).toContain('href="/evaluate"');
	});
});
