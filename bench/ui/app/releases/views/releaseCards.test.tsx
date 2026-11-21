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
});
