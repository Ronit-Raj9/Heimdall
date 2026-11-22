import {
	TRACKS,
	type Grading,
	type OutputKind,
	type RunDetail,
	type ServiceRole,
	type SuiteSummary,
	type ToolCall,
	type Track,
} from "@/lib/types/bench";

/** True for a track this UI has a label for. The server may send a newer one; it is shown as is. */
export function isTrack(value: string | undefined): value is Track {
	return value !== undefined && (TRACKS as readonly string[]).includes(value);
}

export function isGrading(value: string | undefined): value is Grading {
	return value === "verified" || value === "judged";
}

export function isServiceRole(value: string | undefined): value is ServiceRole {
	return value === "data" || value === "tool" || value === "mcp" || value === "user";
}

export function isOutputKind(value: string | undefined): value is OutputKind {
	return value === "diff" || value === "answer" || value === "state" || value === "artifact";
}

/** A suite's track: the server sends coding when the suite sets none, and so does an older server's absence. */
export function suiteTrack(suite: Pick<SuiteSummary, "track">): string {
	return suite.track || "coding";
}

/** A suite's grading; absent means verified, the default. */
export function suiteGrading(suite: Pick<SuiteSummary, "grading">): Grading {
	return suite.grading === "judged" ? "judged" : "verified";
}

/** The suite id of a leaderboard suite value ("id@version"). */
export function suiteIdOf(value: string): string {
	const at = value.lastIndexOf("@");
	return at > 0 ? value.slice(0, at) : value;
}

/** Track and grading of each suite id on disk; a suite that failed to load is left out. */
export function suiteFactsById(suites: readonly SuiteSummary[]): Map<string, { track: string; grading: Grading }> {
	return new Map(suites.filter((s) => !s.error).map((s) => [s.id, { track: suiteTrack(s), grading: suiteGrading(s) }]));
}

/** Whether a leaderboard suite value (id@version) is a judged suite on disk. Unknown suites are not. */
export function isJudgedSuite(value: string | undefined, facts: Map<string, { grading: Grading }>): boolean {
	return value !== undefined && facts.get(suiteIdOf(value))?.grading === "judged";
}

/**
 * "Not on disk": a leaderboard suite with sealed runs but no suite folder, so its track is not
 * known. Not snake_case, so it can never be a real track, and not empty, as a select value must not be.
 */
export const UNKNOWN_TRACK = "~unknown";

export interface TrackGroup {
	/** A track, or UNKNOWN_TRACK. */
	track: string;
	/** Leaderboard suite values (id@version), in the board's order (newest first). */
	suites: string[];
}

/**
 * Leaderboard suites grouped by track: known tracks in TRACKS order, then any newer track by name,
 * then suites whose folder is not on disk. Order inside a group is the board's.
 */
export function groupSuitesByTrack(boardSuites: readonly string[], facts: Map<string, { track: string }>): TrackGroup[] {
	const groups = new Map<string, string[]>();
	for (const value of boardSuites) {
		const track = facts.get(suiteIdOf(value))?.track ?? UNKNOWN_TRACK;
		groups.set(track, [...(groups.get(track) ?? []), value]);
	}
	const known = TRACKS.filter((track) => groups.has(track));
	const newer = [...groups.keys()].filter((track) => track !== UNKNOWN_TRACK && !isTrack(track)).sort();
	const order = [...known, ...newer, ...(groups.has(UNKNOWN_TRACK) ? [UNKNOWN_TRACK] : [])];
	return order.map((track) => ({ track, suites: groups.get(track)! }));
}

/** Whether a run detail carries any scenario evidence. A plain diff run has none, and its sections are left out. */
export function hasScenarioEvidence(
	detail: Pick<RunDetail, "tool_calls" | "sessions" | "answer" | "state" | "services" | "artifacts">,
): boolean {
	return Boolean(
		detail.tool_calls?.length ||
		detail.sessions?.length ||
		detail.answer ||
		detail.state?.length ||
		detail.services?.length ||
		detail.artifacts?.length,
	);
}

/** Counts for the tool-call trace header. */
export function toolCallCounts(calls: readonly ToolCall[]): {
	calls: number;
	errors: number;
	faults: number;
	other: number;
	unparsed: number;
} {
	const tools = calls.filter(isToolCallRow);
	return {
		calls: tools.length,
		errors: tools.filter((call) => call.is_error).length,
		faults: tools.filter((call) => Boolean(call.fault)).length,
		other: calls.length - tools.length,
		unparsed: calls.filter((call) => call.unparsed).length,
	};
}

/** Whether a ledger row is a tool call (schemas.ToolCall.IsToolCall): no kind, and not a tools/list row written before kinds existed. */
export function isToolCallRow(call: ToolCall): boolean {
	return !call.kind && !(call.protocol === "mcp" && call.tool === "tools/list");
}

/** Whole seconds between two timestamps, or null when either is missing or unparsable. */
export function durationSeconds(started: string, ended: string): number | null {
	const a = Date.parse(started);
	const b = Date.parse(ended);
	if (Number.isNaN(a) || Number.isNaN(b) || b < a) return null;
	return Math.round((b - a) / 1000);
}

/** The import commands for one importer: import into datasets/<name>, then lock and admit the new suite. */
export function importCommand(name: string): string {
	return [
		`heimdall import ${name} -from datasets/${name} -suite ${name}`,
		`heimdall suite lock ${name}`,
		`heimdall suite admit ${name}`,
	].join("\n");
}