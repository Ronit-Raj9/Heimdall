import type { RunDetail, SuiteList, ToolCall } from "@/lib/types/bench";
import fs from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import {
	UNKNOWN_TRACK,
	durationSeconds,
	groupSuitesByTrack,
	hasScenarioEvidence,
	importCommand,
	isJudgedSuite,
	isTrack,
	suiteFactsById,
	suiteGrading,
	suiteIdOf,
	suiteTrack,
	toolCallCounts,
	isToolCallRow,
} from "./scenario.utils";

const list = JSON.parse(fs.readFileSync(path.resolve(__dirname, "../../public/fixtures/suites.json"), "utf8")) as SuiteList;

const call = (seq: number, extra: Partial<ToolCall> = {}): ToolCall => ({
	seq,
	server: "crm",
	tool: "refund",
	protocol: "mcp",
	args_sha256: "a",
	result_sha256: "b",
	is_error: false,
	started: "2026-11-15T09:00:00Z",
	latency_ms: 12,
	...extra,
});

describe("track and grading", () => {
	it("knows the scenario tracks and shows a newer one as sent", () => {
		expect(isTrack("tool_use")).toBe(true);
		expect(isTrack("browser")).toBe(true);
		expect(isTrack("computer_use")).toBe(true);
		expect(isTrack("artifact")).toBe(true);
		expect(isTrack("robotics")).toBe(false);
		expect(isTrack(undefined)).toBe(false);
	});

	it("defaults to coding and verified, as the server does for a suite that sets neither", () => {
		expect(suiteTrack({})).toBe("coding");
		expect(suiteTrack({ track: "memory" })).toBe("memory");
		expect(suiteGrading({})).toBe("verified");
		expect(suiteGrading({ grading: "judged" })).toBe("judged");
	});

	it("leaves a suite that failed to load out of the facts", () => {
		const facts = suiteFactsById(list.suites);
		expect(facts.has("acme-broken")).toBe(false);
		expect(facts.get("memory-recall")).toEqual({ track: "memory", grading: "judged" });
		expect(facts.get("support-desk")).toEqual({ track: "tool_use", grading: "verified" });
	});

	it("reads the suite id of a board value and says when it is judged", () => {
		const facts = suiteFactsById(list.suites);
		expect(suiteIdOf("memory-recall@0.1.0")).toBe("memory-recall");
		expect(suiteIdOf("plain")).toBe("plain");
		expect(isJudgedSuite("memory-recall@0.1.0", facts)).toBe(true);
		expect(isJudgedSuite("coding-core@1.0.0", facts)).toBe(false);
		expect(isJudgedSuite("gone@1.0.0", facts)).toBe(false);
		expect(isJudgedSuite(undefined, facts)).toBe(false);
	});
});

describe("groupSuitesByTrack", () => {
	const facts = new Map([
		["core", { track: "coding" }],
		["desk", { track: "tool_use" }],
		["recall", { track: "memory" }],
		["arm", { track: "robotics" }],
	]);

	it("orders known tracks first, then newer ones by name, then suites not on disk; the board's order holds inside a group", () => {
		const groups = groupSuitesByTrack(["recall@1", "gone@2", "core@2", "arm@1", "desk@1", "core@1"], facts);
		expect(groups).toEqual([
			{ track: "coding", suites: ["core@2", "core@1"] },
			{ track: "tool_use", suites: ["desk@1"] },
			{ track: "memory", suites: ["recall@1"] },
			{ track: "robotics", suites: ["arm@1"] },
			{ track: UNKNOWN_TRACK, suites: ["gone@2"] },
		]);
	});

	it("is empty for an empty board", () => {
		expect(groupSuitesByTrack([], facts)).toEqual([]);
	});

	it("never uses an empty value, which a select item must not have", () => {
		expect(UNKNOWN_TRACK).not.toBe("");
	});
});

describe("hasScenarioEvidence", () => {
	const plain: Pick<RunDetail, "tool_calls" | "sessions" | "answer" | "state" | "services" | "artifacts"> = {};
	const cases = [
		{ name: "plain diff run", detail: plain, want: false },
		{ name: "empty arrays", detail: { tool_calls: [], sessions: [], state: [], services: [] }, want: false },
		{ name: "tool run", detail: { tool_calls: [call(1)] }, want: true },
		{
			name: "session run",
			detail: { sessions: [{ id: "a", index: 1, exit_code: 0, timed_out: false, started: "", ended: "", stdout_path: "" }] },
			want: true,
		},
		{ name: "answer run", detail: { answer: { text: "", source: "missing" as const, sha256: "" } }, want: true },
		{ name: "state run", detail: { state: [{ service: "db", id: "dump", path: "state/db/dump", sha256: "x", bytes: 1 }] }, want: true },
		{ name: "services only", detail: { services: [{ id: "db", image: "postgres:16", ready: true }] }, want: true },
		{ name: "artifact run", detail: { artifacts: [{ id: "report", source: "out/*.csv", bytes: 0, error: "missing" }] }, want: true },
		{ name: "empty artifacts", detail: { artifacts: [] }, want: false },
	];
	for (const c of cases) {
		it(c.name, () => expect(hasScenarioEvidence(c.detail)).toBe(c.want));
	}
});

describe("helpers", () => {
	it("counts calls, errors and injected faults", () => {
		expect(toolCallCounts([call(1), call(2, { is_error: true, fault: "error" }), call(3, { is_error: true })])).toEqual({
			calls: 3,
			errors: 2,
			faults: 1,
			other: 0,
			unparsed: 0,
		});
	});

	it("counts only tool calls as calls: handshakes, listings, notifications and opaque traffic are other rows", () => {
		const rows = [
			call(1, { kind: "mcp_method", tool: "initialize" }),
			call(2, { tool: "tools/list" }),
			call(3),
			call(4, { kind: "mcp_notification", tool: "notifications/initialized" }),
			call(5, {
				kind: "opaque",
				tool: "unparsed",
				unparsed: true,
				unparsed_reason: "request content type application/octet-stream is not JSON",
				is_error: true,
			}),
			call(6, { is_error: true }),
		];
		expect(toolCallCounts(rows)).toEqual({ calls: 2, errors: 1, faults: 0, other: 4, unparsed: 1 });
		expect(rows.map(isToolCallRow)).toEqual([false, false, true, false, false, true]);
	});

	it("measures a session in whole seconds, and nothing when a time is missing or runs backwards", () => {
		expect(durationSeconds("2026-11-15T09:00:00Z", "2026-11-15T09:01:30.4Z")).toBe(90);
		expect(durationSeconds("", "2026-11-15T09:01:30Z")).toBeNull();
		expect(durationSeconds("2026-11-15T09:01:30Z", "2026-11-15T09:00:00Z")).toBeNull();
	});

	it("builds the import, lock and admit commands", () => {
		expect(importCommand("harbor")).toBe(
			"bifrost-bench import harbor -from datasets/harbor -suite harbor\nbifrost-bench suite lock harbor\nbifrost-bench suite admit harbor",
		);
	});
});