import "@/lib/i18n";

import type { ReleaseListResponse } from "@/lib/types/bench";
import fs from "node:fs";
import path from "node:path";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { ChainBadge, ReleaseCards, initials, printOneRelease, releaseLink } from "./releaseCards";
import { liveNow } from "./releasesView";
import i18n from "@/lib/i18n";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

const fixture = JSON.parse(
	fs.readFileSync(path.resolve(__dirname, "../../../public/fixtures/releases.list.json"), "utf8"),
) as ReleaseListResponse;

describe("ReleaseCards", () => {
	it("renders one card per release, newest first, with action pill and evidence link", () => {
		const html = renderToStaticMarkup(<ReleaseCards releases={fixture.releases} chainOk={fixture.chain_ok} />);
		const order = ["rel_syn0000003", "rel_syn0000002", "rel_syn0000001"].map((id) => html.indexOf(`data-testid="release-entry-${id}"`));
		expect(order.every((i) => i >= 0)).toBe(true);
		expect([...order].sort((a, b) => a - b)).toEqual(order);
		expect(html).toContain('data-testid="release-action-rel_syn0000003"');
		expect(html).toContain('data-testid="release-evidence-rel_syn0000003"');
		expect(html).toContain("Approved by");
	});

	it("tags an override with one amber badge and the reason inline (M7-75)", () => {
		const r = { ...fixture.releases[0], action: "hold" as const, override: "ship despite warn" };
		const html = renderToStaticMarkup(<ReleaseCards releases={[r]} chainOk />);
		expect(html).toContain(`data-testid="release-override-tag-${r.release_id}"`);
		expect(html).toContain("Released although the result said hold. Reason: ship despite warn");
		expect(html).not.toContain(`data-testid="release-action-${r.release_id}"`);
		expect(html).not.toContain("text-chart-error-ink mt-3");
	});

	it("says first record, no numbers, and offers a link to the release (M7-76, M7-82, M7-83)", () => {
		const r = { ...fixture.releases[0], prev_hash: "", summary: undefined };
		const html = renderToStaticMarkup(<ReleaseCards releases={[r]} chainOk />);
		expect(html).toContain("First record in the log");
		expect(html).toContain("No numbers were recorded with this release.");
		expect(html).toContain(`data-testid="release-copy-link-${r.release_id}"`);
		expect(releaseLink("https://h.io", "rel_1")).toBe("https://h.io/releases?release=rel_1");
		expect(html).not.toContain("hover:shadow-md");
	});

	it("names Live now from the newest release per agent (M7-77)", () => {
		const t = i18n.getFixedT("en", "uf7b") as never;
		const rs = [
			{ ...fixture.releases[0], agent_id: "codex", version: 1, approver: "ana", created_at: "2026-11-21T10:00:00Z" },
			{ ...fixture.releases[0], agent_id: "codex", version: 2, approver: "raj", created_at: "2026-11-23T10:00:00Z" },
		];
		expect(liveNow(rs, t)).toEqual(["codex v2 (released 23 Nov by raj)"]);
		expect(liveNow([], t)).toEqual([]);
	});

	it("shows chain status and approver initials", () => {
		expect(renderToStaticMarkup(<ChainBadge ok />)).toContain("Log not tampered with");
		expect(renderToStaticMarkup(<ChainBadge ok={false} />)).toContain("Log was changed after it was written");
		expect(initials("ada.lovelace@x.io")).toBe("AL");
		expect(initials("raj")).toBe("RA");
		expect(initials("")).toBe("?");
	});

	it("titles a release of an evaluation as a version replacing the live one, with before and after numbers", () => {
		const r = {
			...fixture.releases[0],
			agent_id: "codex",
			version: 4,
			replaces_version: 3,
			evaluation_id: "ev_20261120_000000_abc",
			children: [
				{
					benchmark: "hd-harbor-tb2",
					exp_id: "e_1",
					analysis_id: "a_1",
					decision: "improved" as const,
					action: "ship" as const,
					manifest_hash: "m",
					seals_digest: "d",
					runs: 8,
				},
			],
			summary: {
				candidate: {
					label: "B",
					agent: "codex",
					version: 4,
					score: 0.6,
					ci_low: 0.4,
					ci_high: 0.8,
					runs: 16,
					cost_per_solved_usd: 0.42,
					false_completion: 0.05,
					fault_survival: 0.5,
				},
				baseline: {
					label: "A",
					agent: "codex",
					version: 3,
					score: 0.4,
					ci_low: 0.2,
					ci_high: 0.6,
					runs: 16,
					cost_per_solved_usd: 0.61,
					false_completion: 0.1,
					fault_survival: 0.3,
				},
				capabilities: [
					{
						capability: "terminal",
						candidate: { score: 0.6, ci_low: 0.4, ci_high: 0.8, runs: 16 },
						baseline: { score: 0.4, ci_low: 0.2, ci_high: 0.6, runs: 16 },
					},
				],
			},
		};
		const html = renderToStaticMarkup(<ReleaseCards releases={[r]} chainOk />);
		const id = r.release_id;
		expect(html).toContain("codex v4 replaces v3");
		expect(html).toContain(`data-testid="release-numbers-${id}-cap-terminal"`);
		expect(html).toContain("60% (40%-80%)");
		expect(html).toContain("$0.420");
		expect(html).toContain("Solved under faults");
		expect(html).toContain("False completion");
		expect(html).not.toContain("Arm B");
	});

	it("titles a first release", () => {
		const r = { ...fixture.releases[0], agent_id: "support-bot", version: 1, replaces_version: 0 };
		expect(renderToStaticMarkup(<ReleaseCards releases={[r]} chainOk />)).toContain("support-bot v1 is the first release");
	});
});

