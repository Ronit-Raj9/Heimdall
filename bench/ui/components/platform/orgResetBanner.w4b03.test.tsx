import "@/lib/i18n";

import { configureStore } from "@reduxjs/toolkit";
import { benchApi } from "@/lib/store/apis/benchApi";
import { platformApi } from "@/lib/store/apis/platformApi";
import { setAuthState } from "@/lib/store/apis/session";
import { hostedSession } from "@/lib/testing/platformFixtures";
import { render } from "@/lib/testing/render";
import { afterEach, describe, expect, it, vi } from "vitest";
import { OrgResetBanner } from "./orgResetBanner";

afterEach(() => {
	setAuthState({ mode: "local" });
	vi.unstubAllGlobals();
});

describe("organization reset banner", () => {
	it("shows the reset note after /auth/me says org_reset", async () => {
		const NodeRequest = globalThis.Request;
		vi.stubGlobal(
			"Request",
			class extends NodeRequest {
				constructor(input: RequestInfo | URL, init?: RequestInit) {
					super(typeof input === "string" ? new URL(input, "http://bench.test") : input, init);
				}
			},
		);
		vi.stubGlobal(
			"fetch",
			vi.fn(async () => {
				const body = {
					mode: "live",
					auth_mode: "hosted",
					kind: "user",
					csrf: "c",
					current_org: "org_acme",
					org_reset: true,
					orgs: [{ id: "org_acme", slug: "acme", name: "Acme Agents", role: "owner" }],
				};
				return new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });
			}),
		);
		expect(render(<OrgResetBanner />, hostedSession())).not.toContain("org-reset-banner");
		const store = configureStore({
			reducer: { [benchApi.reducerPath]: benchApi.reducer },
			middleware: (getDefaultMiddleware) => getDefaultMiddleware().concat(benchApi.middleware),
		});
		await store.dispatch(platformApi.endpoints.getMe.initiate()).unwrap();
		const html = render(<OrgResetBanner />, hostedSession());
		expect(html).toContain('data-testid="org-reset-banner"');
		expect(html).toContain('role="status"');
		expect(html).toContain("You can no longer open the organization this tab showed. It now shows Acme Agents.");
		expect(html).toContain("Close");
	});
});