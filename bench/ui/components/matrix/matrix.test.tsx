import "@/lib/i18n";

import type { MatrixPair, MatrixRow, ModelMatrix, ModelMatrixResponse } from "@/lib/types/bench";
import fs from "node:fs";
import path from "node:path";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { formatCellP, pairIndex, pairTone } from "./matrix.utils";
import { MatrixPairGrid } from "./matrixPairGrid";
import { MatrixReport } from "./matrixReport";
import { MatrixTable } from "./matrixTable";

// A plain anchor stands in for the router's Link, which the shared leaderboard cells may use.
vi.mock("@tanstack/react-router", () => ({
	Link: ({ to, search, children, ...rest }: { to: string; search?: Record<string, string>; children: React.ReactNode }) => (
		<a href={`${to}?${new URLSearchParams(search ?? {}).toString()}`} {...rest}>
			{children}
		</a>
	),
}));

const fixture = JSON.parse(
	fs.readFileSync(path.resolve(__dirname, "../../public/fixtures/experiments.matrix.json"), "utf8"),
) as ModelMatrixResponse;

const pair = (arm_x: string, arm_y: string, over: Partial<MatrixPair> = {}): MatrixPair => ({
	arm_x,
	arm_y,
	tasks: 12,
	n_nonzero: 10,
	mean_diff: 0.4,
	p: 0.002,
	p_adjusted: 0.006,
	significant: true,
	reason: "separated: 10 of 12 tasks differ",
	...over,
});

/** A separable matrix: A beats C significantly, A and B cannot be told apart, B and C were never compared. */
const SEPARABLE: ModelMatrix = {
	...fixture,
	separable: true,
	note: "Tier 1 arms cannot be told apart from the leader at alpha 0.05.",
	synthetic: false,
	tasks: 12,
	rows: fixture.rows.map((row, i) => ({ ...row, tier: i === 2 ? 2 : 1 })),
	pairs: [
		pair("A", "B", { mean_diff: 0.05, p: 0.6, p_adjusted: 1, significant: false, n_nonzero: 3, reason: "cannot separate: p 1 after Holm" }),
		pair("A", "C"),
		pair("B", "C", { p: null, p_adjusted: null, significant: false, tasks: 0, n_nonzero: 0, reason: "no common tasks" }),
	],
};

/** The markup of one element with a test id, up to the next tag. */
const textOf = (html: string, testId: string) => html.match(new RegExp(`data-testid="${testId}"[^>]*>([^<]*)<`))?.[1] ?? null;
/** The markup of one grid cell, its own attributes included. */
const cell = (html: string, row: string, col: string) =>
	html.match(new RegExp(`<td[^>]*data-testid="matrix-cell-${row}-${col}"[^>]*>[\\s\\S]*?</td>`))?.[0] ?? "";

describe("matrix utils", () => {
	it("finds a pair in either order and flips the difference to row minus column", () => {
		const lookup = pairIndex([pair("A", "B", { mean_diff: 0.25 })]);
		expect(lookup("A", "B")?.diff).toBe(0.25);
		expect(lookup("B", "A")?.diff).toBe(-0.25);
		expect(lookup("A", "C")).toBeNull();
		expect(pairIndex(null)("A", "B")).toBeNull();
	});

	it("colours only significant cells, by direction from the row", () => {
		const p = pair("A", "B");
		expect(pairTone({ pair: p, diff: 0.4 })).toBe("good");
		expect(pairTone({ pair: p, diff: -0.4 })).toBe("bad");
		expect(pairTone({ pair: { ...p, significant: false }, diff: 0.4 })).toBe("neutral");
	});

	it("formats p-values and keeps a missing one missing", () => {
		expect(formatCellP(0.75, "en")).toBe("0.75");
		expect(formatCellP(0.00001, "en")).toBe("< 0.0001");
		expect(formatCellP(null, "en")).toBeNull();
		expect(formatCellP(undefined, "en")).toBeNull();
	});

	it("fixture pairs cover every pair of rows exactly once", () => {
		const arms = fixture.rows.map((r) => r.arm);
		const keys = fixture.pairs.map((p) => [p.arm_x, p.arm_y].sort().join("|"));
		expect(new Set(keys).size).toBe(keys.length);
		expect(keys.length).toBe((arms.length * (arms.length - 1)) / 2);
		for (const p of fixture.pairs) expect(arms).toEqual(expect.arrayContaining([p.arm_x, p.arm_y]));
	});
});

