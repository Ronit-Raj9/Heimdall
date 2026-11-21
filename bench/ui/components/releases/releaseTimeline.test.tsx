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
		expect(html).toContain("Log not tampered with");
		expect(html).toContain("e_synth_notes");
		expect(html).toContain("claude-code-notes-v2");
		expect(html).toContain("aa68875fcf0e");
		expect(html).toContain("maya (release owner)");
		expect(html).toContain("Notes file v2 after the clean and stale-notes runs.");
		expect(html).toContain("team-default: allow");
		expect(html).toContain("none checked");
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
		expect(html).toContain("Log was changed after it was written");
		expect(html).toMatch(/data-testid="release-link-rel_syn0000002" data-linked="false"/);
		expect(html).toMatch(/data-testid="release-link-rel_syn0000003" data-linked="true"/);
	});

	it("says when nothing was released", () => {
		const html = render([], true);
		expect(html).toContain('data-testid="release-timeline-empty"');
		expect(html).toContain("No releases recorded yet.");
	});
});
// 6-B-03: on a 390 px phone the timeline shrinks to the pane instead of scrolling the whole page.
describe("ReleaseTimeline at narrow widths", () => {
	it("gives every track a shrinkable minimum and lets long ids wrap", () => {
		const html = render(fixture.releases, fixture.chain_ok);
		expect(html).not.toContain("grid-cols-[max-content_1fr]");
		expect(html).toContain("grid-cols-[max-content_minmax(0,1fr)]");
		expect(html).toMatch(/class="min-w-0 rounded-sm border/);
		expect(html).toMatch(/class="min-w-0 font-mono text-xs break-all" data-testid="release-id-/);
	});
	it("wraps a long unbroken approver inside its value cell (QC2-29-07)", () => {
		const long = { ...fixture.releases[0], approver: "W".repeat(200) };
		const html = render([long], true);
		const id = long.release_id;
		const dd = html.match(new RegExp(`<dd class="([^"]*)"><span data-testid="release-approver-${id}"`));
		expect(dd?.[1]).toContain("wrap-anywhere");
		expect(dd?.[1]).toContain("min-w-0");
	});
});
