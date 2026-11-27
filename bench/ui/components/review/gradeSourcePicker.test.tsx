import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { GradeSourcePicker } from "./gradeSourcePicker";

describe("GradeSourcePicker", () => {
	it("presses the board's grade source and offers human reviewed", () => {
		const html = renderToStaticMarkup(<GradeSourcePicker value="reviewed" onChange={() => {}} testId="gs" />);
		expect(html).toMatch(/aria-pressed="true"[^>]*data-testid="gs-reviewed"/);
		expect(html).toMatch(/aria-pressed="false"[^>]*data-testid="gs-original"/);
		expect(html).toMatch(/aria-pressed="false"[^>]*data-testid="gs-latest"/);
		expect(html).toContain("Human reviewed");
		expect(html).toContain("No re-scores");
	});
});