import "@/lib/i18n";

import { LOCAL_SESSION, hostedSession } from "@/lib/testing/platformFixtures";
import { errorQuery, okQuery, render, tagOf } from "@/lib/testing/render";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import DocsView, { docTitles, docsSearchTerm, groupDocs } from "./docsView";

const api = vi.hoisted(() => ({
	list: null as unknown,
	doc: null as unknown,
	listArgs: [] as unknown[],
	selected: "hosting" as string | null,
	titles: [] as (string | undefined)[],
}));

vi.mock("nuqs", async (importOriginal) => ({
	...(await importOriginal<typeof import("nuqs")>()),
	// Only ?doc= is set in these tests; the ?q= filter is empty.
	useQueryState: (key: string) => [key === "doc" ? api.selected : null, () => undefined],
}));
vi.mock("@/lib/store/apis/welcomeApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/welcomeApi")>()),
	useListDocsQuery: (arg: unknown) => {
		api.listArgs.push(arg);
		return api.list;
	},
	useGetDocQuery: () => api.doc,
}));
vi.mock("@/lib/store/apis/publicSiteApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/publicSiteApi")>()),
	useListPublicDocsQuery: (arg: unknown) => {
		api.listArgs.push(arg);
		return api.list;
	},
	useGetPublicDocQuery: () => api.doc,
}));
vi.mock("@/lib/routing/pageTitle", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/routing/pageTitle")>()),
	usePageTitle: (title: string | undefined) => {
		api.titles.push(title);
	},
}));

const LIST = {
	docs: [
		{ name: "hosting", title: "Hosting" },
		{ name: "web-quickstart", title: "Quick start on the website" },
	],
};

beforeEach(() => {
	api.list = okQuery(LIST);
	api.doc = okQuery({ name: "hosting", title: "Hosting", markdown: "# Hosting\n\nRun `curl http://127.0.0.1:8090/healthz`.\n" });
	api.listArgs = [];
	api.selected = "hosting";
	api.titles = [];
});

afterEach(() => vi.unstubAllGlobals());

describe("DocsView", () => {
	// N138: guide entries are links, so they open in a new tab and can be copied.
	it("lists each guide as a link to its address", () => {
		const html = render(<DocsView />);
		const tag = tagOf(html, "docs-item-hosting");
		expect(tag).toMatch(/^<a /);
		expect(tag).toContain('href="/docs?doc=hosting"');
		expect(tag).toContain('aria-current="page"');
	});

	// N138: an unknown guide says so plainly, without a Retry.
	it("says there is no guide of that name, with no Retry", () => {
		api.selected = "zz-nope";
		api.doc = errorQuery(404, "no guide named zz-nope: GET /api/bench/docs lists them");
		const html = render(<DocsView />);
		expect(html).toContain("There is no guide named zz-nope. Pick one from the list.");
		expect(html).not.toContain("Retry");
		expect(html).not.toContain('data-testid="doc-error"');
	});

	it("keeps the error with Retry for other failures", () => {
		api.doc = errorQuery(500, "boom");
		expect(render(<DocsView />)).toContain('data-testid="doc-error"');
	});

	// N138: 2 to 80 characters search the guides' bodies on the server.
	it("searches the server from 2 characters", () => {
		expect(docsSearchTerm("")).toBeUndefined();
		expect(docsSearchTerm(" g ")).toBeUndefined();
		expect(docsSearchTerm(" gV ")).toBe("gV");
		expect(docsSearchTerm("x".repeat(81))).toBeUndefined();
		render(<DocsView />);
		expect(api.listArgs).toContain(undefined);
	});

	// N139: the tab names the guide.
	it("titles the tab with the guide's name", () => {
		render(<DocsView />);
		expect(api.titles).toContain("Hosting · Docs · Heimdall");
		api.doc = okQuery(undefined);
		api.titles = [];
		render(<DocsView />);
		expect(api.titles).not.toContain("Hosting · Docs · Heimdall");
	});

	// N136: on a hosted server the samples point at this server, with a note saying so.
	it("points local server addresses at this server for a hosted reader", () => {
		vi.stubGlobal("window", { location: { origin: "https://heimdall.example", pathname: "/docs" } });
		const hosted = render(<DocsView />, hostedSession("member"));
		expect(hosted).toContain("https://heimdall.example/healthz");
		expect(hosted).not.toContain("127.0.0.1:8090");
		expect(hosted).toContain('data-testid="docs-local-note"');
		const local = render(<DocsView />, LOCAL_SESSION);
		expect(local).toContain("http://127.0.0.1:8090/healthz");
		expect(local).not.toContain("docs-local-note");
	});

	// M8-64: the guides come in groups; M8-62: with a guide open the article comes first on a phone.
	it("groups the guides and puts the article first on a phone", () => {
		expect(groupDocs([{ name: "zz" }, { name: "web-x" }, { name: "glossary" }]).map((g) => g.group)).toEqual(["start", "web", "other"]);
		expect(docTitles([{ name: "a", title: "A" }])).toEqual({ a: "A" });
		const html = render(<DocsView />);
		expect(html).toContain('data-testid="docs-group-start"');
		expect(html).toContain('data-testid="docs-list-toggle"');
		expect(html).toContain("All guides (2)");
		expect(html).toContain("order-first");
		// M8-65: one h1, the guide's own title.
		expect(html.match(/<h1/g)?.length).toBe(1);
	});
});