describe("ReleaseCards: a record always reads Released (N171)", () => {
	it("shows Released with an override on an overridden release, never Not released (M7-75)", () => {
		const r = { ...fixture.releases[0], action: "hold" as const, override: "hotfix for a customer", approver: "ana" };
		const html = renderToStaticMarkup(<ReleaseCards releases={[r]} chainOk />);
		expect(html).toContain("Released with an override");
		expect(html).toContain("Reason: hotfix for a customer");
		expect(html).not.toContain("Not released");
		const unclear = renderToStaticMarkup(<ReleaseCards releases={[{ ...r, action: "no_decision" as const }]} chainOk />);
		expect(unclear).toContain("Released without a clear result");
		expect(unclear).toContain("Released although the test found no clear winner.");
	});
	it("shows only Released on a ship record", () => {
		const r = { ...fixture.releases[0], action: "ship" as const, override: undefined };
		const html = renderToStaticMarkup(<ReleaseCards releases={[r]} chainOk />);
		expect(html).toContain(">Released<");
		expect(html).not.toContain("The result said");
		expect(html).not.toContain("Released with an override");
	});
	it("names the advice on an older record released without an override", () => {
		const r = { ...fixture.releases[0], action: "no_decision" as const, override: undefined };
		const html = renderToStaticMarkup(<ReleaseCards releases={[r]} chainOk />);
		expect(html).toContain(">Released<");
		expect(html).toContain("The result said no decision");
		expect(html).not.toContain("Not released");
	});
});
describe("print one release (M7-83)", () => {
	it("marks the page while printing and clears the mark after the print dialog", () => {
		const listeners: Record<string, () => void> = {};
		const root = { dataset: {} as Record<string, string> };
		const print = vi.fn();
		const win = {
			document: { documentElement: root },
			addEventListener: (ev: string, fn: () => void) => (listeners[ev] = fn),
			removeEventListener: (ev: string) => delete listeners[ev],
			setTimeout: (fn: () => void) => fn(),
			print,
		} as unknown as Window;
		const done = vi.fn();
		printOneRelease("rel_1", done, win);
		expect(root.dataset.printRelease).toBe("rel_1");
		expect(print).toHaveBeenCalledOnce();
		listeners.afterprint();
		expect(root.dataset.printRelease).toBeUndefined();
		expect(done).toHaveBeenCalledOnce();
		expect(listeners.afterprint).toBeUndefined();
	});
});
