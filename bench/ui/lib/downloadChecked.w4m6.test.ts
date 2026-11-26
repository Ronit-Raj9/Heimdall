import "@/lib/i18n";

import { afterEach, describe, expect, it, vi } from "vitest";
import { downloadChecked } from "./downloadChecked";

afterEach(() => vi.unstubAllGlobals());

describe("downloadChecked (p12-1)", () => {
	it("returns the server's refusal and saves nothing", async () => {
		const body = JSON.stringify({ error: { message: "Nothing to export yet: no task of this benchmark is both ready and public." } });
		vi.stubGlobal(
			"fetch",
			vi.fn(async () => new Response(body, { status: 409, headers: { "Content-Type": "application/json" } })),
		);
		expect(await downloadChecked("/api/bench/suites/x/export?format=harbor", "x.harbor.tar.gz")).toBe(
			"Nothing to export yet: no task of this benchmark is both ready and public.",
		);
	});

	it("says the server could not be reached", async () => {
		vi.stubGlobal(
			"fetch",
			vi.fn(async () => Promise.reject(new TypeError("Failed to fetch"))),
		);
		expect(await downloadChecked("/x", "x")).toBe("Could not reach the server. Check your connection, then try again.");
	});

	it("saves the file and returns null when the answer is ok", async () => {
		const click = vi.fn();
		vi.stubGlobal(
			"fetch",
			vi.fn(async () => new Response("tar", { status: 200 })),
		);
		vi.stubGlobal("URL", Object.assign(URL, { createObjectURL: () => "blob:x", revokeObjectURL: () => undefined }));
		vi.stubGlobal("document", {
			createElement: () => ({ click, set href(_v: string) {}, set download(_v: string) {}, remove: () => undefined }),
			body: { appendChild: () => undefined },
		});
		expect(await downloadChecked("/x", "x.tar.gz")).toBeNull();
		expect(click).toHaveBeenCalled();
	});
});