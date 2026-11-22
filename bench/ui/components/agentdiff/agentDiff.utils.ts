import { shortHash, type Tone } from "@/components/verdict/verdict.utils";
import {
	AGENT_DIFF_GROUPS,
	type AgentDiffClass,
	type AgentDiffConclusion,
	type AgentDiffConfigGroup,
	type AgentDiffDirection,
	type AgentDiffFileChange,
	type AgentDiffKind,
	type AgentDiffParams,
	type AgentDiffTask,
	type AgentDiffVersionInfo,
	type JsonValue,
	type RunDiff,
} from "@/lib/types/bench";

// Pure helpers of the agent diff screen, kept apart from the components so they can be tested
// without rendering.

/** Tone of each task class: clean flips and unstable moves read good or bad, the rest neutral. */
export const CLASS_TONE = {
	flipped_to_pass: "good",
	flipped_to_fail: "bad",
	moved_to_pass: "good",
	moved_to_fail: "bad",
	unstable_in_both: "warn",
	unchanged_pass: "neutral",
	unchanged_fail: "neutral",
} as const satisfies Record<AgentDiffClass, Tone>;

/** Tone of each sign test conclusion. "cannot tell" is a warning, never a pass. */
export const CONCLUSION_TONE = {
	b_better: "good",
	b_worse: "bad",
	no_clear_difference: "neutral",
	cannot_tell: "warn",
} as const satisfies Record<AgentDiffConclusion, Tone>;

/** Tone of a metric delta read with its direction. */
export const DIRECTION_TONE = {
	better: "good",
	worse: "bad",
	same: "neutral",
	unknown: "neutral",
} as const satisfies Record<AgentDiffDirection, Tone>;

/** Tone of a field or file change kind. */
export const KIND_TONE = {
	changed: "warn",
	added: "good",
	removed: "bad",
} as const satisfies Record<AgentDiffKind, Tone>;

/** Value of the synthetic search param that includes synthetic runs. */
export const SYNTHETIC_ON = "1";
export const SYNTHETIC_VALUES = [SYNTHETIC_ON] as const;

/** True for a class where the task's result changed between A and B: a flip or a move. */
export function isChangedClass(cls: string): boolean {
	return cls === "flipped_to_pass" || cls === "flipped_to_fail" || cls === "moved_to_pass" || cls === "moved_to_fail";
}

/** The tasks the table shows: flips and moves by default, every common task with showAll. */
export function visibleTasks(tasks: readonly AgentDiffTask[], showAll: boolean): AgentDiffTask[] {
	return showAll ? [...tasks] : tasks.filter((task) => isChangedClass(task.class));
}

/** Groups in report order; an id this UI does not know goes last, in the order the server sent it. */
export function sortGroups(groups: readonly AgentDiffConfigGroup[]): AgentDiffConfigGroup[] {
	const rank = (id: string) => {
		const i = (AGENT_DIFF_GROUPS as readonly string[]).indexOf(id);
		return i === -1 ? AGENT_DIFF_GROUPS.length : i;
	};
	return groups
		.map((group, index) => ({ group, index }))
		.sort((x, y) => rank(x.group.id) - rank(y.group.id) || x.index - y.index)
		.map(({ group }) => group);
}

/** True when the UI has a translated name for a group id. */
export function isKnownGroup(id: string): id is (typeof AGENT_DIFF_GROUPS)[number] {
	return (AGENT_DIFF_GROUPS as readonly string[]).includes(id);
}

/** Where a task's representative runs open: one pair view, or each run's own pair, or nowhere. */
export type RunLinks = { kind: "pair"; pairId: string } | { kind: "split"; a: string | null; b: string | null } | null;

/** /runs?pair=<pair_id> when both runs are one pair; else a link per run through that run's pair. */
export function runLinks(task: Pick<AgentDiffTask, "pair">): RunLinks {
	const pair = task.pair;
	if (!pair) return null;
	if (pair.pair_id) return { kind: "pair", pairId: pair.pair_id };
	const a = pair.a.pair_id || null;
	const b = pair.b.pair_id || null;
	return a || b ? { kind: "split", a, b } : null;
}

/** A config hash without its "sha256:" prefix, lower case, for matching a selector. */
export function normalizeHash(hash: string): string {
	const trimmed = hash.trim().toLowerCase();
	return trimmed.startsWith("sha256:") ? trimmed.slice("sha256:".length) : trimmed;
}

/** Shortest hash prefix the server accepts as a selector. */
export const MIN_HASH_PREFIX = 8;

/** The version a selector names: the exact hash or a unique prefix of 8+ characters. Arm selectors match nothing here. */
export function findVersion(versions: readonly AgentDiffVersionInfo[], selector: string | null | undefined): AgentDiffVersionInfo | null {
	if (!selector) return null;
	const want = normalizeHash(selector);
	if (want.length === 0) return null;
	const exact = versions.find((v) => normalizeHash(v.config_hash) === want);
	if (exact) return exact;
	if (want.length < MIN_HASH_PREFIX) return null;
	const prefixed = versions.filter((v) => normalizeHash(v.config_hash).startsWith(want));
	return prefixed.length === 1 ? prefixed[0] : null;
}

/** Newest first by latest_at; ties keep the server order. */
export function newestFirst(versions: readonly AgentDiffVersionInfo[]): AgentDiffVersionInfo[] {
	const time = (v: AgentDiffVersionInfo) => {
		const ms = Date.parse(v.latest_at);
		return Number.isNaN(ms) ? 0 : ms;
	};
	return versions
		.map((version, index) => ({ version, index }))
		.sort((x, y) => time(y.version) - time(x.version) || x.index - y.index)
		.map(({ version }) => version);
}

