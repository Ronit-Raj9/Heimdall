import "@/lib/i18n";

import type { PublicBenchmark, PublicLibrary } from "@/lib/types/site";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";

// N149: the header totals follow the pack filter and the search, and each card title opens the benchmark.

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
const state = vi.hoisted(() => ({ pack: "voice", session: { signedIn: true, authMode: "hosted" } as Record<string, unknown> }));
vi.mock("nuqs", async (importOriginal) => ({
	...(await importOriginal<typeof import("nuqs")>()),
	useQueryState: () => [state.pack, () => Promise.resolve(new URLSearchParams())],
}));
vi.mock("@/lib/session/sessionContext", () => ({
	useSession: () => state.session,
	signupOffered: (s: Record<string, unknown>) => s.signupOpen === true,
}));
const lib = vi.hoisted(() => ({ data: undefined as unknown }));
vi.mock("@/lib/store/apis/publicSiteApi", () => ({ useGetPublicLibraryQuery: () => ({ data: lib.data, isLoading: false }) }));

import LibraryView, { libraryTotals } from "./libraryView";

const bench = (id: string, packs: string[], tasks: number, ready: number): PublicBenchmark =>
	({
		id,
		title: id.toUpperCase(),
		description: "",
		packs,
		task_count: tasks,
		ready_count: ready,
		licenses: [],
	}) as unknown as PublicBenchmark;
const others = Array.from({ length: 54 }, (_, i) => bench(`b${i}`, ["coding"], 10, 5));
const library = {
	available: true,
	totals: { benchmarks: 57, tasks: 1000, ready: 500 },
	packs: [
		{ id: "voice", name: "Voice" },
		{ id: "coding", name: "Coding" },
	],
	benchmarks: [bench("v1", ["voice"], 10, 4), bench("v2", ["voice"], 20, 5), bench("v3", ["voice"], 30, 6), ...others],
} as unknown as PublicLibrary;

describe("LibraryView", () => {
	it("sums the shown benchmarks when a filter is set", () => {
		const shown = library.benchmarks.slice(0, 3);
		expect(libraryTotals(library, shown, true)).toEqual({ filtered: true, shown: 3, benchmarks: 57, tasks: 60, ready: 15 });
		expect(libraryTotals(library, library.benchmarks, false)).toEqual({
			filtered: false,
			shown: 57,
			benchmarks: 57,
			tasks: 1000,
			ready: 500,
		});
	});

	it("says Showing 3 of 57 benchmarks for ?pack=voice and links cards to the benchmark", () => {
		lib.data = library;
		state.pack = "voice";
		state.session = { signedIn: true, authMode: "hosted" };
		const html = renderToStaticMarkup(<LibraryView />);
		expect(html).toContain("Showing 3 of 57 benchmarks: 60 tasks, 15 ready");
		expect(html).toMatch(/href="\/suites\/v1"[^>]*data-testid="library-open-v1"/);
	});

	it("keeps the whole-library line with no filter", () => {
		lib.data = library;
		state.pack = "";
		const html = renderToStaticMarkup(<LibraryView />);
		expect(html).toContain("57 benchmarks, 1,000 tasks, 500 ready");
		expect(html).not.toContain("Showing");
	});

	it("sends a hosted visitor to sign in first", () => {
		lib.data = library;
		state.pack = "voice";
		state.session = { signedIn: false, authMode: "hosted" };
		const html = renderToStaticMarkup(<LibraryView />);
		expect(html).toMatch(/href="\/login\?return_to=%2Fsuites%2Fv1"[^>]*data-testid="library-open-v1"/);
		expect(html).toContain("Sign in to open");
		// The sign-up page does not keep return_to, so even where sign-up is open the card sends to sign in.
		state.session = { signedIn: false, authMode: "hosted", signupOpen: true };
		expect(renderToStaticMarkup(<LibraryView />)).toMatch(/href="\/login\?return_to=%2Fsuites%2Fv1"/);
		// A local server has no sign-in.
		state.session = { signedIn: false, authMode: "local" };
		expect(renderToStaticMarkup(<LibraryView />)).toMatch(/href="\/suites\/v1"[^>]*data-testid="library-open-v1"/);
	});
});