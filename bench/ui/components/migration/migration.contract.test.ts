import fs from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import { COST_FIXTURE, MIGRATION_FIXTURE, PARETO_FIXTURE } from "./migration.fixtures";

// The Go side writes one key list per type (bench/handlers/testdata/contract/<Type>.keys). The
// fixtures here were written by the real commands, and must keep following the lists, so the
// screens are built against the payload the server sends.
const CONTRACT_DIR = path.resolve(__dirname, "../../../handlers/testdata/contract");

type JsonObject = Record<string, unknown>;

function contractKeys(type: string): { required: string[]; optional: string[] } {
	const lines = fs
		.readFileSync(path.join(CONTRACT_DIR, `${type}.keys`), "utf8")
		.split("\n")
		.map((line) => line.trim())
		.filter((line) => line.length > 0 && !line.startsWith("#"));
	return { required: lines.filter((l) => !l.endsWith("?")), optional: lines.filter((l) => l.endsWith("?")).map((l) => l.slice(0, -1)) };
}

function keyProblems(where: string, obj: unknown, type: string, topLevel = false): string[] {
	const o = obj as JsonObject;
	const keys = contractKeys(type);
	const need = topLevel ? [...keys.required, "mode"] : keys.required;
	const allowed = new Set([...need, ...keys.optional]);
	return [
		...need.filter((k) => !(k in o)).map((k) => `${where}: missing ${k}`),
		...Object.keys(o)
			.filter((k) => !allowed.has(k))
			.map((k) => `${where}: ${k} is not in ${type}`),
	];
}

function costProblems(where: string, e: unknown, topLevel = false): string[] {
	const c = e as typeof COST_FIXTURE;
	return [
		...keyProblems(where, c, "CostExplanation", topLevel),
		...keyProblems(`${where}.a`, c.a, "CostArm"),
		...keyProblems(`${where}.b`, c.b, "CostArm"),
		...[c.per_run, c.per_success].flatMap((w, i) => [
			...keyProblems(`${where}.waterfall ${i}`, w, "CostWaterfall"),
			...w.parts.flatMap((p, j) => keyProblems(`${where}.waterfall ${i} part ${j}`, p, "CostPart")),
		]),
		...c.apart.flatMap((a, i) => keyProblems(`${where}.apart ${i}`, a, "CostApart")),
		...c.cache.flatMap((f, i) => keyProblems(`${where}.cache ${i}`, f, "CostCacheFinding")),
	];
}

describe("migration fixtures", () => {
	it("follow the Go contract key lists", () => {
		const r = MIGRATION_FIXTURE;
		const problems = [
			...keyProblems("report", r, "MigrationReport", true),
			...keyProblems("report.from", r.from, "MigrationArmRef"),
			...r.models.flatMap((m, i) => keyProblems(`models ${i}`, m, "MigrationModelSummary")),
			...r.pairs.flatMap((p, i) => [
				...keyProblems(`pair ${i}`, p, "MigrationPair"),
				...keyProblems(`pair ${i} overall`, p.overall, "MigrationVerdict"),
				...p.strata.flatMap((s, j) => keyProblems(`pair ${i} stratum ${j}`, s, "MigrationVerdict")),
				...keyProblems(`pair ${i} recommendation`, p.recommendation, "MigrationRecommendation"),
				...keyProblems(`pair ${i} behaviour`, p.behaviour, "MigrationBehaviour"),
				...keyProblems(`pair ${i} false completion`, p.behaviour.false_completion, "MigrationChange"),
				...p.behaviour.conditions.flatMap((c, j) => keyProblems(`pair ${i} condition ${j}`, c, "MigrationConditionDiff")),
				...[...p.behaviour.new_clusters, ...p.behaviour.fixed_clusters].flatMap((c, j) =>
					keyProblems(`pair ${i} cluster ${j}`, c, "MigrationClusterRef"),
				),
				...p.flipped.flatMap((f, j) => [
					...keyProblems(`pair ${i} flipped ${j}`, f, "MigrationFlippedTask"),
					...f.runs.flatMap((run, k) => keyProblems(`pair ${i} flipped ${j} run ${k}`, run, "MigrationRunRef")),
				]),
				...(p.cost ? costProblems(`pair ${i} cost`, p.cost) : []),
			]),
			...(r.pareto ? keyProblems("report.pareto", r.pareto, "Pareto") : []),
			...keyProblems("pareto", PARETO_FIXTURE, "Pareto", true),
			...PARETO_FIXTURE.points.flatMap((p, i) => keyProblems(`pareto point ${i}`, p, "ParetoPoint")),
			...costProblems("cost", COST_FIXTURE, true),
		];
		expect(problems).toEqual([]);
	});
});