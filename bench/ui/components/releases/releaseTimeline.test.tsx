import "@/lib/i18n";

import type { ReleaseListResponse, ReleaseRecord } from "@/lib/types/bench";
import fs from "node:fs";
import path from "node:path";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { ReleaseTimeline } from "./releaseTimeline";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

const fixture = JSON.parse(
	fs.readFileSync(path.resolve(__dirname, "../../public/fixtures/releases.list.json"), "utf8"),
) as ReleaseListResponse;

function render(releases: ReleaseRecord[], chainOk: boolean): string {
	return renderToStaticMarkup(<ReleaseTimeline releases={releases} chainOk={chainOk} />);
}

describe("ReleaseTimeline", () => {
	it("renders the synthetic log newest first with every field", () => {
		const html = render(fixture.releases, fixture.chain_ok);
		const order = ["rel_syn0000003", "rel_syn0000002", "rel_syn0000001"].map((id) => html.indexOf(`data-testid="release-entry-${id}"`));
		expect(order.every((i) => i >= 0)).toBe(true);
		expect([...order].sort((a, b) => a - b)).toEqual(order);
		expect(html).toContain('data-chain-ok="true"');
		expect(html).toContain("Chain verifies");
		expect(html).toContain("e_synth_notes");
		expect(html).toContain("claude-code-notes-v2");
		expect(html).toContain("aa68875fcf0e");
		expect(html).toContain("maya (release owner)");
		expect(html).toContain("Notes file v2 after the clean and stale-notes runs.");
		expect(html).toContain("team-default: allow");
		expect(html).toContain("none evaluated");
		expect(html).toContain("2026-11-10 09:12 UTC");
		expect(html).toContain('href="/api/bench/releases/rel_syn0000001/evidence"');
		expect(html).toContain('download="rel_syn0000001-evidence.zip"');
		expect(html.match(/data-linked="true"/g)).toHaveLength(3);
	});

	it("shows an override in red and only where one was recorded", () => {
		const html = render(fixture.releases, true);
		expect(html).toMatch(/class="text-chart-error-ink[^"]*" data-testid="release-override-rel_syn0000002"/);
		expect(html).not.toContain('data-testid="release-override-rel_syn0000001"');
		expect(html).not.toContain('data-testid="release-override-rel_syn0000003"');
	});

	it("flags a broken chain and a broken link", () => {
		const tampered = structuredClone(fixture.releases);
		tampered[1].prev_hash = "0".repeat(64);
		const html = render(tampered, false);
		expect(html).toContain('data-chain-ok="false"');
		expect(html).toContain("Chain does not verify");
		expect(html).toMatch(/data-testid="release-link-rel_syn0000002" data-linked="false"/);
		expect(html).toMatch(/data-testid="release-link-rel_syn0000003" data-linked="true"/);
	});

	it("says when nothing was released", () => {
		const html = render([], true);
		expect(html).toContain('data-testid="release-timeline-empty"');
		expect(html).toContain("No releases recorded yet.");
	});
});