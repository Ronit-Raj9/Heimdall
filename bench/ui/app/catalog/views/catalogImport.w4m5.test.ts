import "@/lib/i18n";

import { describe, expect, it } from "vitest";
import { type ImportForm, importProblem } from "./catalogView";

const ok: ImportForm = { mode: "url", from: "", url: "https://example.com/data.zip", file: null, ref: "", suite: "my-bench", limit: "5" };

describe("importProblem (p09-2)", () => {
	it("gives null for a valid form", () => {
		expect(importProblem(ok)).toBeNull();
	});
	it("names each problem", () => {
		expect(importProblem({ ...ok, mode: "upload" })).toBe("Pick a file to import.");
		expect(importProblem({ ...ok, url: "" })).toBe("Enter a link that starts with https://.");
		expect(importProblem({ ...ok, url: "http://example.com/x.zip" })).toBe("Only https links can be fetched. Change http:// to https://.");
		expect(importProblem({ ...ok, suite: "" })).toBe("Enter the benchmark id.");
		expect(importProblem({ ...ok, suite: "Bad Id" })).toContain("The benchmark id can use lower-case letters");
		expect(importProblem({ ...ok, limit: "0" })).toBe("Enter a whole number from 1 to 500.");
		expect(importProblem({ ...ok, limit: "600" })).toBe("Enter a whole number from 1 to 500.");
		expect(importProblem({ ...ok, limit: "" })).toBe("Enter a whole number from 1 to 500.");
	});
});