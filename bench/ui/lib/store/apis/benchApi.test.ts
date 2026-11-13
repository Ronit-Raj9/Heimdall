import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { WRITE_TOKEN_KEY, prepareBenchHeaders, readWriteToken, saveWriteToken } from "./benchApi";

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