import type { Tone } from "@/components/verdict/verdict.utils";
import type { CodeScanFinding, CodeScanSeverity } from "@/lib/types/bench";

// X4: pure helpers for the code scan panel (docs/code-scan.md), tested without rendering.

/** Tone per severity: high and critical in the error tone, medium as a warning. */
export const SEVERITY_TONE = {
	critical: "bad",
	high: "bad",
	medium: "warn",
	low: "neutral",
} as const satisfies Record<CodeScanSeverity, Tone>;

const SEVERITY_RANK: Record<string, number> = { low: 1, medium: 2, high: 3, critical: 4 };

/** Rank of a severity, 0 for an unknown one. */
export function severityRank(s: string): number {
	return SEVERITY_RANK[s] ?? 0;
}

/** Findings ordered most severe first, then by file and line. Does not mutate its input. */
export function sortFindings(findings: readonly CodeScanFinding[]): CodeScanFinding[] {
	return [...findings].sort(
		(a, b) =>
			severityRank(b.severity) - severityRank(a.severity) ||
			a.file.localeCompare(b.file) ||
			a.line - b.line ||
			a.rule.localeCompare(b.rule),
	);
}

/** "app/x.py:12": the file and the line number in the post-run file (the pre-run file for a removed line). */
export function findingWhere(f: CodeScanFinding): string {
	return `${f.file}:${f.line}`;
}

export type PatchLineKind = "header" | "hunk" | "add" | "del" | "ctx";

/** One line of the patch with the findings that sit on it. n is its 1-based line in workspace.diff. */
export interface AnnotatedLine {
	n: number;
	text: string;
	kind: PatchLineKind;
	findings: CodeScanFinding[];
}

/** One file of the patch: its lines and how many findings sit on them. */
export interface AnnotatedFile {
	path: string;
	lines: AnnotatedLine[];
	flagged: number;
}

function kindOf(line: string, inHunk: boolean): PatchLineKind {
	if (!inHunk) return line.startsWith("@@") ? "hunk" : "header";
	if (line.startsWith("@@")) return "hunk";
	if (line.startsWith("+")) return "add";
	if (line.startsWith("-")) return "del";
	return "ctx";
}

const HUNK_RE = /^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@/;

/**
 * Splits a unified diff into files and puts each finding on its line: by patch_line when the server
 * sent it, else by file and post-run line number (pre-run for a removed line) read from the hunk
 * headers. A finding that matches no line is left out of the annotation; the table still lists it.
 */
export function annotatePatch(patch: string, findings: readonly CodeScanFinding[]): AnnotatedFile[] {
	const lines = patch.length > 0 ? patch.replace(/\n$/, "").split("\n") : [];
	const files: AnnotatedFile[] = [];
	let cur: AnnotatedFile | undefined;
	let oldNo = 0;
	let newNo = 0;
	let left = 0;
	const byPatch = new Map<number, CodeScanFinding[]>();
	const rest: CodeScanFinding[] = [];
	for (const f of findings) {
		if (f.patch_line && f.patch_line > 0) byPatch.set(f.patch_line, [...(byPatch.get(f.patch_line) ?? []), f]);
		else rest.push(f);
	}
	lines.forEach((text, i) => {
		const n = i + 1;
		if (left <= 0 && text.startsWith("diff --git ")) {
			const m = / b\/(.*)$/.exec(text);
			cur = { path: m ? m[1] : text.slice("diff --git ".length), lines: [], flagged: 0 };
			files.push(cur);
		}
		if (!cur) return;
		const inHunk = left > 0;
		const kind = kindOf(text, inHunk);
		const hunk = kind === "hunk" ? HUNK_RE.exec(text) : null;
		const line: AnnotatedLine = { n, text, kind, findings: [...(byPatch.get(n) ?? [])] };
		if (hunk) {
			oldNo = Number(hunk[1]);
			newNo = Number(hunk[3]);
			left = Number(hunk[2] ?? 1) + Number(hunk[4] ?? 1);
		} else if (inHunk && !text.startsWith("\\")) {
			const path = cur.path;
			const match = (f: CodeScanFinding) =>
				f.file === path && ((kind === "del" && f.removed && f.line === oldNo) || (kind === "add" && !f.removed && f.line === newNo));
			line.findings.push(...rest.filter(match));
			if (kind === "add") {
				newNo++;
				left--;
			} else if (kind === "del") {
				oldNo++;
				left--;
			} else {
				newNo++;
				oldNo++;
				left -= 2;
			}
		}
		cur.flagged += line.findings.length;
		cur.lines.push(line);
	});
	return files;
}

/** The files of the patch that hold a finding. */
export function flaggedFiles(patch: string, findings: readonly CodeScanFinding[]): AnnotatedFile[] {
	return annotatePatch(patch, findings).filter((f) => f.flagged > 0);
}