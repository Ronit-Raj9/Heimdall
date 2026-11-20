import type { ExportRunRow } from "@/lib/types/bench";
import fs from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

// The Go side writes the key list (bench/handlers/testdata/contract/ExportRunRow.keys), one key a line,
// "?" for an optional key; bench/export/eee_test.go holds export.RunRow to it. The typecheck holds the
// spelling below to the type, and this test holds it to the key list.
const CONTRACT_DIR = path.resolve(__dirname, "../../../handlers/testdata/contract");

// {} extends Pick<T, K> holds exactly when K is optional in T.
type KeySpec<T> = { [K in keyof T]-?: {} extends Pick<T, K> ? "optional" : "required" };

const RUN_ROW = {
	exp_id: "required",
	run_id: "required",
	slot_id: "required",
	pair_id: "required",
	suite: "required",
	task: "required",
	task_version: "required",
	arm: "required",
	arm_label: "required",
	agent_config: "required",
	model: "required",
	provider: "required",
	condition: "required",
	trial: "required",
	attempt: "required",
	outcome: "required",
	split: "required",
	verified_success: "required",
	outcome_score: "required",
	grade_passed: "required",
	grade_source: "required",
	cost_usd: "required",
	synthetic: "required",
	started: "optional",
	sealed: "optional",
	manifest_hash: "required",
	seal_hash: "required",
	problems: "optional",
	metrics: "required",
} satisfies KeySpec<ExportRunRow>;

describe("ExportRunRow contract", () => {
	it("matches the Go key list", () => {
		const lines = fs
			.readFileSync(path.join(CONTRACT_DIR, "ExportRunRow.keys"), "utf8")
			.split("\n")
			.map((l) => l.trim())
			.filter((l) => l.length > 0 && !l.startsWith("#"));
		const want = Object.fromEntries(lines.map((l) => [l.replace(/\?$/, ""), l.endsWith("?") ? "optional" : "required"]));
		expect(RUN_ROW).toEqual(want);
	});
});