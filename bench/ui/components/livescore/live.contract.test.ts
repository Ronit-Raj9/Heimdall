import fs from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import { LIVE_COMPARE_FIXTURE, LIVE_PULL_FIXTURE, LIVE_SESSIONS_FIXTURE, LIVE_STATUS_FIXTURE } from "./live.fixtures";
import type {
	LiveComparison,
	LiveGroupingView,
	LiveJudgeView,
	LiveLogEvent,
	LivePrivacyView,
	LivePullResult,
	LiveSeriesBucket,
	LiveSessionList,
	LiveSessionRecord,
	LiveSignalDef,
	LiveSignalMean,
	LiveSignalResult,
	LiveSignals,
	LiveSourceView,
	LiveStatus,
	LiveVersionView,
} from "./livescore.types";

// The Go side writes one key list per type (bench/handlers/testdata/contract/Live*.keys). Each type
// is spelled out below as key -> "required" | "optional"; the typecheck holds the spelling to the
// TypeScript type and this test holds it to the key list. The fixtures must carry exactly those keys.
const CONTRACT_DIR = path.resolve(__dirname, "../../../handlers/testdata/contract");

// {} extends Pick<T, K> holds exactly when K is optional in T.
type KeySpec<T> = {
	[K in keyof T]-?: {} extends Pick<T, K> ? "optional" : "required";
};

const LIVE_TYPES = {
	LiveStatus: {
		evidence: "required",
		note: "required",
		configured: "required",
		problem: "optional",
		source: "optional",
		versions: "required",
		signals: "required",
		grouping: "required",
		compare: "optional",
		worst: "required",
		events: "required",
		privacy: "required",
		judge: "optional",
	} satisfies KeySpec<LiveStatus>,
	LiveSourceView: {
		url: "required",
		token_env: "optional",
		read_only: "required",
		last_pull: "optional",
		since: "optional",
		last_error: "optional",
		last_scored: "required",
		warnings: "optional",
		schedule: "optional",
	} satisfies KeySpec<LiveSourceView>,
	LiveVersionView: {
		name: "required",
		selector: "required",
		sessions: "required",
		flagged: "required",
		last_24h: "required",
		series: "required",
	} satisfies KeySpec<LiveVersionView>,
	LiveSignalMean: {
		signal: "required",
		mean: "required",
		n: "required",
	} satisfies KeySpec<LiveSignalMean>,
	LiveSeriesBucket: {
		start: "required",
		sessions: "required",
		means: "required",
	} satisfies KeySpec<LiveSeriesBucket>,
	LiveGroupingView: {
		by_session_id: "required",
		by_gap: "required",
		concurrent: "required",
		gap_seconds: "required",
		reliability: "required",
	} satisfies KeySpec<LiveGroupingView>,
	LivePrivacyView: {
		sample_rate: "required",
		retention_days: "required",
		stores_text: "required",
		inbox_redacted: "required",
		inbox_push: "required",
		inbox_max_per_pull: "required",
	} satisfies KeySpec<LivePrivacyView>,
	LiveJudgeView: {
		model: "required",
		sample_rate: "required",
		calibrated: "required",
		note: "required",
	} satisfies KeySpec<LiveJudgeView>,
	LiveComparison: {
		evidence: "required",
		baseline: "required",
		candidate: "required",
		result: "required",
		alpha: "required",
		per_test_level: "required",
		threshold: "required",
		min_pairs: "required",
		started_at: "required",
		decided_at: "optional",
		sessions_baseline: "required",
		sessions_candidate: "required",
		restarts: "required",
		signals: "required",
		method: "required",
		stale: "optional",
	} satisfies KeySpec<LiveComparison>,
	LiveSignalResult: {
		name: "required",
		label: "required",
		kind: "required",
		lower_is_better: "required",
		unit: "optional",
		pairs: "required",
		need_pairs: "required",
		sessions_baseline: "required",
		sessions_candidate: "required",
		mean_baseline: "optional",
		mean_candidate: "optional",
		e_worse: "required",
		e_better: "required",
		decision: "required",
		decided_at: "optional",
		dropped: "optional",
	} satisfies KeySpec<LiveSignalResult>,
	LiveSessionRecord: {
		seq: "required",
		id: "required",
		version: "required",
		evidence: "required",
		grouping: "required",
		concurrent: "optional",
		started: "required",
		ended: "required",
		scored_at: "required",
		requests: "required",
		model: "optional",
		last_row: "optional",
		signals: "required",
		flags: "optional",
		severity: "optional",
		signature: "optional",
		inbox_item: "optional",
	} satisfies KeySpec<LiveSessionRecord>,
	LiveSignals: {
		request_errors: "required",
		request_error_rate: "optional",
		tool_calls: "optional",
		tool_errors: "optional",
		tool_error_rate: "optional",
		repeated_calls: "optional",
		loop: "optional",
		retries: "required",
		fallbacks: "required",
		refusal: "optional",
		turn_latency_ms: "optional",
		max_turn_latency_ms: "optional",
		session_ms: "optional",
		cost_usd: "optional",
		tokens: "optional",
		budget_hits: "required",
		rate_limited: "required",
		injection_markers: "optional",
		abandoned: "required",
		judge_score: "optional",
		judge_calibrated: "optional",
		content_hidden: "optional",
	} satisfies KeySpec<LiveSignals>,
	LiveSignalDef: {
		name: "required",
		label: "required",
		kind: "required",
		lower_is_better: "required",
		unit: "optional",
		method: "required",
	} satisfies KeySpec<LiveSignalDef>,
	LiveLogEvent: {
		at: "required",
		event: "required",
		detail: "optional",
	} satisfies KeySpec<LiveLogEvent>,
	LiveSessionList: {
		evidence: "required",
		total: "required",
		sessions: "required",
	} satisfies KeySpec<LiveSessionList>,
	LivePullResult: {
		at: "required",
		window: "required",
		rows: "required",
		sessions: "required",
		skipped: "required",
		open: "required",
		details: "required",
		unchecked: "optional",
		overlap: "optional",
		inbox: "required",
		alerts: "required",
		decided: "required",
		pruned: "optional",
		truncated: "required",
		warnings: "optional",
		judged: "optional",
		errors: "optional",
	} satisfies KeySpec<LivePullResult>,
} as const;

