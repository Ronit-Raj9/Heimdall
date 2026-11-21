import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
	WRITE_TOKEN_KEY,
	isWorkersOff,
	normalizeCatalog,
	normalizeWorkers,
	prepareBenchHeaders,
	rateLimitedError,
	writeType,
	readWriteToken,
	saveWriteToken,
} from "./benchApi";
import { CSRF_HEADER, ORG_HEADER, setAuthState } from "./session";

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

describe("write token", () => {
	let storage: Storage;
	beforeEach(() => {
		storage = memoryStorage();
		vi.stubGlobal("window", { sessionStorage: storage, location: { search: "" } });
	});
	afterEach(() => {
		saveWriteToken("");
		vi.unstubAllGlobals();
	});

	it("is stored under the key the new-experiment form uses", () => {
		saveWriteToken("secret");
		expect(storage.getItem(WRITE_TOKEN_KEY)).toBe("secret");
		expect(WRITE_TOKEN_KEY).toBe("bifrost-bench.write-token");
		expect(readWriteToken()).toBe("secret");
	});

	it("rides on mutations only", () => {
		saveWriteToken(" secret ");
		expect(prepareBenchHeaders(new Headers(), "mutation").get("Authorization")).toBe("Bearer secret");
		expect(prepareBenchHeaders(new Headers(), "query").get("Authorization")).toBeNull();
		expect(prepareBenchHeaders(new Headers(), "query").get("Content-Type")).toBe("application/json");
	});

	it("is not sent when none was entered", () => {
		expect(prepareBenchHeaders(new Headers(), "mutation").has("Authorization")).toBe(false);
	});

	it("survives blocked storage for this page view", () => {
		vi.stubGlobal("window", {
			location: { search: "" },
			get sessionStorage(): Storage {
				throw new Error("blocked");
			},
		});
		saveWriteToken("secret");
		expect(readWriteToken()).toBe("secret");
	});
});
describe("workers", () => {
	it("reads enabled false or a 404 as worker mode being off, and nothing else", () => {
		expect(isWorkersOff(undefined, { enabled: false })).toBe(true);
		expect(isWorkersOff(undefined, { enabled: true })).toBe(false);
		expect(isWorkersOff({ status: 404, data: {} })).toBe(true);
		expect(isWorkersOff({ status: 503, data: {} })).toBe(false);
		expect(isWorkersOff({ status: "FETCH_ERROR", error: "x" })).toBe(false);
		expect(isWorkersOff(undefined)).toBe(false);
	});

	it("reads a missing or null worker list as empty", () => {
		expect(normalizeWorkers({ mode: "live", workers: null }).workers).toEqual([]);
		expect(normalizeWorkers({ mode: "live" }).workers).toEqual([]);
		expect(normalizeWorkers(null).workers).toEqual([]);
		const w = { id: "w1", host: "h", version: "v", state: "idle", slots: 1, running: [], completed: 0, last_seen: "" };
		expect(normalizeWorkers({ mode: "recorded", enabled: true, workers: [w] })).toEqual({ mode: "recorded", enabled: true, workers: [w] });
	});
});
describe("hosted mode headers", () => {
	beforeEach(() => {
		vi.stubGlobal("window", { sessionStorage: memoryStorage(), location: { search: "" } });
	});
	afterEach(() => {
		saveWriteToken("");
		setAuthState({ mode: "local" });
		vi.unstubAllGlobals();
	});

	it("sends the session's CSRF value on every mutation and never a write token", () => {
		saveWriteToken("left-over-local-token");
		setAuthState({ mode: "hosted", csrf: "csrf-123" });
		const mutation = prepareBenchHeaders(new Headers(), "mutation");
		expect(mutation.get(CSRF_HEADER)).toBe("csrf-123");
		expect(mutation.has("Authorization")).toBe(false);
		expect(CSRF_HEADER).toBe("X-Heimdall-CSRF");
	});

	it("sends no CSRF header on queries", () => {
		setAuthState({ mode: "hosted", csrf: "csrf-123" });
		expect(prepareBenchHeaders(new Headers(), "query").has(CSRF_HEADER)).toBe(false);
	});

	// 4-A-06: a stale tab names the org it displays, so the server never resolves another tab's switch.
	it("names the tab's displayed org on every hosted request", () => {
		setAuthState({ mode: "hosted", csrf: "csrf-123", org: "org_a" });
		expect(prepareBenchHeaders(new Headers(), "mutation").get(ORG_HEADER)).toBe("org_a");
		expect(prepareBenchHeaders(new Headers(), "query").get(ORG_HEADER)).toBe("org_a");
		expect(ORG_HEADER).toBe("X-Heimdall-Org");
		setAuthState({ mode: "hosted", csrf: "csrf-123" });
		expect(prepareBenchHeaders(new Headers(), "mutation").has(ORG_HEADER)).toBe(false);
		setAuthState({ mode: "local", org: "org_a" });
		expect(prepareBenchHeaders(new Headers(), "mutation").has(ORG_HEADER)).toBe(false);
	});

	it("keeps local mode exactly as before: the write token, no CSRF header", () => {
		saveWriteToken("secret");
		setAuthState({ mode: "local" });
		const mutation = prepareBenchHeaders(new Headers(), "mutation");
		expect(mutation.get("Authorization")).toBe("Bearer secret");
		expect(mutation.has(CSRF_HEADER)).toBe(false);
	});
});

// U2: GET /catalog from a bench root without an agents/ folder sent agents: null.
describe("normalizeCatalog", () => {
	it("reads null lists as empty and keeps the rest", () => {
		const out = normalizeCatalog({
			mode: "live",
			suites: [{ id: "s", version: "1", tasks: null, probes: ["p"] }],
			agents: null,
			probes: null,
		});
		expect(out.agents).toEqual([]);
		expect(out.probes).toEqual([]);
		expect(out.metrics).toEqual([]);
		expect(out.gates).toEqual([]);
		expect(out.parts).toEqual({});
		expect(out.suites[0]).toMatchObject({ id: "s", tasks: [], probes: ["p"] });
		expect(out.mode).toBe("live");
	});
});
describe("writeType", () => {
	it("sends write credentials on a POST declared as a query (the live estimate)", () => {
		expect(writeType("query", { url: "/evaluations/estimate", method: "POST", body: {} })).toBe("mutation");
		expect(writeType("query", "/suites")).toBe("query");
		expect(writeType("query", { url: "/suites" })).toBe("query");
		expect(writeType("mutation", "/x")).toBe("mutation");
	});
});

describe("rate limited answers (X16)", () => {
	it("says how long to wait from Retry-After, in plain words", async () => {
		await import("@/lib/i18n");
		expect(rateLimitedError("7")).toEqual({ status: 429, data: { error: { message: "Too many requests. Try again in 7 seconds." } } });
		expect(rateLimitedError("1")).toEqual({ status: 429, data: { error: { message: "Too many requests. Try again in 1 second." } } });
		expect(rateLimitedError(null)).toEqual({ status: 429, data: { error: { message: "Too many requests. Try again in a moment." } } });
	});
});
