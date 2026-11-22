import "@/lib/i18n";

import { render } from "@/lib/testing/render";
import type { SuiteSummary } from "@/lib/types/bench";
import { describe, expect, it, vi } from "vitest";
import { SuitesList } from "./suitesView";

// C1.search: My benchmarks has the Catalog tab's search and filters. C1.name: a benchmark the team
// added reads as its id, exactly as typed.

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("@/components/suites/suiteRunButton", () => ({ SuiteRunButton: () => null }));

const suite = (id: string, extra: Partial<SuiteSummary> = {}) =>
	({
		id,
		version: "",
		description: "",
		tasks: [],
		task_count: 3,
		admitted_count: 3,
		lock_state: "locked",
		...extra,
	}) as unknown as SuiteSummary;

const suites = [suite("hd-swe-verified", { builtin: true, source: "library" }), suite("zz-test-own")];

describe("My benchmarks list", () => {
	it("has a search box, a category filter and a source filter", () => {
		const html = render(<SuitesList suites={suites} />);
		expect(html).toContain('data-testid="suites-toolbar"');
		expect(html).toContain('data-testid="suites-search"');
		expect(html).toContain('data-testid="suites-filter-category"');
		expect(html).toContain('data-testid="suites-source"');
		expect(html).toContain("Added by your team");
	});

	it("shows a team benchmark by the id the team typed", () => {
		const html = render(<SuitesList suites={suites} />);
		expect(html).toContain(">zz-test-own<");
		expect(html).not.toContain("Zz Test Own");
	});

	it("narrows the list and says when nothing matches", () => {
		const one = render(<SuitesList suites={suites} initialFilters={{ query: "zz-test", category: null, source: "all" }} />);
		expect(one).toContain("suites-row-zz-test-own");
		expect(one).not.toContain("suites-row-hd-swe-verified");
		const none = render(<SuitesList suites={suites} initialFilters={{ query: "no such thing", category: null, source: "all" }} />);
		expect(none).toContain('data-testid="suites-no-match"');
		expect(none).toContain("No benchmarks match. Clear the search or filters.");
	});
});