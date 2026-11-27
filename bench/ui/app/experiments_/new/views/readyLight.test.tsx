import "@/lib/i18n";

import type { Catalog, SuiteSummary } from "@/lib/types/bench";
import { hostedSession } from "@/lib/testing/platformFixtures";
import { loadingQuery, mutation, okQuery, render, tagOf } from "@/lib/testing/render";
import fs from "node:fs";
import path from "node:path";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ExperimentForm } from "./experimentForm";
import { dateSeed, defaultFormValues, type ExperimentFormValues } from "./experimentFormSchema";
import { defaultBuilderValues, fillUntouched, readyTaskMap, toMatrixExperiment, type BuilderValues } from "./matrixBuilderSchema";
import NewExperimentView from "./newExperimentView";

const fixture = JSON.parse(fs.readFileSync(path.resolve(__dirname, "../../../../public/fixtures/catalog.json"), "utf8")) as Catalog;
const bfclTasks = ["bfcl-irrelevance-0", "bfcl-irrelevance-1", "bfcl-simple-0", "bfcl-simple-1", "bfcl-simple-2", "bfcl-simple-3"];
const catalog: Catalog = {
	...fixture,
	suites: [...fixture.suites, { ...fixture.suites[0], id: "bfcl", version: "1.0.0", tasks: bfclTasks, probes: [] }],
};

/** A light list entry: no task arrays, ready ids from the lock. */
function light(id: string, version: string, extra: Partial<SuiteSummary>): SuiteSummary {
	return {
		id,
		version,
		description: "",
		environment: "",
		grader: "",
		verifier_result: "",
		verifier_timeout_s: 0,
		pass_threshold: null,
		base_image: { tag: "", digest: "" },
		lock_hash: "",
		lock_state: "locked",
		locked_at: null,
		drift: [],
		task_count: 0,
		admitted_count: 0,
		probes: [],
		tasks: [],
		...extra,
	} as SuiteSummary;
}

const lightList: SuiteSummary[] = [
	light("coding-core", fixture.suites[0].version, { ready_known: false, partial: true }),
	light("bfcl", "1.0.0", { ready_known: true, ready_task_ids: ["bfcl-simple-0", "bfcl-simple-1", "bfcl-simple-2", "bfcl-simple-3"] }),
];

describe("readyTaskMap over the light list", () => {
	it("reads ready_task_ids and treats ready_known false as unknown, not as zero", () => {
		const ready = readyTaskMap(catalog, lightList)!;
		expect(ready.has(`coding-core@${fixture.suites[0].version}`)).toBe(false);
		expect(ready.get("bfcl@1.0.0")).toEqual({ ids: ["bfcl-simple-0", "bfcl-simple-1", "bfcl-simple-2", "bfcl-simple-3"], total: 6 });
	});
	it("an entry known to have no ready task counts zero", () => {
		const ready = readyTaskMap(catalog, [light("bfcl", "1.0.0", { ready_known: true })])!;
		expect(ready.get("bfcl@1.0.0")).toEqual({ ids: [], total: 6 });
	});
});

describe("First n ready tasks", () => {
	it("sends exactly the first three ready ids, never a draft", () => {
		const ready = readyTaskMap(catalog, lightList);
		const v: BuilderValues = {
			...defaultBuilderValues(catalog, 1, { ready }),
			suite: "bfcl@1.0.0",
			agents: ["agents/claude-code.yaml"],
			models: [
				{ provider: "openai", model: "gpt-5.4" },
				{ provider: "anthropic", model: "claude-sonnet-5" },
			],
			taskScope: "first",
			taskCount: 3,
		};
		const exp = toMatrixExperiment(v, catalog, ready);
		expect(exp.conditions[0].tasks).toEqual({ ids: ["bfcl-simple-0", "bfcl-simple-1", "bfcl-simple-2"] });
	});
});

