import { configureStore } from "@reduxjs/toolkit";
import { afterEach, describe, expect, it, vi } from "vitest";
import { governApi } from "@/lib/store/apis/governApi";
import { benchApi } from "@/lib/store/apis/benchApi";
import { setAuthState } from "@/lib/store/apis/session";

// p14-3: a policy save refused with 409 must not refetch the file. The editor is keyed on the file hash,
// so a refetch would remount it and drop the conflict message and the typed edit.

const NodeRequest = globalThis.Request;

afterEach(() => {
	vi.unstubAllGlobals();
});

function stubServer() {
	const calls: string[] = [];
	vi.stubGlobal(
		"Request",
		class extends NodeRequest {
			constructor(input: RequestInfo | URL, init?: RequestInit) {
				super(typeof input === "string" ? new URL(input, "http://localhost") : input, init);
			}
		},
	);
	let sha = "abc";
	vi.stubGlobal("fetch", async (req: Request) => {
		calls.push(req.method);
		const json = (status: number, body: unknown) =>
			new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
		if (req.method === "PUT") {
			sha = "other";
			return json(409, { error: { message: "Someone else changed this file. Reload to see their version, then apply your edit again." } });
		}
		return json(200, { id: "zz", path: "agents/zz.yaml", yaml: "id: zz\n", sha256: sha, fields: {} });
	});
	return calls;
}

describe("policiesView stale save", () => {
	it("409 on save shows the reload message and keeps the edit", async () => {
		setAuthState({ mode: "local" });
		const calls = stubServer();
		const store = configureStore({
			reducer: { [benchApi.reducerPath]: benchApi.reducer },
			middleware: (m) => m().concat(benchApi.middleware),
		});
		const sub = store.dispatch(governApi.endpoints.getPolicy.initiate("zz.yaml"));
		await sub;
		const put = await store.dispatch(
			governApi.endpoints.savePolicy.initiate({ file: "zz.yaml", body: { yaml: "id: zz\n", if_match: "abc" } }),
		);
		expect(put.error).toBeDefined();
		await new Promise((r) => setTimeout(r, 20));
		expect(calls.filter((m) => m === "GET")).toHaveLength(1);
		sub.unsubscribe();
	});
});