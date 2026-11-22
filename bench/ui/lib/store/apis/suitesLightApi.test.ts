import { configureStore } from "@reduxjs/toolkit";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { benchApi } from "./benchApi";
import { suitesLightApi } from "./suitesLightApi";

describe("getSuitesLight (E1)", () => {
	let urls: string[];

	beforeEach(() => {
		urls = [];
		vi.stubGlobal("window", {
			location: { search: "" },
			sessionStorage: { getItem: () => null, setItem: () => undefined, removeItem: () => undefined },
		});
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
				urls.push(request.url);
				return new Response(JSON.stringify({ mode: "live", suites: [] }), { status: 200, headers: { "Content-Type": "application/json" } });
			}),
		);
	});

	afterEach(() => vi.unstubAllGlobals());

	it("asks the light list", async () => {
		const store = configureStore({
			reducer: { [benchApi.reducerPath]: benchApi.reducer },
			middleware: (getDefaultMiddleware) => getDefaultMiddleware().concat(benchApi.middleware),
		});
		await store.dispatch(suitesLightApi.endpoints.getSuitesLight.initiate());
		expect(urls).toHaveLength(1);
		expect(new URL(urls[0]).pathname.endsWith("/suites")).toBe(true);
		expect(new URL(urls[0]).searchParams.get("fields")).toBe("light");
	});
});