import type { SuitesResponse } from "@/lib/types/bench";
import { configureStore } from "@reduxjs/toolkit";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { benchApi, saveWriteToken } from "./benchApi";
import { suiteBuildApi } from "./suiteBuildApi";

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

const list = {
	mode: "live",
	suites: [
		{ id: "own", tasks: [{ id: "t1", split: "public" }, { id: "t2" }] },
		{ id: "other", tasks: [{ id: "t1", split: "public" }] },
	],
} as unknown as SuitesResponse;

describe("setTaskSplit (N118)", () => {
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
				if (request.method === "PUT") {
					return new Response(JSON.stringify({ suite: "own", task: "t1", split: "held_out" }), {
						status: 200,
						headers: { "Content-Type": "application/json" },
					});
				}
				// After the first read, the list refetch answers slowly with the old state, as a busy server would.
				if (requests.filter((r) => r.method === "GET").length > 1) await new Promise((r) => setTimeout(r, 500));
				return new Response(JSON.stringify(list), { status: 200, headers: { "Content-Type": "application/json" } });
			}),
		);
	});

	afterEach(() => {
		saveWriteToken("");
		vi.unstubAllGlobals();
	});

	it("patches the cached suite list as soon as the PUT succeeds", async () => {
		saveWriteToken("s3cret");
		const store = configureStore({
			reducer: { [benchApi.reducerPath]: benchApi.reducer },
			middleware: (getDefaultMiddleware) => getDefaultMiddleware().concat(benchApi.middleware),
		});
		const page = store.dispatch(benchApi.endpoints.getSuites.initiate()); // the page holds the list
		await page;
		await store.dispatch(suiteBuildApi.endpoints.setTaskSplit.initiate({ suite: "own", task: "t1", held_out: true }));
		const cached = benchApi.endpoints.getSuites.select()(store.getState()).data;
		expect(cached?.suites[0].tasks[0].split).toBe("held_out");
		expect(cached?.suites[1].tasks[0].split).toBe("public");
		expect(requests.some((r) => r.method === "PUT" && r.url.endsWith("/suites/own/tasks/t1/split"))).toBe(true);
		page.unsubscribe();
	});
});