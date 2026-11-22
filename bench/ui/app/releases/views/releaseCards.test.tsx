import "@/lib/i18n";

import type { ReleaseListResponse } from "@/lib/types/bench";
import fs from "node:fs";
import path from "node:path";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { ChainBadge, ReleaseCards, initials } from "./releaseCards";

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

	it("tags an override in red", () => {
		const r = { ...fixture.releases[0], override: "ship despite warn" };
		const html = renderToStaticMarkup(<ReleaseCards releases={[r]} chainOk />);
		expect(html).toContain(`data-testid="release-override-tag-${r.release_id}"`);
		expect(html).toContain("ship despite warn");
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
		expect(html).toContain("60% (40-80%)");
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
	it("shows Released and Released despite the rules on an overridden release, never Not released", () => {
		const r = { ...fixture.releases[0], action: "hold" as const, override: "hotfix for a customer", approver: "ana" };
		const html = renderToStaticMarkup(<ReleaseCards releases={[r]} chainOk />);
		expect(html).toContain(">Released<");
		expect(html).toContain("Released despite the rules");
		expect(html).toContain("The result said hold; ana released it anyway: hotfix for a customer");
		expect(html).not.toContain("Not released");
	});
	it("shows only Released on a ship record", () => {
		const r = { ...fixture.releases[0], action: "ship" as const, override: undefined };
		const html = renderToStaticMarkup(<ReleaseCards releases={[r]} chainOk />);
		expect(html).toContain(">Released<");
		expect(html).not.toContain("The result said");
		expect(html).not.toContain("Released despite the rules");
	});
	it("names the advice on an older record released without an override", () => {
		const r = { ...fixture.releases[0], action: "no_decision" as const, override: undefined };
		const html = renderToStaticMarkup(<ReleaseCards releases={[r]} chainOk />);
		expect(html).toContain(">Released<");
		expect(html).toContain("The result said no decision");
		expect(html).not.toContain("Not released");
	});
});