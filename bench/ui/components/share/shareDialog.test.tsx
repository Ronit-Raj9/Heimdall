import "@/lib/i18n";

import { render } from "@/lib/testing/render";
import { LOCAL_SESSION, signedOutSession } from "@/lib/testing/platformFixtures";
import { okQuery, mutation } from "@/lib/testing/render";
import { badgeMarkdown, reportExportUrl, reproduceUrl, shareUrl } from "@/lib/store/apis/shareApi";
import { describe, expect, it, vi } from "vitest";
import { ShareDialog, shareQuery } from "./shareDialog";

vi.mock("@/lib/store/apis/shareApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/shareApi")>()),
	useListSharesQuery: () =>
		okQuery({
			shares: [
				{
					id: "shr_1",
					kind: "evaluation",
					target: "ev_1",
					hide_prompts: false,
					created_at: "2026-11-22T00:00:00Z",
					state: "active",
					token: "o.shr_1.s",
					path: "/s/o.shr_1.s",
					label: "Board",
				},
				{
					id: "shr_2",
					kind: "evaluation",
					target: "ev_1",
					hide_prompts: false,
					created_at: "2026-11-21T00:00:00Z",
					state: "revoked",
				},
			],
		}),
	useCreateShareMutation: () => mutation(),
	useRevokeShareMutation: () => mutation(),
}));

describe("share helpers", () => {
	it("builds queries, URLs and badge Markdown", () => {
		expect(shareQuery({ version: 2, model: "", capability: undefined })).toBe("version=2");
		expect(shareUrl({ path: "/s/abc" }, "https://h.example")).toBe("https://h.example/s/abc");
		expect(shareUrl({ url: "https://pub/s/abc", path: "/s/abc" }, "https://h.example")).toBe("https://pub/s/abc");
		expect(badgeMarkdown("o.s.t", "https://h/s/o.s.t", "Score", "https://h")).toBe(
			"[![Score](https://h/api/bench/shared/o.s.t/badge.svg)](https://h/s/o.s.t)",
		);
		expect(reportExportUrl("ev_1", "md")).toBe("/api/bench/evaluations/ev_1/report/export?format=md");
		expect(reproduceUrl("ev_1")).toBe("/api/bench/evaluations/ev_1/reproduce");
	});
});

describe("ShareDialog", () => {
	it("offers expiry, prompt hiding, links and downloads to a member", () => {
		const html = render(<ShareDialog kind="evaluation" target="ev_1" evaluationId="ev_1" onClose={() => undefined} />, LOCAL_SESSION);
		expect(html).toContain('data-testid="share-create"');
		expect(html).toContain('data-testid="share-expiry-0"');
		expect(html).toContain('data-testid="share-hide-prompts"');
		expect(html).toContain('data-testid="share-link-shr_1"');
		expect(html).toContain('data-testid="share-revoke-shr_1"');
		expect(html).not.toContain('data-testid="share-revoke-shr_2"');
		expect(html).toContain('data-testid="share-download-pdf"');
		expect(html).toContain("/api/bench/evaluations/ev_1/reproduce");
		expect(html).not.toMatch(/[–—]/);
	});

	it("lets a visitor without member rights see links but not create them", () => {
		const html = render(<ShareDialog kind="verdict" target="exp_1" onClose={() => undefined} />, signedOutSession());
		expect(html).toContain('data-testid="share-read-only"');
		expect(html).not.toContain('data-testid="share-create"');
		expect(html).not.toContain('data-testid="share-downloads"');
	});
});