describe("MatrixTable", () => {
	it("shows one row per arm with rank, tier, arm, model, provider, success interval, pass^k and metrics", () => {
		const html = renderToStaticMarkup(<MatrixTable rows={fixture.rows} />);
		expect(html).toContain('data-testid="matrix-table"');
		for (const row of fixture.rows) {
			expect(html).toContain(`data-testid="matrix-row-${row.arm}"`);
			expect(textOf(html, `matrix-provider-${row.arm}`)).toBe(row.provider);
			expect(html).toContain(`>${row.model}<`);
			expect(html).toContain(`>${row.label}<`);
		}
		expect(textOf(html, "matrix-arm-A")).toBe("A");
		expect(html).toContain('data-testid="leaderboard-interval-1"');
		expect(html).toContain("75%");
		expect(html).toContain(">solved all 3 tries<");
		const metrics = (arm: string, id: string) =>
			html.match(new RegExp(`data-testid="matrix-metric-${arm}-${id}"[\\s\\S]*?</td>`))?.[0] ?? "";
		expect(metrics("A", "cost_per_success")).toContain(">$0.061<");
		expect(metrics("A", "tokens")).toContain(">52K<");
		expect(metrics("A", "wall_time_s")).toContain(">3.5m<");
		expect(metrics("C", "false_completion")).toContain(">16.7%<");
	});

	it("draws a missing metric as unknown, never 0", () => {
		const row: MatrixRow = {
			...fixture.rows[0],
			metrics: { ...fixture.rows[0].metrics, tokens: { state: "unknown", v: null, reason: "no usage" } },
		};
		const html = renderToStaticMarkup(<MatrixTable rows={[row]} />);
		const tokens = html.match(/data-testid="matrix-metric-A-tokens"[\s\S]*?<\/td>/)?.[0] ?? "";
		expect(tokens).toContain('data-state="unknown"');
		expect(tokens).not.toContain(">0<");
	});
});

describe("MatrixPairGrid", () => {
	it("shows the adjusted p per cell, bold and coloured only when significant, with the reason in the title", () => {
		const html = renderToStaticMarkup(<MatrixPairGrid rows={SEPARABLE.rows} pairs={SEPARABLE.pairs} />);
		expect(html).toContain('data-testid="matrix-grid"');
		expect(cell(html, "A", "A")).toContain('data-state="same"');

		const ac = cell(html, "A", "C");
		expect(ac).toContain('data-state="significant"');
		expect(ac).toContain('data-tone="good"');
		expect(ac).toContain("font-bold");
		expect(ac).toContain(">0.006<");
		expect(ac).toContain("separated: 10 of 12 tasks differ");
		expect(ac).toContain("Row minus column +40 pp on 12 common tasks, 10 differ");

		const ca = cell(html, "C", "A");
		expect(ca).toContain('data-tone="bad"');
		expect(ca).toContain("Row minus column -40 pp");

		const ab = cell(html, "A", "B");
		expect(ab).toContain('data-state="not-significant"');
		expect(ab).not.toContain("font-bold");
		expect(ab).toContain(">1<");
		expect(ab).toContain("cannot separate: p 1 after Holm");

		const bc = cell(html, "B", "C");
		expect(bc).toContain('data-state="none"');
		expect(bc).toContain(">no test<");
		expect(bc).toContain('title="no common tasks"');
	});
});

describe("MatrixReport", () => {
	it("states plainly that nothing can be separated, before the numbers", () => {
		const html = renderToStaticMarkup(<MatrixReport matrix={fixture} onCondition={() => undefined} />);
		expect(html).toContain('data-separable="false"');
		expect(html).toContain('data-testid="matrix-not-separable"');
		expect(html).toContain(">Cannot separate<");
		expect(html).toContain(fixture.note.replace("<", "&lt;"));
		expect(html.indexOf("matrix-not-separable")).toBeLessThan(html.indexOf('data-testid="matrix-table"'));
		expect(html).toContain('data-testid="matrix-synthetic"');
		expect(html).toContain('data-testid="matrix-grid"');
		expect(textOf(html, "matrix-facts")).toBe("coding-core@1.0.0, common tasks 4, alpha 0.05");
		expect(textOf(html, "matrix-grade-source")).toBe("Grades: original");
		expect(html).toContain('data-testid="matrix-condition"');
		expect(html).toContain("Left out: infra_error (1 runs)");
	});

	it("shows the note quietly when arms can be separated", () => {
		const html = renderToStaticMarkup(<MatrixReport matrix={SEPARABLE} onCondition={() => undefined} />);
		expect(html).toContain('data-separable="true"');
		expect(html).not.toContain('data-testid="matrix-not-separable"');
		expect(html).toContain('data-testid="matrix-note"');
		expect(html).not.toContain('data-testid="matrix-synthetic"');
		expect(html).toContain('data-tier="2"');
	});

	it("survives null lists from the server and skips the grid with one arm", () => {
		const bare = { ...fixture, rows: [fixture.rows[0]], pairs: null, conditions: null, excluded: null } as unknown as ModelMatrix;
		const html = renderToStaticMarkup(<MatrixReport matrix={bare} onCondition={() => undefined} />);
		expect(html).toContain('data-testid="matrix-row-A"');
		expect(html).not.toContain('data-testid="matrix-grid"');
		expect(html).not.toContain('data-testid="matrix-condition"');
		expect(html).not.toContain('data-testid="matrix-excluded"');
	});
});