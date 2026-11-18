import type { CodeScanFinding } from "@/lib/types/bench";
import { describe, expect, it } from "vitest";
import { annotatePatch, findingWhere, flaggedFiles, severityRank, sortFindings } from "./codescan.utils";

const PATCH = [
	"diff --git a/app/run.py b/app/run.py",
	"index 1..2 100644",
	"--- a/app/run.py",
	"+++ b/app/run.py",
	"@@ -10,3 +10,3 @@ def f():",
	" import os",
	"-    os.system('ls')",
	"+    os.system('ls ' + name)",
	" x = 1",
	"diff --git a/README.md b/README.md",
	"index 3..4 100644",
	"--- a/README.md",
	"+++ b/README.md",
	"@@ -1 +1 @@",
	"-old",
	"+new",
	"",
].join("\n");

const finding = (over: Partial<CodeScanFinding>): CodeScanFinding => ({
	rule: "shell-injection-python",
	title: "Shell command built from non-constant input (Python)",
	cwe: "CWE-78",
	severity: "high",
	file: "app/run.py",
	line: 11,
	source: "builtin",
	...over,
});

describe("annotatePatch", () => {
	it("puts a finding on its patch line", () => {
		const files = annotatePatch(PATCH, [finding({ patch_line: 8 })]);
		expect(files.map((f) => f.path)).toEqual(["app/run.py", "README.md"]);
		const flagged = files[0].lines.filter((l) => l.findings.length > 0);
		expect(flagged.map((l) => [l.n, l.kind])).toEqual([[8, "add"]]);
		expect(files[1].flagged).toBe(0);
	});

	it("falls back to the file and post-run line, and to the pre-run line for a removed line", () => {
		const files = annotatePatch(PATCH, [finding({}), finding({ rule: "csrf-protection-removed", removed: true, line: 11 })]);
		const flagged = files[0].lines.filter((l) => l.findings.length > 0).map((l) => [l.n, l.kind, l.findings[0].rule]);
		expect(flagged).toEqual([
			[7, "del", "csrf-protection-removed"],
			[8, "add", "shell-injection-python"],
		]);
	});

	it("leaves out a finding that matches no line and handles an empty patch", () => {
		expect(flaggedFiles(PATCH, [finding({ file: "other.py" })])).toEqual([]);
		expect(annotatePatch("", [finding({})])).toEqual([]);
	});
});

describe("sortFindings", () => {
	it("orders by severity, then file and line, without mutating", () => {
		const input = [
			finding({ severity: "medium", line: 1 }),
			finding({ severity: "critical", file: "z.py" }),
			finding({ severity: "high", line: 2 }),
		];
		const out = sortFindings(input);
		expect(out.map((f) => f.severity)).toEqual(["critical", "high", "medium"]);
		expect(input[0].severity).toBe("medium");
		expect(severityRank("bogus")).toBe(0);
		expect(findingWhere(out[1])).toBe("app/run.py:2");
	});
});