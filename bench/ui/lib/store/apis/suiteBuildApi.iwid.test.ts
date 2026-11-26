import { configureStore } from "@reduxjs/toolkit";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { benchApi, saveWriteToken } from "./benchApi";
import { suiteBuildApi } from "./suiteBuildApi";
import { suitesLightApi } from "./suitesLightApi";

function memoryStorage(): Storage {
	const items = new Map<string, string>();
	return {
		get length() {
			return items.size;
		},
		clear: () => items.clear(),
		getItem: (key) => items.get(key) ?? null,
		key: (i) => [...items.keys()][i] ?? null,
		removeItem: (key) => void items.delete(key),
		setItem: (key, value) => void items.set(key, value),
	};
}

const one = { mode: "live", id: "own", grader: "exact", tasks: [{ id: "t1", split: "public" }, { id: "t2" }] };

// N194: the benchmark page reads GET /suites/{id}; the held-out flip and the grader change patch that entry at once.
describe("getSuite (N194) and the instant cache patches", () => {
	let requests: Request[];

	beforeEach(() => {
		requests = [];
		vi.stubGlobal("window", { location: { search: "" }, sessionStorage: memoryStorage() });
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
				requests.push(request);
				const json = (v: unknown) => new Response(JSON.stringify(v), { status: 200, headers: { "Content-Type": "application/json" } });
				if (request.method === "PUT" && request.url.endsWith("/split")) return json({ suite: "own", task: "t1", split: "held_out" });
				if (request.method === "PUT") return json({ suite: "own", grader: "judge", grading: "judge" });
				// The refetch after the write answers slowly with the old state, as a busy server would.
				if (requests.filter((r) => r.method === "GET").length > 1) await new Promise((r) => setTimeout(r, 500));
				return json(one);
			}),
		);
	});

	afterEach(() => {
		saveWriteToken("");
		vi.unstubAllGlobals();
	});

	it("asks one benchmark by id and patches it after a split and a grader change", async () => {
		saveWriteToken("s3cret");
		const store = configureStore({
			reducer: { [benchApi.reducerPath]: benchApi.reducer },
			middleware: (getDefaultMiddleware) => getDefaultMiddleware().concat(benchApi.middleware),
		});
		const page = store.dispatch(suitesLightApi.endpoints.getSuite.initiate("own"));
		await page;
		expect(requests[0].url).toMatch(/\/suites\/own$/);
		await store.dispatch(suiteBuildApi.endpoints.setTaskSplit.initiate({ suite: "own", task: "t1", held_out: true }));
		expect(suitesLightApi.endpoints.getSuite.select("own")(store.getState()).data?.tasks[0].split).toBe("held_out");
		await store.dispatch(suiteBuildApi.endpoints.setSuiteGrader.initiate({ suite: "own", grader: "judge" } as never));
		expect(suitesLightApi.endpoints.getSuite.select("own")(store.getState()).data?.grader).toBe("judge");
		page.unsubscribe();
	});
});