import "@/lib/i18n";

import type { VerifyRow } from "@/lib/types/connect";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { VerifyRows } from "./verifyRows";

describe("VerifyRows", () => {
	it("shows each row with its status and the failure message", () => {
		const rows: VerifyRow[] = [
			{ id: "reach", title: "Reach", status: "pass", detail: "connected", ms: 12 },
			{
				id: "auth",
				title: "Sign-in",
				status: "fail",
				detail: "HTTP 403",
				failure: {
					code: "auth_wrong",
					title: "Token rejected",
					message: "Your endpoint rejected the token in BOT_KEY.",
					fix: { action: "edit_secret", label: "Replace the secret" },
				},
			},
			{ id: "answer", title: "Answer found", status: "skip", detail: "skipped" },
		];
		const html = renderToStaticMarkup(<VerifyRows rows={rows} />);
		expect(html).toContain('data-testid="verify-row-reach"');
		expect(html).toContain('data-status="fail"');
		expect(html).toContain("Your endpoint rejected the token in BOT_KEY.");
		expect(html).toContain('data-code="auth_wrong"');
		expect(html.includes(String.fromCharCode(0x2014))).toBe(false);
	});
});