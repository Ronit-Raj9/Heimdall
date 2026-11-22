import "@/lib/i18n";

import type { Catalog } from "@/lib/types/bench";
import { hostedSession } from "@/lib/testing/platformFixtures";
import { loadingQuery, mutation, okQuery, render } from "@/lib/testing/render";
import fs from "node:fs";
import path from "node:path";
import { beforeEach, describe, expect, it, vi } from "vitest";
import NewExperimentView from "./newExperimentView";

const catalog = JSON.parse(fs.readFileSync(path.resolve(__dirname, "../../../../public/fixtures/catalog.json"), "utf8")) as Catalog;
const api = vi.hoisted(() => ({ suites: {} as unknown }));

vi.mock("nuqs", async (importOriginal) => ({
	...(await importOriginal<typeof import("nuqs")>()),
	useQueryState: () => [null, () => undefined],
}));
vi.mock("@/components/models/modelPicker", async () => await import("@/lib/testing/modelPickerStub"));
vi.mock("@/components/experiments/experiments.utils", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/components/experiments/experiments.utils")>()),
	readWriteToken: () => "",
}));
vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("@/lib/store/apis/modelsApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/modelsApi")>()),
	useGetModelsQuery: () => okQuery({ keys: [] }),
}));
vi.mock("@/lib/store/apis/benchApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/benchApi")>()),
	useGetCatalogQuery: () => okQuery(catalog),
	useGetAgentsQuery: () => okQuery({ agents: [] }),
	useGetHealthQuery: () => okQuery({ status: "ok", version: "dev" }),
	useGetSuitesQuery: () => api.suites,
	useGetExperimentQuery: () => ({}),
	usePlanExperimentMutation: () => mutation(),
	useCreateExperimentMutation: () => mutation(),
	useStartExperimentMutation: () => mutation(),
	useEstimateExperimentMutation: () => mutation(),
}));

beforeEach(() => {
	api.suites = loadingQuery();
});

describe("New comparison page while the benchmark list loads", () => {
	it("shows the form at once with a note, not a blank page or a skeleton", () => {
		const html = render(<NewExperimentView />, hostedSession("member"));
		expect(html).not.toContain('data-testid="new-experiment-loading"');
		expect(html).toContain('data-testid="builder-mode"');
		expect(html).toContain('data-testid="new-experiment-ready-pending"');
		expect(html).toContain("Checking which tasks are ready.");
	});
	it("drops the note once the list is known", () => {
		api.suites = okQuery({ suites: [] });
		const html = render(<NewExperimentView />, hostedSession("member"));
		expect(html).toContain('data-testid="builder-mode"');
		expect(html).not.toContain("new-experiment-ready-pending");
	});
});
