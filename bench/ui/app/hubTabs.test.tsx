import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";

// Agent-first nav (X09): Benchmarks, Scheduled checks and Releases hold the old pages as tabs.
const url = vi.hoisted(() => ({ search: {} as Record<string, string | null> }));
vi.mock("nuqs", async (importOriginal) => ({
	...(await importOriginal<typeof import("nuqs")>()),
	useQueryState: (key: string) => [url.search[key] ?? null, () => undefined],
}));
// The Checks header links to the gateway's model watch (Q-273); no router is mounted here.
vi.mock("@tanstack/react-router", async (importOriginal) => ({
	...(await importOriginal<typeof import("@tanstack/react-router")>()),
	Link: ({ children, to }: { children?: React.ReactNode; to?: string }) => <a href={String(to)}>{children}</a>,
}));
// The hub header (X-42) lives in suitesView as SuitesPageHeader; keep it real, stub the list.
vi.mock("@/app/suites/views/suitesView", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/app/suites/views/suitesView")>()),
	default: () => <div data-testid="inner-suites" />,
}));
vi.mock("@/app/catalog/views/catalogView", () => ({ default: () => <div data-testid="inner-catalog" /> }));
vi.mock("@/app/suites_/add/views/addSuiteView", () => ({ default: () => <div data-testid="inner-import" /> }));
vi.mock("@/app/canaries/views/canariesView", () => ({ default: () => <div data-testid="inner-drift" /> }));
vi.mock("@/app/schedules/views/schedulesView", () => ({ default: () => <div data-testid="inner-schedules" /> }));
vi.mock("@/app/releases/views/releasesView", () => ({ default: () => <div data-testid="inner-history" /> }));
vi.mock("@/app/review/views/reviewView", () => ({ default: () => <div data-testid="inner-review" /> }));
vi.mock("@/app/policies/views/policiesView", () => ({ default: () => <div data-testid="inner-rules" /> }));

const { default: SuitesPage } = await import("./suites/page");
const { default: ScheduledChecksPage } = await import("./scheduled-checks/page");
const { default: ReleasesPage } = await import("./releases/page");

function at(tab: string | null, Page: () => React.ReactElement) {
	url.search = { tab };
	return renderToStaticMarkup(<Page />);
}

describe("hub tabs", () => {
	it("Benchmarks: My benchmarks by default and Catalog as tabs; no Import tab (decision 5)", () => {
		const html = at(null, SuitesPage);
		for (const label of ["My benchmarks", "Catalog"]) expect(html).toContain(label);
		expect(html).not.toContain(">Import<");
		expect(html).toMatch(/<h1[^>]*>[^<]*Benchmarks/);
		expect(html).toContain("inner-suites");
		expect(at("catalog", SuitesPage)).toContain("inner-catalog");
		// The route redirects ?tab=import to the catalog (importTabRedirect); the page never mounts the old view.
		expect(at("import", SuitesPage)).not.toContain("inner-import");
	});

	it("Scheduled checks: drift checks and schedules", () => {
		expect(at(null, ScheduledChecksPage)).toContain("inner-drift");
		expect(at("schedules", ScheduledChecksPage)).toContain("inner-schedules");
	});

	it("Releases: history, review and rules", () => {
		const html = at(null, ReleasesPage);
		for (const label of ["Releases", "Waiting for approval", "Rules"]) expect(html).toContain(label);
		expect(html).toContain("inner-history");
		expect(at("review", ReleasesPage)).toContain("inner-review");
		expect(at("rules", ReleasesPage)).toContain("inner-rules");
	});
});
