import "@/lib/i18n";

import { LOCAL_SESSION } from "@/lib/testing/platformFixtures";
import { mutation, okQuery, render } from "@/lib/testing/render";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { canonicalQuery, PdfHint, ShareDialog, shareQuery } from "./shareDialog";

vi.mock("@/lib/store/apis/shareApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/shareApi")>()),
	useListSharesQuery: () =>
		okQuery({
			shares: [
				{
					id: "shr_sorted",
					kind: "leaderboard",
					target: "board",
					query: "condition=heimdall-model-down&suite=hd-harbor-bfcl%401.0.0",
					hide_prompts: false,
					created_at: "2026-11-22T00:00:00Z",
					state: "active",
					token: "o.shr_sorted.s",
					path: "/s/o.shr_sorted.s",
				},
			],
		}),
	useCreateShareMutation: () => mutation(),
	useRevokeShareMutation: () => mutation(),
}));

describe("share dialog wave 4 fixes", () => {
	it("lists a link whose stored query is key sorted", () => {
		const html = render(
			<ShareDialog
				kind="leaderboard"
				target="board"
				query="suite=hd-harbor-bfcl%401.0.0&condition=heimdall-model-down"
				onClose={() => undefined}
			/>,
			LOCAL_SESSION,
		);
		expect(html).toContain('data-testid="share-link-shr_sorted"');
		expect(html).toContain('data-testid="share-revoke-shr_sorted"');
	});

	it("compares queries in key order", () => {
		expect(canonicalQuery("b=2&a=1")).toBe(canonicalQuery("a=1&b=2"));
		expect(canonicalQuery(undefined)).toBe("");
		expect(shareQuery({ suite: "s", condition: "c" })).toBe("condition=c&suite=s");
	});

	it("says what to do when the PDF could not be prepared", () => {
		const html = renderToStaticMarkup(<PdfHint failed />);
		expect(html).toContain('role="alert"');
		expect(html).toContain("Could not prepare the PDF. Download the HTML file and print it from your browser.");
		expect(renderToStaticMarkup(<PdfHint failed={false} />)).not.toContain('role="alert"');
	});

	it("keeps a focusable panel for the dialog focus hook", () => {
		const html = render(<ShareDialog kind="leaderboard" target="board" onClose={() => undefined} />, LOCAL_SESSION);
		expect(html).toMatch(/data-testid="share-dialog"/);
		expect(html).toContain('tabindex="-1"');
	});
});