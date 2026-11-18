import type { SuiteList, SuiteTask } from "@/lib/types/bench";
import fs from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import {
	admissionState,
	capabilityCoverage,
	controlRows,
	filterOptions,
	filterTasks,
	formatBytes,
	leaderboardSuite,
	licenseSummary,
	nextTaskSort,
	parseTaskSort,
	probesByFamily,
	sortTasks,
} from "./suites.utils";

const list = JSON.parse(fs.readFileSync(path.resolve(__dirname, "../../public/fixtures/suites.json"), "utf8")) as SuiteList;
const suite = (id: string) => list.suites.find((s) => s.id === id)!;
const acme = suite("acme-payments");
const core = suite("coding-core");
const harbor = suite("harbor-shell");
const ids = (tasks: readonly SuiteTask[]) => tasks.map((task) => task.id);

describe("admissionState", () => {
	it("is admitted only when usable now, stale when files drifted, else the lock status", () => {
		expect(acme.tasks.map(admissionState)).toEqual(["admitted", "stale", "draft"]);
		expect(harbor.tasks.map(admissionState)).toEqual(["not_locked", "not_locked"]);
		const failed = { ...acme.tasks[0], admitted: false };
		expect(admissionState(failed)).toBe("draft");
		expect(admissionState({ ...failed, status: "quarantined" })).toBe("quarantined");
	});
});

describe("sort", () => {
	it("parses URL values with a safe fallback", () => {
		expect(parseTaskSort(null, null)).toEqual({ key: "id", dir: "asc" });
		expect(parseTaskSort("difficulty", "desc")).toEqual({ key: "difficulty", dir: "desc" });
		expect(parseTaskSort("drop table", "sideways")).toEqual({ key: "id", dir: "asc" });
	});

	it("flips the same column and starts another ascending", () => {
		expect(nextTaskSort({ key: "id", dir: "asc" }, "id")).toEqual({ key: "id", dir: "desc" });
		expect(nextTaskSort({ key: "id", dir: "desc" }, "kind")).toEqual({ key: "kind", dir: "asc" });
	});

	it("orders difficulty easy to hard and admission usable first", () => {
		expect(ids(sortTasks(core.tasks, { key: "difficulty", dir: "asc" }))).toEqual(["t02", "t01", "t03", "t04"]);
		expect(ids(sortTasks(core.tasks, { key: "difficulty", dir: "desc" }))).toEqual(["t03", "t04", "t01", "t02"]);
		expect(ids(sortTasks(acme.tasks, { key: "admission", dir: "desc" }))).toEqual(["p03", "p02", "p01"]);
	});

	it("keeps unknown values last in both directions", () => {
		const unset = { ...core.tasks[0], id: "t00", difficulty: "" };
		const tasks = [unset, ...core.tasks];
		expect(ids(sortTasks(tasks, { key: "difficulty", dir: "asc" })).at(-1)).toBe("t00");
		expect(ids(sortTasks(tasks, { key: "difficulty", dir: "desc" })).at(-1)).toBe("t00");
		expect(ids(sortTasks(harbor.tasks, { key: "license", dir: "asc" }))).toEqual(["h01", "h02"]);
		expect(ids(sortTasks(harbor.tasks, { key: "license", dir: "desc" }))).toEqual(["h01", "h02"]);
	});
});

describe("filters", () => {
	it("match every set filter", () => {
		const none = { kind: null, difficulty: null, capability: null, status: null };
		expect(ids(filterTasks(core.tasks, { ...none, kind: "bugfix" }))).toEqual(["t01", "t03"]);
		expect(ids(filterTasks(core.tasks, { ...none, kind: "bugfix", difficulty: "hard" }))).toEqual(["t03"]);
		expect(ids(filterTasks(core.tasks, { ...none, capability: "test_discipline" }))).toEqual(["t01", "t03"]);
		expect(ids(filterTasks(acme.tasks, { ...none, status: "stale" }))).toEqual(["p02"]);
		expect(filterTasks(core.tasks, none)).toHaveLength(core.tasks.length);
	});

	it("offer only the values the suite has", () => {
		const options = filterOptions(acme.tasks);
		expect(options.kinds).toEqual(["bugfix", "feature", "refactor"]);
		expect(options.difficulties).toEqual(["easy", "medium", "hard"]);
		expect(options.statuses).toEqual(["admitted", "stale", "draft"]);
		expect(options.capabilities).toContain("error_recovery");
	});
});

describe("summaries", () => {
	it("count tasks per capability, most covered first", () => {
		const coverage = capabilityCoverage(core.tasks);
		expect(coverage.rows[0]).toEqual({ name: "test_discipline", count: 2 });
		expect(coverage.missing).toBe(0);
		expect(capabilityCoverage([{ ...core.tasks[0], capabilities: [] }]).missing).toBe(1);
	});

	it("count licenses and flag tasks without one", () => {
		expect(licenseSummary(acme.tasks)).toEqual({ rows: [{ name: "proprietary", count: 3 }], missing: [] });
		expect(licenseSummary(harbor.tasks)).toEqual({ rows: [{ name: "Apache-2.0", count: 1 }], missing: ["h02"] });
		expect(licenseSummary(core.tasks).missing).toEqual(["t01", "t02", "t03", "t04"]);
	});

	it("group probes by PS2 family in a fixed order", () => {
		const groups = probesByFamily(core.probes);
		expect(groups.map((g) => g.family)).toEqual(["unavailable_model", "broken_tool", "misleading_instruction"]);
		expect(groups.map((g) => g.probes.length)).toEqual([1, 2, 7]);
		const odd = probesByFamily([{ ...core.probes[0], ps2_family: "new_family" as never }]);
		expect(odd.map((g) => g.family)).toEqual(["new_family"]);
	});

	it("list every contract control, recorded or not", () => {
		const rows = controlRows(core.tasks[0].checks);
		expect(rows.map((r) => r.control)).toEqual(["reference", "empty", "mutant", "exploit", "null_agent", "secrets", "network"]);
		expect(rows.find((r) => r.control === "reference")).toMatchObject({ passed: 3, recorded: true });
		expect(rows.find((r) => r.control === "null_agent")?.recorded).toBe(false);
		const draft = controlRows(acme.tasks[2].checks);
		expect(draft.find((r) => r.control === "mutant")).toMatchObject({ passed: 0, recorded: true });
		expect(draft.find((r) => r.control === "null_agent")).toMatchObject({ passed: 2, recorded: true });
	});
});

describe("small helpers", () => {
	it("name the leaderboard suite and format sizes", () => {
		expect(leaderboardSuite(core)).toBe("coding-core@1.0.0");
		expect(formatBytes(512, "en")).toBe("512 B");
		expect(formatBytes(4210, "en")).toBe("4.1 KB");
		expect(formatBytes(3 * 1024 * 1024, "en")).toBe("3 MB");
	});
});