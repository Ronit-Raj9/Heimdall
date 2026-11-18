import { acceptDefaults, acceptRequestFrom } from "@/components/inbox/inbox.utils";
import { INBOX_ACCEPT_FIXTURE, INBOX_LIST_FIXTURE, INBOX_NEW_DETAIL_FIXTURE } from "@/components/inbox/inbox.fixtures";
import { configureStore } from "@reduxjs/toolkit";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { benchApi, saveWriteToken } from "./benchApi";
import { inboxActionRequest, inboxApi, inboxItemPath, inboxListRequest } from "./inboxApi";

describe("inbox requests", () => {
	it("sends the bare list route when no filter has a value", () => {
		expect(inboxListRequest({})).toBe("/inbox");
		expect(inboxListRequest({ state: undefined, source: "" })).toBe("/inbox");
	});

	it("sends only the filters that have a value", () => {
		expect(inboxListRequest({ state: "new", source: "prod-langfuse" })).toEqual({
			url: "/inbox",
			params: { state: "new", source: "prod-langfuse" },
		});
		expect(inboxListRequest({ state: "snoozed" })).toEqual({ url: "/inbox", params: { state: "snoozed" } });
	});

	it("escapes the item id as one path segment", () => {
		expect(inboxItemPath("inc-3f9a1c2b7d10")).toBe("/inbox/inc-3f9a1c2b7d10");
		expect(inboxItemPath("../x?y")).toBe("/inbox/..%2Fx%3Fy");
		expect(inboxActionRequest("inc-1", "snooze", { days: 7 })).toEqual({ url: "/inbox/inc-1/snooze", method: "POST", body: { days: 7 } });
	});
});

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

/**
 * Drives the real endpoints through a store, with fetch answered here. Node's Request refuses a
 * relative URL, so it is resolved against a placeholder origin; the path is what is checked.
 */
describe("inbox endpoints on the wire", () => {
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
				const url = new URL(request.url);
				const body = url.pathname.endsWith("/accept")
					? INBOX_ACCEPT_FIXTURE
					: url.pathname === "/api/bench/inbox"
						? { ...INBOX_LIST_FIXTURE, mode: "live" }
						: { ...INBOX_NEW_DETAIL_FIXTURE, mode: "live" };
				return new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });
			}),
		);
	});

	afterEach(() => {
		saveWriteToken("");
		vi.unstubAllGlobals();
	});

	const makeStore = () =>
		configureStore({
			reducer: { [benchApi.reducerPath]: benchApi.reducer },
			middleware: (getDefaultMiddleware) => getDefaultMiddleware().concat(benchApi.middleware),
		});

	it("posts the accept form's body with the write token, and refetches the list", async () => {
		saveWriteToken("s3cret");
		const store = makeStore();
		const list = store.dispatch(inboxApi.endpoints.getInbox.initiate({ state: "new" }));
		await list;
		expect(requests).toHaveLength(1);
		expect(new URL(requests[0].url).search).toBe("?state=new");
		expect(requests[0].headers.has("Authorization")).toBe(false);

		const item = INBOX_NEW_DETAIL_FIXTURE.item;
		const body = acceptRequestFrom({ ...acceptDefaults(item, INBOX_LIST_FIXTURE.suites), taskId: "refund-timeout" });
		const result = await store.dispatch(inboxApi.endpoints.acceptInboxItem.initiate({ id: item.id, body })).unwrap();
		expect(result.draft.task).toBe("support/refund-timeout");

		const post = requests[1];
		expect(post.method).toBe("POST");
		expect(new URL(post.url).pathname).toBe("/api/bench/inbox/inc-3f9a1c2b7d10/accept");
		expect(post.headers.get("Authorization")).toBe("Bearer s3cret");
		expect(JSON.parse(await post.text())).toEqual({ suite: "support", kind: "mcp", task_id: "refund-timeout" });

		// The list is subscribed, so the invalidated tag refetches it.
		await vi.waitFor(() => expect(requests.length).toBe(3));
		expect(new URL(requests[2].url).pathname).toBe("/api/bench/inbox");
		list.unsubscribe();
	});

	it("posts reject and snooze bodies to their routes", async () => {
		const store = makeStore();
		await store.dispatch(inboxApi.endpoints.rejectInboxItem.initiate({ id: "inc-1", body: { reason: "expected behaviour" } }));
		await store.dispatch(inboxApi.endpoints.snoozeInboxItem.initiate({ id: "inc-1", body: { days: 14 } }));
		expect(requests.map((r) => [r.method, new URL(r.url).pathname])).toEqual([
			["POST", "/api/bench/inbox/inc-1/reject"],
			["POST", "/api/bench/inbox/inc-1/snooze"],
		]);
		expect(JSON.parse(await requests[0].text())).toEqual({ reason: "expected behaviour" });
		expect(JSON.parse(await requests[1].text())).toEqual({ days: 14 });
	});
});