import { BENCH_API_BASE, resolveFixture } from "@/lib/store/apis/fixtures";
import type {
	InboxAcceptResult,
	InboxActionResult,
	InboxDraft,
	InboxIngestResult,
	InboxItem,
	InboxItemDetail,
	InboxList,
	InboxReleaseStatus,
	InboxSignatureParts,
	InboxSourceStatus,
	InboxSuite,
	InboxTaskLink,
	InboxTraceRef,
} from "@/lib/types/bench";
import fs from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import {
	INBOX_ACCEPT_FIXTURE,
	INBOX_ACCEPTED_DETAIL_FIXTURE,
	INBOX_INGEST_FIXTURE,
	INBOX_LIST_FIXTURE,
	INBOX_NEW_DETAIL_FIXTURE,
} from "./inbox.fixtures";

// The Go side writes one key list per type (bench/handlers/testdata/contract/<Type>.keys): one key a
// line, "?" for an optional (omitempty) key. Two checks keep the screen on that contract:
//  1. Each Inbox type in bench.ts is spelled out below as key -> "required" | "optional". The
//     typecheck holds the spelling to the type (a missing, extra or wrongly optional key fails
//     tsc), and this test holds it to the key list.
//  2. The fixtures the tests and fixture mode use carry exactly the listed keys.
const CONTRACT_DIR = path.resolve(__dirname, "../../../handlers/testdata/contract");

/** A type's keys, each marked required or optional the way the type declares it. */
// {} extends Pick<T, K> holds exactly when K is optional in T.
type KeySpec<T> = { [K in keyof T]-?: {} extends Pick<T, K> ? "optional" : "required" };

const INBOX_TYPES = {
	InboxList: {
		items: "required",
		counts: "required",
		sources: "required",
		suites: "required",
		ingest_enabled: "required",
		regression_tasks: "required",
	} satisfies KeySpec<InboxList>,
	InboxItem: {
		id: "required",
		state: "required",
		source: "required",
		source_kind: "required",
		trace_id: "required",
		link: "optional",
		title: "required",
		flagged: "required",
		signature: "required",
		signature_parts: "required",
		occurrences: "required",
		first_seen: "required",
		last_seen: "required",
		traces: "required",
		suggested_kind: "required",
		suggested_suite: "optional",
		tools: "optional",
		model: "optional",
		outcome: "required",
		redacted: "optional",
		task: "optional",
		recurrences: "optional",
		last_recurrence: "optional",
		release: "optional",
		reason: "optional",
		duplicate_of: "optional",
		snoozed_until: "optional",
		updated: "required",
		golden: "optional",
	} satisfies KeySpec<InboxItem>,
	InboxSignatureParts: { request: "required", tool: "optional", error: "optional" } satisfies KeySpec<InboxSignatureParts>,
	InboxTraceRef: { id: "required", source: "required", at: "required", link: "optional" } satisfies KeySpec<InboxTraceRef>,
	InboxTaskLink: {
		suite: "required",
		id: "required",
		dir: "required",
		accepted_at: "required",
		admitted: "required",
	} satisfies KeySpec<InboxTaskLink>,
	InboxReleaseStatus: {
		status: "required",
		experiment: "optional",
		at: "optional",
		passed: "required",
		runs: "required",
	} satisfies KeySpec<InboxReleaseStatus>,
	InboxSourceStatus: {
		name: "required",
		kind: "required",
		last_pull: "optional",
		since: "optional",
		last_error: "optional",
		pulled: "optional",
	} satisfies KeySpec<InboxSourceStatus>,
	InboxSuite: { id: "required", template: "required" } satisfies KeySpec<InboxSuite>,
	InboxItemDetail: { item: "required", preview: "required", session: "optional" } satisfies KeySpec<InboxItemDetail>,
	InboxDraft: {
		task: "required",
		dir: "required",
		template: "required",
		edit_me: "required",
		finished: "required",
		next: "required",
		warnings: "optional",
	} satisfies KeySpec<InboxDraft>,
	InboxAcceptResult: { item: "required", draft: "required" } satisfies KeySpec<InboxAcceptResult>,
	InboxActionResult: { item: "required" } satisfies KeySpec<InboxActionResult>,
	InboxIngestResult: { item_id: "required", created: "required", occurrences: "required", skipped: "optional" } satisfies KeySpec<InboxIngestResult>,
} as const;

type JsonObject = Record<string, unknown>;

function contractKeys(type: string): { required: string[]; optional: string[] } {
	const lines = fs
		.readFileSync(path.join(CONTRACT_DIR, `${type}.keys`), "utf8")
		.split("\n")
		.map((line) => line.trim())
		.filter((line) => line.length > 0 && !line.startsWith("#"));
	return {
		required: lines.filter((line) => !line.endsWith("?")),
		optional: lines.filter((line) => line.endsWith("?")).map((line) => line.slice(0, -1)),
	};
}

