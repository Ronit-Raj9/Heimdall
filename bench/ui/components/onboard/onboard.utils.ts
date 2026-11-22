/**
 * The benchmark id rule of the server (onboard/onboard.go suiteIDRe): 1 to 64 lowercase letters, digits, '.', '_' or
 * '-', starting with a letter or digit. Checked as the id is typed, so Draft is never pressed for an id the server refuses.
 */
export const SUITE_ID_RE = /^[a-z0-9][a-z0-9._-]{0,63}$/;

/** The fields of a listed benchmark the id check reads. */
export interface SuiteIdEntry {
	id: string;
	builtin?: boolean;
	source?: string;
	task_count?: number;
}

/** What drafting into a typed id does: refused (invalid, built-in), adds to an org benchmark, or creates a new one. */
export type SuiteIdCheck =
	| { state: "empty" }
	| { state: "invalid" }
	| { state: "builtin" }
	| { state: "existing"; count: number }
	| { state: "new" };

/** True for a benchmark from the server's read-only library. */
function isBuiltin(s: SuiteIdEntry): boolean {
	return s.builtin === true || s.source === "library";
}

/** Checks id against the server's rule and the listed benchmarks (the id part before any "@version"). */
export function suiteIdCheck(id: string, suites: readonly SuiteIdEntry[]): SuiteIdCheck {
	if (!id) return { state: "empty" };
	if (!SUITE_ID_RE.test(id)) return { state: "invalid" };
	const same = suites.filter((s) => s.id.split("@")[0] === id);
	if (same.some(isBuiltin)) return { state: "builtin" };
	if (same.length > 0) return { state: "existing", count: same[0].task_count ?? 0 };
	return { state: "new" };
}

/** True when Draft must stay disabled for this id. */
export function suiteIdBlocks(check: SuiteIdCheck): boolean {
	return check.state === "empty" || check.state === "invalid" || check.state === "builtin";
}

/**
 * A free benchmark id made from a source name (a file or dataset name): lowercased, a file extension dropped, other
 * characters outside the rule replaced by "-", cut to 64. A built-in id gets "-own" so the draft does not land on it.
 * Returns "" when nothing usable is left.
 */
export function suggestSuiteId(name: string, suites: readonly SuiteIdEntry[]): string {
	let id = name
		.trim()
		.toLowerCase()
		.replace(/\.(csv|tsv|jsonl|json|parquet|txt|ya?ml|zip)$/, "")
		.split("/")
		.pop()!
		.replace(/[^a-z0-9._-]+/g, "-")
		.replace(/^[^a-z0-9]+/, "")
		.slice(0, 64)
		.replace(/[^a-z0-9]+$/, "");
	if (!id) return "";
	if (suiteIdCheck(id, suites).state === "builtin") id = `${id.slice(0, 60)}-own`;
	return SUITE_ID_RE.test(id) ? id : "";
}