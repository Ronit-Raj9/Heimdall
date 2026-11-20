import "@/lib/i18n";

import type { Grid } from "@/lib/types/bench";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { TaskGrid } from "./taskGrid";
import { gridFixture } from "./testFixtures";

const noop = () => {};

describe("TaskGrid", () => {
	it("renders discordant rows first with a cell per arm and trial", () => {
		const html = renderToStaticMarkup(<TaskGrid grid={gridFixture} onOpenPair={noop} />);
		const rowOrder = [...html.matchAll(/data-testid="verdict-grid-row-([^"]+)" data-discordant="(true|false)"/g)].map((m) => m[2]);
		expect(rowOrder.indexOf("false")).toBeGreaterThan(rowOrder.lastIndexOf("true"));
		const cells = gridFixture.rows.flatMap((row) => row.cells);
		for (const cell of cells) expect(html).toContain(`data-testid="verdict-grid-cell-${cell.slot_id}"`);
		expect(html).toContain('data-pair-id="p_ltosmymyqy"');
		expect(html).toContain(">A1<");
		expect(html).toContain(">B3<");
		expect(html).toContain("t02, stale-notes, arm A, trial 3: timeout. Open the run comparison.");
	});

	it("colours outcomes and hatches unknowns", () => {
		const grid: Grid = {
			exp_id: "e_x",
			arms: ["A", "B"],
			conditions: ["clean"],
			trials: 1,
			rows: [
				{
					task: "t01",
					condition: "clean",
					discordant: true,
					diff: { task: "t01", mean_a: 0, mean_b: 1, d: 1, n_a: 1, n_b: 1, unknown_a: 1, unknown_b: 0 },
					cells: [
						{ slot_id: "s_a", pair_id: "p_1", run_id: null, arm: "A", trial: 1, outcome: "infra_error", y: { state: "unknown", v: null } },
						{
							slot_id: "s_b",
							pair_id: "p_1",
							run_id: "r_b",
							arm: "B",
							trial: 1,
							outcome: "integrity_violation",
							y: { state: "observed", v: 0 },
						},
					],
				},
			],
		};
		const html = renderToStaticMarkup(<TaskGrid grid={grid} onOpenPair={noop} />);
		expect(html).toMatch(/data-testid="verdict-grid-cell-s_a"[^>]*data-tone="unknown"/);
		expect(html).toContain("bench-hatched");
		expect(html).toMatch(/data-testid="verdict-grid-cell-s_b"[^>]*data-tone="integrity"/);
		expect(html).toContain(">+100 pp<");
	});

	it("marks a missing cell as not run", () => {
		const grid: Grid = { ...gridFixture, rows: [{ ...gridFixture.rows[0], cells: gridFixture.rows[0].cells.slice(1) }] };
		const html = renderToStaticMarkup(<TaskGrid grid={grid} onOpenPair={noop} />);
		expect(html).toContain('data-testid="verdict-grid-cell-missing-t01-stale-notes-A-1"');
		expect(html).toContain("not run yet");
	});

	it("says so when the grid is empty", () => {
		const html = renderToStaticMarkup(<TaskGrid grid={{ ...gridFixture, rows: [] }} onOpenPair={noop} />);
		expect(html).toContain('data-testid="verdict-grid-empty"');
	});
});
// 6-B-06: the pass check is dark ink on the bright dark-theme success fill, white on the light one.
describe("pass cell icon contrast", () => {
	it("uses the paired success-on token, dark in the dark theme", async () => {
		const { CELL_CLASS } = await import("./taskGrid");
		expect(CELL_CLASS.pass).toContain("text-chart-success-on");
		expect(CELL_CLASS.pass).not.toContain("text-white");
		const fs = await import("node:fs");
		const path = await import("node:path");
		const css = fs.readFileSync(path.resolve(__dirname, "../../app/globals.css"), "utf8");
		const dark = css.slice(css.indexOf(".dark {"));
		const lightness = (block: string) => Number(/--chart-success-on: oklch\(([\d.]+)/.exec(block)?.[1]);
		expect(lightness(dark)).toBeLessThan(0.35); // fill is L 0.72
		expect(lightness(css.slice(css.indexOf(":root {", 100)))).toBeGreaterThan(0.9); // fill is L 0.63
	});
});
