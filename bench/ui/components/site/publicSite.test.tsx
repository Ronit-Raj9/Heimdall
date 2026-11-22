import "@/lib/i18n";

import DownloadsView, { cliInstallCommand, formatSize, githubStep } from "@/app/downloads/views/downloadsView";
import LibraryView from "@/app/library/views/libraryView";
import PricingView from "@/app/pricing/views/pricingView";
import PrivacyView from "@/app/privacy/views/privacyView";
import SecurityView from "@/app/security/views/securityView";
import TermsView from "@/app/terms/views/termsView";
import { isPublicPage, isVisitorPage, PUBLIC_PAGES } from "@/lib/routing/publicPages";
import { frameFor } from "@/lib/session/sessionContext";
import { hostedSession, LOCAL_SESSION, signedOutSession } from "@/lib/testing/platformFixtures";
import { errorQuery, okQuery, render } from "@/lib/testing/render";
import type { DownloadsManifest, PublicLibrary, PublicPricing } from "@/lib/types/site";
import { NuqsTestingAdapter } from "nuqs/adapters/testing";
import { describe, expect, it, vi } from "vitest";
import { LandingView } from "./landingView";
import { PublicFrame } from "./publicFrame";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

const fx = vi.hoisted(() => ({ downloadsError: false, downloadsEmpty: false }));

const LIBRARY: PublicLibrary = {
	available: true,
	totals: { benchmarks: 2, tasks: 30, ready: 25, packs: 1 },
	benchmarks: [
		{ id: "sweb", title: "SWE tasks", description: "Fix real bugs.", licenses: ["MIT"], task_count: 20, ready_count: 18, packs: ["coding"], track: "coding" },
		{ id: "tau", title: "Retail support", description: "Help customers.", licenses: [], task_count: 10, ready_count: 7, packs: [] },
	],
	packs: [{ id: "coding", name: "Coding", description: "Coding agents", benchmarks: ["sweb"] }],
	updated_at: "2026-11-22T00:00:00Z",
};

const PRICING: PublicPricing = {
	beta: true,
	headline: "Free during beta",
	note: "Bring your own model key.",
	plans: [{ id: "beta", name: "Beta", price: "$0", period: "during beta", summary: "Everything", features: ["All benchmarks"], limits: [{ name: "Model spend", value: "Your key" }], cta: "signup" }],
	faq: [{ q: "Why my key?", a: "Spend stays on your bill." }],
};

const SHA = "a".repeat(64);
const DOWNLOADS: DownloadsManifest = {
	available: true,
	commit: "0123456789abcdef0123",
	version: "v1.2.3",
	base_url: "https://heimdall.example.com",
	mcp_url: "https://heimdall.example.com/mcp",
	gitlab_url: "https://heimdall.example.com/downloads/heimdall.gitlab-ci.yml",
	files: [
		{ name: "bifrost-bench-darwin-arm64", kind: "cli", os: "darwin", arch: "arm64", size: 20 * 1024 * 1024, sha256: SHA },
		{ name: "bifrost-bench-linux-amd64", kind: "cli", os: "linux", arch: "amd64", size: 21 * 1024 * 1024, sha256: SHA },
		{ name: "heimdall_bench-0.2.0-py3-none-any.whl", kind: "python-wheel", size: 64196, sha256: SHA },
		{ name: "heimdall_bench-0.2.0.tar.gz", kind: "python-sdist", size: 59344, sha256: SHA },
		{ name: "heimdall-bench-client-0.2.0.tgz", kind: "typescript", size: 127137, sha256: SHA },
		{ name: "bench-0123456789ab.tar.gz", kind: "source", size: 5 * 1024 * 1024, sha256: SHA },
	],
};

vi.mock("@/lib/store/apis/publicSiteApi", () => ({
	useGetPublicOverviewQuery: () =>
		okQuery({ product: "Heimdall", version: "v1.2.3", commit: "0123456789abcdef", library: LIBRARY.totals, downloads: true, mcp: true }),
	useGetPublicLibraryQuery: () => okQuery(LIBRARY),
	useGetPublicPricingQuery: () => okQuery(PRICING),
	useGetPublicDownloadsQuery: () =>
		fx.downloadsError
			? errorQuery(500)
			: okQuery(fx.downloadsEmpty ? { available: false, commit: "", version: "", files: [] } : DOWNLOADS),
	useListPublicDocsQuery: () => okQuery({ docs: [] }),
	useGetPublicDocQuery: () => okQuery(undefined),
}));

const noDash = (html: string) => expect(html).not.toMatch(/[–—]/);

