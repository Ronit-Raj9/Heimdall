import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { GradeSourceTag, gradeSourceKind } from "./gradeSourceTag";

describe("gradeSourceKind", () => {
	it("names every grade source the server sends", () => {
		expect(gradeSourceKind(undefined)).toBe("verified");
		expect(gradeSourceKind("original")).toBe("verified");
		expect(gradeSourceKind("original", true)).toBe("judged");
		expect(gradeSourceKind("reviewed")).toBe("reviewed");
		expect(gradeSourceKind("rg_123")).toBe("regraded");
	});
});

describe("GradeSourceTag", () => {
	it("shows the regrade id, and sealed blocks ignore the regrade", () => {
		expect(renderToStaticMarkup(<GradeSourceTag source="rg_123" />)).toContain("Regraded rg_123");
		const sealed = renderToStaticMarkup(<GradeSourceTag source="rg_123" sealedOnly />);
		expect(sealed).toContain('data-grade-source="verified"');
	});
});