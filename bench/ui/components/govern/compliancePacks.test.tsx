import "@/lib/i18n";

import { errorQuery, okQuery, render } from "@/lib/testing/render";
import type { CompliancePack } from "@/lib/types/govern";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { CompliancePacks, groupPacks } from "./compliancePacks";

const api = vi.hoisted(() => ({ packs: {} as unknown }));
vi.mock("@/lib/store/apis/governApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/governApi")>()),
	useListCompliancePacksQuery: () => api.packs,
}));

const pack = (release_id: string, format: "md" | "json"): CompliancePack => ({
	name: `compliance-${release_id}.${format}`,
	release_id,
	format,
	size: 10,
	created_at: "2026-11-21T00:00:00Z",
});

beforeEach(() => {
	api.packs = okQuery({ packs: [] });
});

describe("CompliancePacks", () => {
	it("lists each release's pack files with Download links", () => {
		api.packs = okQuery({ packs: [pack("rel_2", "json"), pack("rel_2", "md"), pack("rel_1", "md")] });
		const html = render(<CompliancePacks />);
		expect(html).toContain('data-testid="compliance-packs-list"');
		expect(html).toContain('href="/api/bench/compliance-packs/compliance-rel_2.md"');
		expect(html).toContain('download="compliance-rel_2.json"');
		expect(html).toContain("Report .md");
		expect(html.indexOf("rel_2")).toBeLessThan(html.indexOf("rel_1"));
	});

	it("points to the Write button when there is no pack yet", () => {
		expect(render(<CompliancePacks />)).toContain("No compliance report yet.");
	});

	it("shows the server's error with a retry", () => {
		api.packs = errorQuery(500, "disk gone");
		expect(render(<CompliancePacks />)).toContain("disk gone");
	});

	it("groups md and json per release", () => {
		const g = groupPacks([pack("rel_1", "md"), pack("rel_1", "json")]);
		expect(g).toHaveLength(1);
		expect(g[0].files.map((f) => f.format)).toEqual(["json", "md"]);
	});
});
