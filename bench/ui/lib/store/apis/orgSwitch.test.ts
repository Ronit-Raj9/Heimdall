import { configureStore } from "@reduxjs/toolkit";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { benchApi } from "./benchApi";
import { platformApi } from "./platformApi";
import { CSRF_HEADER, getAuthState, ORG_HEADER, ORG_SWITCH_KEY, setAuthState } from "./session";
import { trendsApi } from "./trendsApi";

// 4-A-06 and 4-A-07: an org switch scopes later requests to the new org and leaves no cached
// answer of the old one, including untagged queries such as trends.

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

describe("organization switch", () => {
	let requests: Request[];
	let sessionOrg: string;
	let local: Storage;

	beforeEach(() => {
		requests = [];
		sessionOrg = "org_a";
		local = memoryStorage();
		vi.stubGlobal("window", { location: { search: "" }, sessionStorage: memoryStorage(), localStorage: local });
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
				const path = new URL(request.url).pathname;
				let body: unknown = { mode: "live" };
				if (path.endsWith("/auth/me")) {
					body = {
						mode: "live",
						auth_mode: "hosted",
						kind: "user",
						csrf: "c",
						current_org: sessionOrg,
						orgs: [
							{ id: "org_a", slug: "a", name: "A", role: "owner" },
							{ id: "org_b", slug: "b", name: "B", role: "owner" },
						],
					};
				} else if (path.endsWith("/auth/switch-org")) {
					sessionOrg = "org_b";
					body = { mode: "live", id: "org_b", slug: "b", name: "B", role: "owner" };
				} else if (path.endsWith("/trends")) {
					body = { mode: "live", org: request.headers.get(ORG_HEADER) };
				}
				return new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });
			}),
		);
	});

	afterEach(() => {
		setAuthState({ mode: "local" });
		vi.unstubAllGlobals();
	});

	const makeStore = () =>
		configureStore({
			reducer: { [benchApi.reducerPath]: benchApi.reducer },
			middleware: (getDefaultMiddleware) => getDefaultMiddleware().concat(benchApi.middleware),
		});

	const cachedTrends = (store: ReturnType<typeof makeStore>) => trendsApi.endpoints.getTrends.select(undefined)(store.getState()).data;

	it("drops untagged org data, names the new org, and tells other tabs", async () => {
		const store = makeStore();
		await store.dispatch(platformApi.endpoints.getMe.initiate()).unwrap();
		expect(getAuthState().org).toBe("org_a");
		await store.dispatch(trendsApi.endpoints.getTrends.initiate(undefined)).unwrap();
		expect(cachedTrends(store)).toMatchObject({ org: "org_a" });

		await store.dispatch(platformApi.endpoints.switchOrg.initiate({ org: "org_b" })).unwrap();
		expect(cachedTrends(store)).toBeUndefined();
		expect(getAuthState().org).toBe("org_b");
		expect(local.getItem(ORG_SWITCH_KEY)).toMatch(/^org_b:/);

		const again = await store.dispatch(trendsApi.endpoints.getTrends.initiate(undefined)).unwrap();
		expect(again).toMatchObject({ org: "org_b" });
	});

	it("resets the cache when /auth/me reports another org than the tab displayed (switch in another tab)", async () => {
		const store = makeStore();
		await store.dispatch(platformApi.endpoints.getMe.initiate()).unwrap();
		await store.dispatch(trendsApi.endpoints.getTrends.initiate(undefined)).unwrap();
		expect(cachedTrends(store)).toBeDefined();

		sessionOrg = "org_b";
		await store.dispatch(platformApi.endpoints.getMe.initiate(undefined, { forceRefetch: true })).unwrap();
		expect(getAuthState().org).toBe("org_b");
		expect(cachedTrends(store)).toBeUndefined();
	});

	// 4-A-05: the browser password change on the wire.
	it("posts the password change with the CSRF value and the auth body fields", async () => {
		const store = makeStore();
		await store.dispatch(platformApi.endpoints.getMe.initiate()).unwrap();
		await store.dispatch(platformApi.endpoints.changePassword.initiate({ current_password: "old", new_password: "new-password-123" }));
		const post = requests.at(-1)!;
		expect(post.method).toBe("POST");
		expect(new URL(post.url).pathname).toBe("/api/bench/auth/password");
		expect(post.headers.get(CSRF_HEADER)).toBe("c");
		expect(await post.json()).toEqual({ current_password: "old", new_password: "new-password-123" });
	});
});