describe("fillUntouched (no remount when the list arrives)", () => {
	it("keeps typed fields and fills the untouched ones from the new defaults", () => {
		const before = defaultFormValues(catalog, 20261123);
		const typed: ExperimentFormValues = { ...before, name: "my comparison", suite: "bfcl@1.0.0" };
		const next: ExperimentFormValues = { ...before, totalUsd: 30, suite: `coding-core@${fixture.suites[0].version}` };
		const out = fillUntouched(typed, next, (k) => k === "name" || k === "suite");
		expect(out.name).toBe("my comparison");
		expect(out.suite).toBe("bfcl@1.0.0");
		expect(out.totalUsd).toBe(30);
	});
	it("builder: a typed name and picked benchmark survive, the untouched task count follows the ready count", () => {
		const pending = defaultBuilderValues(catalog, 1);
		const typed = { ...pending, name: "typed", suite: "bfcl@1.0.0" };
		const known = { ...defaultBuilderValues(catalog, 1, { ready: readyTaskMap(catalog, lightList) }), taskCount: 4 };
		const out = fillUntouched(typed, known, (k) => k === "name" || k === "suite");
		expect(out.name).toBe("typed");
		expect(out.suite).toBe("bfcl@1.0.0");
		expect(out.taskCount).toBe(4);
	});
});

describe("A/B form native selects (N233)", () => {
	const defaults = defaultFormValues(catalog, 20261123);
	it("renders benchmark, agent, main test and main metric as native selects with their values", () => {
		const html = render(<ExperimentForm catalog={catalog} defaults={defaults} onStarted={() => undefined} />, hostedSession("member"));
		for (const id of [
			"experiment-suite-select",
			"experiment-base-select",
			"experiment-primary-condition-select",
			"experiment-primary-metric-select",
		]) {
			expect(tagOf(html, id) ?? "", id).toMatch(/^<select/);
		}
		expect(html).toMatch(/<option[^>]*value="verified_success"[^>]*selected=""/);
		expect(html).toMatch(/<option[^>]*value="clean"[^>]*selected=""/);
	});
	it("blocks Plan while the ready tasks are checked, and when they cannot be known", () => {
		const pending = render(
			<ExperimentForm catalog={catalog} defaults={defaults} readyPending onStarted={() => undefined} />,
			hostedSession("member"),
		);
		expect(tagOf(pending, "experiment-plan-button")).toContain('disabled=""');
		expect(pending).toContain("Checking which tasks are ready...");
		const ready = readyTaskMap(catalog, lightList);
		const onCore = { ...defaults, suite: `coding-core@${fixture.suites[0].version}` };
		const unknown = render(
			<ExperimentForm catalog={catalog} defaults={onCore} ready={ready} onStarted={() => undefined} />,
			hostedSession("member"),
		);
		expect(tagOf(unknown, "experiment-plan-button")).toContain('disabled=""');
		expect(unknown).toContain("Heimdall cannot tell which tasks of coding-core are ready. Run the readiness checks on its page first.");
	});
});

const api = vi.hoisted(() => ({ full: 0, light: {} as unknown }));
vi.mock("nuqs", async (importOriginal) => ({
	...(await importOriginal<typeof import("nuqs")>()),
	useQueryState: () => [null, () => undefined],
}));
vi.mock("@/components/models/modelPicker", async () => await import("@/lib/testing/modelPickerStub"));
vi.mock("@tanstack/react-router", async () => ({ ...(await import("@/lib/testing/routerMock")).routerMock, useBlocker: () => undefined }));
vi.mock("@/lib/store/apis/modelsApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/modelsApi")>()),
	useGetModelsQuery: () => okQuery({ keys: [] }),
}));
vi.mock("@/lib/store/apis/suitesLightApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/suitesLightApi")>()),
	useGetSuitesLightQuery: () => api.light,
}));
vi.mock("@/lib/store/apis/benchApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/benchApi")>()),
	useGetCatalogQuery: () => okQuery(catalog),
	useGetAgentsQuery: () => okQuery({ agents: [] }),
	useGetHealthQuery: () => okQuery({ status: "ok", version: "dev" }),
	useGetSuitesQuery: () => {
		api.full++;
		return okQuery({ suites: [] });
	},
	useGetExperimentQuery: () => ({}),
	usePlanExperimentMutation: () => mutation(),
	useCreateExperimentMutation: () => mutation(),
	useStartExperimentMutation: () => mutation(),
	useEstimateExperimentMutation: () => mutation(),
}));

describe("New comparison reads only the light list (N194)", () => {
	beforeEach(() => {
		api.full = 0;
	});
	it("never asks for the full benchmark list, loading or loaded", () => {
		api.light = loadingQuery();
		const pending = render(<NewExperimentView />, hostedSession("member"));
		expect(pending).toContain("Checking which tasks are ready...");
		api.light = okQuery({ suites: lightList });
		render(<NewExperimentView />, hostedSession("member"));
		expect(api.full).toBe(0);
	});
});