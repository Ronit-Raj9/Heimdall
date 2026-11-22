import "@/lib/i18n";

import { render } from "@/lib/testing/render";
import i18n from "i18next";
import { describe, expect, it, vi } from "vitest";
import { VersionTable } from "./dataTab";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

const t = i18n.getFixedT("en", "benchData") as never;
const version = { version: 3, at: "2026-11-21T10:00:00Z", source: "upload", added: [], updated: [], removed: [], tasks: 4, sha256: "aa" };

describe("benchmark data versions name who changed them (D4)", () => {
	it("prints the display name with the raw actor as a tooltip", () => {
		const html = render(<VersionTable versions={[{ ...version, actor: "usr_ada", actor_name: "Ada" }]} t={t} />);
		expect(html).toContain('title="usr_ada"');
		expect(html).toContain(">Ada<");
	});

	it("prints the name alone when the server left the email out", () => {
		const html = render(<VersionTable versions={[{ ...version, actor_name: "bo" }]} t={t} />);
		expect(html).toContain(">bo<");
		expect(html).not.toContain("@");
	});
});