import "@/lib/i18n";

import type { CanaryDefinition } from "@/lib/types/bench";
import { configureStore } from "@reduxjs/toolkit";
import { renderToStaticMarkup } from "react-dom/server";
import { Provider } from "react-redux";
import { describe, expect, it, vi } from "vitest";

// N194: the task picker reads the picked benchmark from GET /suites/{id}, never the full list.
const calls = vi.hoisted(() => ({ one: [] as string[], list: 0 }));
vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("@/lib/store/apis/benchApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/benchApi")>()),
	useGetAgentsQuery: () => ({ data: { agents: [] }, isSuccess: true }),
	useGetSuitesQuery: () => {
		calls.list += 1;
		return { data: undefined, isLoading: true };
	},
}));
vi.mock("@/lib/store/apis/suitesLightApi", () => ({
	useGetSuitesLightQuery: () => ({ data: { suites: [{ id: "coding-core", version: "1.0.0" }] }, isSuccess: true }),
	useGetSuiteQuery: (id: string, opts?: { skip?: boolean }) => {
		if (!opts?.skip) calls.one.push(id);
		const s = { id: "coding-core", version: "1.0.0", tasks: [{ id: "t1" }, { id: "t2" }] };
		return { data: s, currentData: s, isLoading: false, isFetching: false, isSuccess: true };
	},
}));

const { NewCanaryForm } = await import("./newCanaryForm");
const { benchApi } = await import("@/lib/store/apis/benchApi");
const store = configureStore({ reducer: { [benchApi.reducerPath]: benchApi.reducer }, middleware: (g) => g().concat(benchApi.middleware) });

const def = {
	name: "watch",
	agent: "a1",
	suite: "coding-core@1.0.0",
	tasks: ["t1"],
	trials: 1,
	targets: [{ provider: "openai", model: "gpt-x" }],
	budget: { per_run_usd: 1, per_day_usd: 2 },
} as unknown as CanaryDefinition;

describe("canary task picker (N194)", () => {
	it("lists the tasks of the picked benchmark from the single benchmark route", () => {
		const html = renderToStaticMarkup(
			<Provider store={store}>
				<NewCanaryForm initial={def} onCreated={() => undefined} onClose={() => undefined} />
			</Provider>,
		);
		expect(calls.one).toContain("coding-core");
		expect(calls.list).toBe(0);
		expect(html).toContain('data-testid="canary-new-task-list"');
		expect(html).toContain("t2");
	});
});