function contractKeys(type: string): {
	required: string[];
	optional: string[];
} {
	const lines = fs
		.readFileSync(path.join(CONTRACT_DIR, `${type}.keys`), "utf8")
		.split("\n")
		.map((l) => l.trim())
		.filter((l) => l !== "" && !l.startsWith("#"));
	return {
		required: lines.filter((l) => !l.endsWith("?")),
		optional: lines.filter((l) => l.endsWith("?")).map((l) => l.slice(0, -1)),
	};
}

const sorted = (xs: readonly string[]) => [...xs].sort();

/** Problems of one object's keys against a key list; top-level responses also carry "mode". */
function keyProblems(where: string, obj: object, type: string, topLevel: boolean): string[] {
	const { required, optional } = contractKeys(type);
	const req = topLevel ? [...required, "mode"] : required;
	const keys = Object.keys(obj);
	return [
		...req.filter((k) => !keys.includes(k)).map((k) => `${where}: missing ${k}`),
		...keys.filter((k) => !req.includes(k) && !optional.includes(k)).map((k) => `${where}: extra ${k}`),
	];
}

function sessionProblems(where: string, s: LiveSessionRecord): string[] {
	return [...keyProblems(where, s, "LiveSessionRecord", false), ...keyProblems(`${where}.signals`, s.signals, "LiveSignals", false)];
}

function compareProblems(where: string, c: LiveComparison, top: boolean): string[] {
	return [
		...keyProblems(where, c, "LiveComparison", top),
		...c.signals.flatMap((r, i) => keyProblems(`${where}.signals #${i}`, r, "LiveSignalResult", false)),
	];
}

describe("live scoring contract", () => {
	it("has a key list for every Live type and no other", () => {
		const files = fs
			.readdirSync(CONTRACT_DIR)
			.filter((f) => f.startsWith("Live") && f.endsWith(".keys"))
			.map((f) => f.slice(0, -".keys".length));
		expect(sorted(files)).toEqual(sorted(Object.keys(LIVE_TYPES)));
	});

	for (const [type, spec] of Object.entries(LIVE_TYPES)) {
		it(`${type} has exactly the listed keys, optional where the list says so`, () => {
			const keys = contractKeys(type);
			const entries = Object.entries(spec as Record<string, string>);
			expect(sorted(entries.filter(([, v]) => v === "required").map(([k]) => k))).toEqual(sorted(keys.required));
			expect(sorted(entries.filter(([, v]) => v === "optional").map(([k]) => k))).toEqual(sorted(keys.optional));
		});
	}

	it("fixtures follow the key lists", () => {
		const st = LIVE_STATUS_FIXTURE;
		const problems = [
			...keyProblems("status", st, "LiveStatus", true),
			...(st.source ? keyProblems("status.source", st.source, "LiveSourceView", false) : ["status: no source"]),
			...st.versions.flatMap((v, i) => [
				...keyProblems(`versions #${i}`, v, "LiveVersionView", false),
				...v.last_24h.flatMap((m, j) => keyProblems(`versions #${i}.last_24h #${j}`, m, "LiveSignalMean", false)),
				...v.series.flatMap((b, j) => [
					...keyProblems(`versions #${i}.series #${j}`, b, "LiveSeriesBucket", false),
					...b.means.flatMap((m, k) => keyProblems(`versions #${i}.series #${j}.means #${k}`, m, "LiveSignalMean", false)),
				]),
			]),
			...st.signals.flatMap((d, i) => keyProblems(`signals #${i}`, d, "LiveSignalDef", false)),
			...keyProblems("grouping", st.grouping, "LiveGroupingView", false),
			...(st.compare ? compareProblems("status.compare", st.compare, false) : ["status: no compare"]),
			...st.worst.flatMap((s, i) => sessionProblems(`worst #${i}`, s)),
			...st.events.flatMap((e, i) => keyProblems(`events #${i}`, e, "LiveLogEvent", false)),
			...keyProblems("privacy", st.privacy, "LivePrivacyView", false),
			...(st.judge ? keyProblems("judge", st.judge, "LiveJudgeView", false) : ["status: no judge"]),
			...compareProblems("compare", LIVE_COMPARE_FIXTURE, true),
			...keyProblems("sessions", LIVE_SESSIONS_FIXTURE, "LiveSessionList", true),
			...LIVE_SESSIONS_FIXTURE.sessions.flatMap((s, i) => sessionProblems(`sessions #${i}`, s)),
			...keyProblems("pull", LIVE_PULL_FIXTURE, "LivePullResult", true),
		];
		expect(problems).toEqual([]);
	});

	it("labels every live response with the live evidence class", () => {
		expect(LIVE_STATUS_FIXTURE.evidence).toBe("live");
		expect(LIVE_COMPARE_FIXTURE.evidence).toBe("live");
		expect(LIVE_SESSIONS_FIXTURE.evidence).toBe("live");
		for (const s of [...LIVE_STATUS_FIXTURE.worst, ...LIVE_SESSIONS_FIXTURE.sessions]) expect(s.evidence).toBe("live");
	});
});
