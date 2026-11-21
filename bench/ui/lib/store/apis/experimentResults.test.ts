import { configureStore } from "@reduxjs/toolkit";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { adaptiveApi } from "./adaptiveApi";
import { benchApi, experimentResultTags } from "./benchApi";
import { migrationApi } from "./migrationApi";
import { researchApi } from "./researchApi";
import { routingApi } from "./routingApi";
import { supportApi } from "./supportApi";

// QC2-27-02: when a run settles, every derived result of the experiment refetches, including the
// untagged extension summaries that decide which tabs show and the migration and cost reports.

describe("experiment result refresh on settle", () => {
	let settled: boolean;
	let calls: Map<string, number>;

	beforeEach(() => {
		settled = false;
		calls = new Map();
		vi.stubGlobal("window", { location: { search: "" }, sessionStorage: undefined, localStorage: undefined });
		const NodeRequest = globalThis.Request;
		vi.stubGlobal(
			"Request",
			class extends NodeRequest {
				constructor(input: RequestInfo | URL, init?: RequestInit) {
					super(typeof input === "string" ? new URL(input, "http://bench.test") : input, init);
				}
			},
		);
		vi.stubGlobal(
			"fetch",
			vi.fn(async (request: Request) => {
				const path = new URL(request.url).pathname;
				calls.set(path, (calls.get(path) ?? 0) + 1);
				const body: Record<string, unknown> = { mode: "live", research: settled, support: false, routed: false, partial: !settled };
				return new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });
			}),
		);
	});

	afterEach(() => vi.unstubAllGlobals());

	it("refetches research, support, routing, adaptive, migration, cost and Pareto", async () => {
		const store = configureStore({
			reducer: { [benchApi.reducerPath]: benchApi.reducer },
			middleware: (getDefaultMiddleware) => getDefaultMiddleware().concat(benchApi.middleware),
		});
		const subs = [
			store.dispatch(researchApi.endpoints.getExperimentResearch.initiate("e1")),
			store.dispatch(supportApi.endpoints.getExperimentSupport.initiate("e1")),
			store.dispatch(routingApi.endpoints.getExperimentRouting.initiate("e1")),
			store.dispatch(adaptiveApi.endpoints.getExperimentAdaptive.initiate("e1")),
			store.dispatch(migrationApi.endpoints.getMigration.initiate({ expId: "e1" })),
			store.dispatch(migrationApi.endpoints.getCostExplain.initiate({ expId: "e1" })),
			store.dispatch(migrationApi.endpoints.getExperimentPareto.initiate({ expId: "e1" })),
		];
		await Promise.all(subs.map((s) => s.unwrap()));
		expect(researchApi.endpoints.getExperimentResearch.select("e1")(store.getState()).data?.research).toBe(false);

		settled = true;
		store.dispatch(benchApi.util.invalidateTags(experimentResultTags("e1")));
		await vi.waitFor(() => expect(researchApi.endpoints.getExperimentResearch.select("e1")(store.getState()).data?.research).toBe(true));
		for (const suffix of ["research", "support", "routing", "adaptive", "migration", "cost-explain", "pareto"]) {
			await vi.waitFor(() => expect(calls.get(`/api/bench/experiments/e1/${suffix}`)).toBe(2));
		}
		subs.forEach((s) => s.unsubscribe());
	});
});
