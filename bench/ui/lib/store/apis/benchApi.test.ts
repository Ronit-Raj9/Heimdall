import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { WRITE_TOKEN_KEY, isWorkersOff, normalizeWorkers, prepareBenchHeaders, readWriteToken, saveWriteToken } from "./benchApi";

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