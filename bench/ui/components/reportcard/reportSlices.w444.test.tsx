import { render } from "@/lib/testing/render";
import { describe, expect, it } from "vitest";
import { abReport } from "./reportCard.fixtures";
import { ReportSlices, sliceLabel } from "./reportSlices";

const metrics = { score: 0.5, ci_low: 0.3, ci_high: 0.7 } as never;

describe("task category slices", () => {
	it("maps the category key and its values to plain words", () => {
		expect(sliceLabel("category")).toBe("By kind of work");
		expect(sliceLabel("category", "bug_fix")).toBe("Bug fix");
		expect(sliceLabel("category", "navigation")).toBe("Finding your way in code");
		expect(sliceLabel("category", "refactor (unsure)")).toBe("Refactor (unsure)");
		expect(sliceLabel("tier")).toBe("tier");
		expect(sliceLabel("tier", "bug_fix")).toBe("bug_fix");
	});

	it("renders the category slice with the heading and words", () => {
		const report = {
			...abReport,
			slices: [
				{
					key: "category",
					values: [
						{ value: "bug_fix", tasks: 4, arms: { A: metrics, B: metrics } },
						{ value: "test_repair (unsure)", tasks: 3, arms: { A: metrics, B: metrics } },
					],
					worst: {},
				},
			],
		};
		const html = render(<ReportSlices report={report} />);
		expect(html).toContain("By kind of work");
		expect(html).toContain("Bug fix");
		expect(html).toContain("Test repair (unsure)");
		expect(html).not.toContain(">bug_fix<");
	});
});