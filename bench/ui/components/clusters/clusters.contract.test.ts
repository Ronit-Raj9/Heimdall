import fs from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import { clusterRequest } from "@/lib/store/apis/benchApi";
import { BENCH_API_BASE, resolveFixture } from "@/lib/store/apis/fixtures";
import { CLUSTER_DETAIL_FIXTURE, CLUSTER_LIST_FIXTURE } from "./clusters.fixtures";

// The Go side writes one key list per type (bench/handlers/testdata/contract/<Type>.keys): one key a
// line, "?" for an optional (omitempty) key. The test fixtures here must follow them, so the screen
// is built against the payload the server sends. A list that does not exist is skipped. The shared
// fixture test (lib/store/apis/fixtures.test.ts) checks the same files; this one keeps the feature's
// check inside its own folder.
const CONTRACT_DIR = path.resolve(__dirname, "../../../handlers/testdata/contract");

type JsonObject = Record<string, unknown>;

function contractKeys(type: string): { required: string[]; optional: string[] } | null {
	const file = path.join(CONTRACT_DIR, `${type}.keys`);
	if (!fs.existsSync(file)) return null;
	const lines = fs
		.readFileSync(file, "utf8")
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
	if (!keys) return [];
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

describe("cluster fixtures", () => {
	it("follow the Go contract key lists", () => {
		const list = asObject(CLUSTER_LIST_FIXTURE);
		const detail = asObject(CLUSTER_DETAIL_FIXTURE);
		const problems = [
			...keyProblems("list", list, "ClusterList", true),
			...keyProblems("list.filters", asObject(CLUSTER_LIST_FIXTURE.filters), "ClusterFilters", false),
			...CLUSTER_LIST_FIXTURE.excluded.flatMap((e, i) => keyProblems(`list.excluded #${i}`, asObject(e), "ClusterExclusion", false)),
			...CLUSTER_LIST_FIXTURE.clusters.flatMap((c, i) => [
				...keyProblems(`list.clusters #${i}`, asObject(c), "ClusterSummary", false),
				...c.layers.flatMap((l, j) => keyProblems(`list.clusters #${i} layers #${j}`, asObject(l), "ClusterLayer", false)),
				...(c.related ?? []).flatMap((r, j) => keyProblems(`list.clusters #${i} related #${j}`, asObject(r), "ClusterRelated", false)),
				...(c.after_config_change
					? keyProblems(`list.clusters #${i} after_config_change`, asObject(c.after_config_change), "ClusterConfigChange", false)
					: []),
			]),
			...keyProblems("detail", detail, "ClusterDetail", true),
			...keyProblems("detail.cluster", asObject(CLUSTER_DETAIL_FIXTURE.cluster), "ClusterSummary", false),
			...CLUSTER_DETAIL_FIXTURE.runs.flatMap((r, i) => keyProblems(`detail.runs #${i}`, asObject(r), "ClusterRun", false)),
		];
		expect(problems).toEqual([]);
	});

	it("answer the cluster routes in fixture mode, whatever the query", () => {
		expect(resolveFixture("GET", `${BENCH_API_BASE}/clusters?suite=coding-core&exp=e_1,e_2&min=2`)).toBe("clusters.json");
		expect(resolveFixture("GET", "/clusters/c_99aabbccddeeff00?synthetic=include")).toBe("clusters.c_99aabbccddeeff00.json");
		expect(resolveFixture("GET", "/clusters/..%2F..%2Fsecret")).toBeNull();
		expect(resolveFixture("POST", "/clusters")).toBeNull();
	});

	it("are synthetic and sorted by count like the server", () => {
		expect(CLUSTER_LIST_FIXTURE.mode).toBe("synthetic");
		expect(CLUSTER_DETAIL_FIXTURE.mode).toBe("synthetic");
		const counts = CLUSTER_LIST_FIXTURE.clusters.map((c) => c.count);
		expect(counts).toEqual([...counts].sort((a, b) => b - a));
		for (const c of CLUSTER_LIST_FIXTURE.clusters) expect(c.signature).toMatch(/^c_[0-9a-f]{16}$/);
	});
});

describe("clusterRequest", () => {
	it("leaves empty, zero and absent params out so the server default applies", () => {
		expect(clusterRequest("/clusters", {})).toBe("/clusters");
		expect(clusterRequest("/clusters", { suite: "", min: 0 })).toBe("/clusters");
		expect(clusterRequest("/clusters/c_1", { exp: "e_1,e_2", min: 3, synthetic: "only" })).toEqual({
			url: "/clusters/c_1",
			params: { exp: "e_1,e_2", min: 3, synthetic: "only" },
		});
	});
});