function keyProblems(where: string, obj: JsonObject, type: string, topLevel: boolean): string[] {
	const keys = contractKeys(type);
	const need = topLevel && !keys.required.includes("mode") ? [...keys.required, "mode"] : keys.required;
	// "_comment" labels a fixture file as synthetic and is allowed at the top level.
	const allowed = new Set([...need, ...keys.optional, ...(topLevel ? ["_comment"] : [])]);
	return [
		...need.filter((key) => !(key in obj)).map((key) => `${where}: missing ${key}`),
		...Object.keys(obj)
			.filter((key) => !allowed.has(key))
			.map((key) => `${where}: ${key} is not in ${type}`),
	];
}

const asObject = (value: unknown) => value as JsonObject;
const sorted = (keys: string[]) => [...keys].sort();

describe("Inbox types in bench.ts", () => {
	it("cover every Inbox key list in the contract folder", () => {
		const files = fs
			.readdirSync(CONTRACT_DIR)
			.filter((file) => file.startsWith("Inbox") && file.endsWith(".keys"))
			.map((file) => file.slice(0, -".keys".length));
		expect(sorted(files)).toEqual(sorted(Object.keys(INBOX_TYPES)));
	});

	for (const [type, spec] of Object.entries(INBOX_TYPES)) {
		it(`${type} has exactly the listed keys, optional where the list says so`, () => {
			const keys = contractKeys(type);
			const entries = Object.entries(spec as Record<string, string>);
			expect(sorted(entries.filter(([, v]) => v === "required").map(([k]) => k))).toEqual(sorted(keys.required));
			expect(sorted(entries.filter(([, v]) => v === "optional").map(([k]) => k))).toEqual(sorted(keys.optional));
		});
	}
});

function itemProblems(where: string, item: InboxItem): string[] {
	return [
		...keyProblems(where, asObject(item), "InboxItem", false),
		...keyProblems(`${where}.signature_parts`, asObject(item.signature_parts), "InboxSignatureParts", false),
		...item.traces.flatMap((trace, i) => keyProblems(`${where}.traces #${i}`, asObject(trace), "InboxTraceRef", false)),
		...(item.task ? keyProblems(`${where}.task`, asObject(item.task), "InboxTaskLink", false) : []),
		...(item.release ? keyProblems(`${where}.release`, asObject(item.release), "InboxReleaseStatus", false) : []),
	];
}

describe("inbox fixtures", () => {
	it("follow the Go contract key lists", () => {
		const list = INBOX_LIST_FIXTURE;
		const problems = [
			...keyProblems("list", asObject(list), "InboxList", true),
			...list.items.flatMap((item, i) => itemProblems(`list.items #${i}`, item)),
			...list.sources.flatMap((s, i) => keyProblems(`list.sources #${i}`, asObject(s), "InboxSourceStatus", false)),
			...list.suites.flatMap((s, i) => keyProblems(`list.suites #${i}`, asObject(s), "InboxSuite", false)),
			...[INBOX_NEW_DETAIL_FIXTURE, INBOX_ACCEPTED_DETAIL_FIXTURE].flatMap((detail, i) => [
				...keyProblems(`detail #${i}`, asObject(detail), "InboxItemDetail", true),
				...itemProblems(`detail #${i}.item`, detail.item),
			]),
			...keyProblems("accept", asObject(INBOX_ACCEPT_FIXTURE), "InboxAcceptResult", true),
			...itemProblems("accept.item", INBOX_ACCEPT_FIXTURE.item),
			...keyProblems("accept.draft", asObject(INBOX_ACCEPT_FIXTURE.draft), "InboxDraft", false),
			...keyProblems("ingest", asObject(INBOX_INGEST_FIXTURE), "InboxIngestResult", true),
		];
		expect(problems).toEqual([]);
	});

	it("are synthetic, count every item by state and carry each detail's item as listed", () => {
		expect(INBOX_LIST_FIXTURE.mode).toBe("synthetic");
		const byState: Record<string, number> = {};
		for (const item of INBOX_LIST_FIXTURE.items) byState[item.state] = (byState[item.state] ?? 0) + 1;
		expect(INBOX_LIST_FIXTURE.counts).toEqual(byState);
		for (const detail of [INBOX_NEW_DETAIL_FIXTURE, INBOX_ACCEPTED_DETAIL_FIXTURE]) {
			expect(detail.mode).toBe("synthetic");
			expect(INBOX_LIST_FIXTURE.items.find((item) => item.id === detail.item.id)).toEqual(detail.item);
		}
		for (const item of INBOX_LIST_FIXTURE.items) {
			expect(item.id).toMatch(/^inc-[0-9a-f]{12}$/);
			expect(item.traces.length).toBeLessThanOrEqual(20);
		}
	});

	it("answer the inbox routes in fixture mode, whatever the query", () => {
		expect(resolveFixture("GET", `${BENCH_API_BASE}/inbox?state=new&source=prod-langfuse`)).toBe("inbox.json");
		expect(resolveFixture("GET", "/inbox/inc-3f9a1c2b7d10")).toBe("inbox.inc-3f9a1c2b7d10.json");
		expect(resolveFixture("GET", "/inbox/..%2F..%2Fsecret")).toBeNull();
		expect(resolveFixture("POST", "/inbox/inc-3f9a1c2b7d10/accept")).toBeNull();
	});
});