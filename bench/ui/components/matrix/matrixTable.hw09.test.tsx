import "@/lib/i18n";

import type { MatrixRow, ModelMatrixResponse } from "@/lib/types/bench";
import fs from "node:fs";
import path from "node:path";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { withSetups } from "@/app/experiments_/$expId/views/matrixTab";
import { MatrixTable } from "./matrixTable";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

const fixture = JSON.parse(
	fs.readFileSync(path.resolve(__dirname, "../../public/fixtures/experiments.matrix.json"), "utf8"),
) as ModelMatrixResponse;

// Fix wave 3, N245: the Setups tab row of e_zz_test_matrix3_6z3h said "solved all 1 tries" next to
// "50%, 1 of 2 solved". One try per task measures no reliability.
describe("MatrixTable with one try per task", () => {
	it("says Not measured and how many tries ran", () => {
		const rows: MatrixRow[] = fixture.rows.map((row) => ({
			...row,
			trials: 1,
			tasks: 2,
			tasks_solved: 1,
			passes: { num: 1, den: 2, unknown: 0 },
			reliability: { state: "unknown", v: null, reason: "pass^k needs 2 or more trials per task; its experiments ran 1" },
		}));
		const html = renderToStaticMarkup(<MatrixTable rows={rows} />);
		expect(html).not.toContain("solved all 1 tries");
		expect(html).toContain(">Not measured<");
		expect(html).toContain("needs 2 or more tries per task; this ran 1");
		expect(html).toContain("1 of 2 tasks solved");
	});
});

// Fix wave 3, N244: the Setups tab takes its tiers and note from the analysis's setups verdict.
describe("withSetups", () => {
	it("puts the verdict's groups and note on the matrix", () => {
		const matrix = fixture;
		const arms = matrix.rows.map((r) => r.arm);
		const setups = {
			leader: arms[0],
			top_group: [arms[0]],
			behind: arms.slice(1),
			separable: true,
			note: "One setup is ahead.",
			pairs: [],
		};
		const out = withSetups(matrix, setups);
		expect(out.note).toBe("One setup is ahead.");
		expect(out.rows.map((r) => r.tier)).toEqual(arms.map((_, i) => (i === 0 ? 1 : 2)));
		expect(withSetups(matrix, undefined)).toBe(matrix);
	});
});