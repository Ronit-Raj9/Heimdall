import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { VerdictTabNotes } from "@/app/experiments_/$expId/views/verdictTab";
import { SetupsTable } from "./setupsTable";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

// Fix wave 3, N244: the Result tab lists every setup of a comparison of three or more, from the
// same setups verdict as the headline.
describe("SetupsTable", () => {
	const setups = {
		leader: "A",
		top_group: ["A", "C", "B"],
		behind: [],
		separable: false,
		note: "Cannot separate any two arms",
		pairs: [
			{ arm_x: "A", arm_y: "C", tasks: 2, n_nonzero: 0, mean_diff: 0, p: 1, p_adjusted: 1, significant: false, reason: "" },
			{ arm_x: "A", arm_y: "B", tasks: 2, n_nonzero: 1, mean_diff: 0.5, p: 1, p_adjusted: 1, significant: false, reason: "" },
		],
		safety: { B: ["t02"] },
	};
	it("lists every setup with its solved runs, group and safety flag", () => {
		const html = renderToStaticMarkup(
			<SetupsTable
				setups={setups}
				arms={["A", "B", "C"]}
				naive={{ A: { num: 2, den: 2, unknown: 0 }, B: { num: 1, den: 2, unknown: 0 }, C: { num: 2, den: 2, unknown: 0 } }}
				names={{ B: "codex on gpt-6.2-sol" }}
			/>,
		);
		for (const arm of ["A", "B", "C"]) expect(html).toContain(`data-testid="verdict-setups-row-${arm}"`);
		expect(html).toContain("Broke a safety rule on t02");
		expect(html).toContain("codex on gpt-6.2-sol");
		expect(html).toContain(">Leader<");
		expect(html).toContain(">Top group<");
	});
});

describe("VerdictTabNotes", () => {
	it("says a readiness check is not a comparison and links the benchmark's Build tab", () => {
		const html = renderToStaticMarkup(<VerdictTabNotes system="readiness" suite="coding-core@1.0.0" />);
		expect(html).toContain("This is a readiness check, not a comparison. Its result is on the benchmark&#x27;s Build tab.");
		expect(html).toMatch(/href="\/suites\/coding-core[^"]*tab=build/);
	});
	it("sends a draft benchmark's readiness check to Add benchmark", () => {
		const html = renderToStaticMarkup(<VerdictTabNotes system="readiness" suite="draft-abc@0" />);
		expect(html).toMatch(/href="\/suites\/add/);
	});
	it("says how many runs are done while the comparison runs", () => {
		const html = renderToStaticMarkup(<VerdictTabNotes partial={{ done: 3, total: 12 }} />);
		expect(html).toContain("Still running: 3 of 12 runs done. The result below counts only finished runs and is not final.");
	});
	it("says nothing for a finished user comparison", () => {
		expect(renderToStaticMarkup(<VerdictTabNotes suite="coding-core@1.0.0" />)).toBe("");
	});
});