/**
 * The baseline to compare B against when only B is chosen (a link from the leaderboard): the
 * next older version of the same agent id, or null when B is that agent's only version.
 */
export function suggestBaseline(versions: readonly AgentDiffVersionInfo[], b: string | null | undefined): AgentDiffVersionInfo | null {
	const target = findVersion(versions, b);
	if (!target) return null;
	const sameAgent = newestFirst(versions.filter((v) => v.agent_id === target.agent_id));
	const at = sameAgent.indexOf(target);
	return sameAgent[at + 1] ?? null;
}

/**
 * Versions grouped by agent id (sorted). In each group saved versions come first, then setups that
 * were never saved, each part newest first. System checks (oracle, noop, readiness check runs) are
 * left out unless showSystem.
 */
export function groupVersionsByAgent(
	versions: readonly AgentDiffVersionInfo[],
	opts: { showSystem?: boolean } = {},
): { agent: string; versions: AgentDiffVersionInfo[] }[] {
	const byAgent = new Map<string, AgentDiffVersionInfo[]>();
	for (const version of versions) {
		if (version.system && !opts.showSystem) continue;
		const list = byAgent.get(version.agent_id) ?? [];
		list.push(version);
		byAgent.set(version.agent_id, list);
	}
	return [...byAgent.entries()]
		.sort(([x], [y]) => x.localeCompare(y))
		.map(([agent, list]) => {
			const ordered = newestFirst(list);
			return { agent, versions: [...ordered.filter((v) => v.version), ...ordered.filter((v) => !v.version)] };
		});
}

/** How many versions are system checks, hidden from the picker by default. */
export function countSystemVersions(versions: readonly AgentDiffVersionInfo[]): number {
	return versions.filter((v) => v.system).length;
}

/**
 * Picker text of a version. A saved version reads "v<n> · <model> · <hash>"; a setup the server
 * knows was never saved reads "<unsaved> · <model> · <hash>" when the caller passes the word. Rows
 * from an older server keep the old text: label (or agent id), short hash and agent id.
 */
export function versionLabel(
	version: Pick<AgentDiffVersionInfo, "config_hash" | "agent_id" | "label" | "version" | "model">,
	unsaved?: string,
): string {
	if (version.version) return [`v${version.version}`, version.model, shortHash(version.config_hash)].filter(Boolean).join(" · ");
	if (unsaved && version.model) return [unsaved, version.model, shortHash(version.config_hash)].join(" · ");
	const parts = [version.label || version.agent_id, shortHash(version.config_hash)];
	if (version.label) parts.push(version.agent_id);
	return parts.join(" · ");
}

/** Suites both versions ran, in A's order; every suite of the one that is known when only one is. */
export function commonSuites(a: AgentDiffVersionInfo | null, b: AgentDiffVersionInfo | null): string[] {
	if (a && b) return a.suites.filter((suite) => b.suites.includes(suite));
	return [...(a?.suites ?? b?.suites ?? [])];
}

/** The page's search params, as nuqs reads them. */
export interface AgentDiffSearch {
	a: string | null;
	b: string | null;
	suite: string | null;
	condition: string | null;
	agent: string | null;
	synthetic: string | null;
}

/**
 * The diff query for the page's search params, or null when there is nothing to ask yet. A and B
 * win; with only B, A is the suggested baseline when there is one; with neither, an agent id asks
 * for that agent's two newest versions.
 */
export function diffParams(search: AgentDiffSearch, suggestedA: string | null): AgentDiffParams | null {
	const extra: AgentDiffParams = {
		...(search.suite ? { suite: search.suite } : {}),
		...(search.condition ? { condition: search.condition } : {}),
		...(search.synthetic === SYNTHETIC_ON ? { synthetic: SYNTHETIC_ON } : {}),
	};
	const a = search.a || (search.b ? suggestedA : null);
	if (a && search.b) return { a, b: search.b, ...extra };
	if (!search.a && !search.b && search.agent) return { agent: search.agent, ...extra };
	return null;
}

/** A config value as inert text: strings as they are, everything else as JSON; null means unset. */
export function formatConfigValue(value: JsonValue | undefined): string | null {
	if (value === null || value === undefined) return null;
	return typeof value === "string" ? value : JSON.stringify(value);
}

/** A metric value; up to three fraction digits. */
export function formatMetric(value: number, locale: string): string {
	return new Intl.NumberFormat(locale, { maximumFractionDigits: 3 }).format(value);
}

/** A delta with its sign; zero has none. */
export function formatSigned(value: number, locale: string): string {
	return new Intl.NumberFormat(locale, { maximumFractionDigits: 3, signDisplay: "exceptZero" }).format(value);
}

/** A rate in 0..1 as a whole percent. */
export function formatRate(rate: number, locale: string): string {
	return new Intl.NumberFormat(locale, { style: "percent", maximumFractionDigits: 0 }).format(rate);
}

/** A file change's text diff in the shape the run diff view draws, so the same inert renderer shows it. */
export function fileDiffAsRunDiff(file: Pick<AgentDiffFileChange, "path" | "diff">): RunDiff {
	return { run_id: "", diff: { path: file.path, sha256: "", files: [] }, patch: file.diff ?? "" };
}

/** A test id fragment from free text (a task id, a file path). */
export function testIdPart(value: string): string {
	return value.replace(/[^A-Za-z0-9_-]/g, "-");
}