describe("public pages routing", () => {
	it("lists exactly the public site and keeps every other page behind sign-in", () => {
		expect([...PUBLIC_PAGES].sort()).toEqual(["/downloads", "/library", "/pricing", "/privacy", "/security", "/terms"]);
		expect(isPublicPage("/pricing/")).toBe(true);
		expect(isPublicPage("/settings")).toBe(false);
		expect(isVisitorPage("/")).toBe(true);
		expect(isVisitorPage("/docs")).toBe(true);
		expect(isVisitorPage("/agents")).toBe(false);
	});

	it("frames the public site for visitors and members alike", () => {
		const out = signedOutSession();
		expect(frameFor(out, "/")).toBe("landing");
		expect(frameFor(out, "/docs")).toBe("public");
		expect(frameFor(out, "/library")).toBe("public");
		expect(frameFor(out, "/downloads")).toBe("public");
		expect(frameFor(out, "/agents")).toBe("login");
		expect(frameFor(out, "/library/sweb")).toBe("login");
		expect(frameFor(hostedSession("viewer"), "/")).toBe("app");
		expect(frameFor(hostedSession("viewer"), "/docs")).toBe("app");
		expect(frameFor(hostedSession("viewer"), "/pricing")).toBe("public");
		expect(frameFor(LOCAL_SESSION, "/")).toBe("app");
	});
});

describe("public frame and landing", () => {
	it("offers sign-in to a visitor and the app to a member", () => {
		const visitor = render(<PublicFrame>page</PublicFrame>, signedOutSession());
		expect(visitor).toContain('data-testid="public-sign-in"');
		expect(visitor).not.toContain('data-testid="public-open-app"');
		expect(visitor).toContain('data-testid="public-terms"');
		expect(visitor).toContain('data-testid="public-version"');
		const member = render(<PublicFrame>page</PublicFrame>, hostedSession("member"));
		expect(member).toContain('data-testid="public-open-app"');
		noDash(visitor);
	});

	it("shows the real library counts on the landing page", () => {
		const html = render(<LandingView />, signedOutSession());
		expect(html).toContain("30");
		expect(html).toContain("25");
		noDash(html);
	});
});

describe("library, pricing, legal and security", () => {
	it("lists benchmarks with counts and licences, no task content", () => {
		const html = render(
			<NuqsTestingAdapter>
				<LibraryView />
			</NuqsTestingAdapter>,
			signedOutSession(),
		);
		expect(html).toContain("SWE tasks");
		expect(html).toContain("MIT");
		expect(html).toContain("Retail support");
		noDash(html);
	});

	it("shows plans with their stated limits", () => {
		const html = render(<PricingView />, signedOutSession());
		expect(html).toContain('data-testid="pricing-plan-beta"');
		expect(html).toContain('data-testid="pricing-limits-beta"');
		expect(html).toContain("Free during beta");
		noDash(html);
	});

	it("marks terms and privacy as drafts under legal review", () => {
		for (const html of [render(<TermsView />), render(<PrivacyView />)]) {
			expect(html).toContain("under legal review");
			noDash(html);
		}
		const sec = render(<SecurityView />);
		expect(sec).toContain("security.txt");
		noDash(sec);
	});
});

describe("downloads", () => {
	it("formats sizes and builds checked install commands", () => {
		expect(formatSize(512)).toBe("512 B");
		expect(formatSize(2048)).toBe("2.0 KB");
		expect(formatSize(21 * 1024 * 1024)).toBe("21.0 MB");
		const linux = cliInstallCommand("https://h", "bifrost-bench-linux-amd64");
		expect(linux).toContain("curl -fsSLO https://h/downloads/bifrost-bench-linux-amd64");
		expect(linux).toContain("sha256sum -c -");
		expect(cliInstallCommand("https://h", "bifrost-bench-darwin-arm64")).toContain("shasum -a 256 -c -");
		const step = githubStep("https://h", "bifrost-bench-linux-amd64");
		expect(step).toContain("https://h/downloads/SHA256SUMS");
		expect(step).toContain('>> "$GITHUB_PATH"');
		expect(step).not.toContain("github.com");
	});

	it("lists every file with size and checksum, from this server only", () => {
		const html = render(<DownloadsView />, signedOutSession());
		for (const f of DOWNLOADS.files) expect(html).toContain(`data-testid="download-${f.name}"`);
		expect(html).toContain('data-testid="downloads-built-at"');
		expect(html).toContain("0123456789ab");
		expect(html).toContain(SHA);
		expect(html).toContain("pip install https://heimdall.example.com/downloads/heimdall_bench-0.2.0-py3-none-any.whl");
		expect(html).toContain("npm install https://heimdall.example.com/downloads/heimdall-bench-client-0.2.0.tgz");
		expect(html).toContain("claude mcp add --transport http heimdall https://heimdall.example.com/mcp");
		expect(html).toContain("remote: https://heimdall.example.com/downloads/heimdall.gitlab-ci.yml");
		expect(html).toContain('href="/downloads/SHA256SUMS"');
		expect(html).not.toContain("github.com");
		// The Linux build is listed before the macOS one.
		expect(html.indexOf("bifrost-bench-linux-amd64")).toBeLessThan(html.indexOf("bifrost-bench-darwin-arm64"));
		noDash(html);
	});

	it("says so when the server has no downloads, and when the list fails", () => {
		fx.downloadsEmpty = true;
		const empty = render(<DownloadsView />, signedOutSession());
		expect(empty).toContain('data-testid="downloads-none"');
		expect(empty).not.toContain('data-testid="downloads-cli"');
		fx.downloadsEmpty = false;
		fx.downloadsError = true;
		expect(render(<DownloadsView />, signedOutSession())).toContain('data-testid="downloads-error"');
		fx.downloadsError = false;
	});
});
