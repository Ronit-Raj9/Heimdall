import "@/lib/i18n";

import { setOrgPlanRequest } from "@/lib/store/apis/adminPlanApi";
import { modalAppsRequest } from "@/lib/store/apis/modalAppsApi";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { ModalAppsList } from "./modalAppsPanel";

describe("admin actions", () => {
	it("sets a plan with the body handlers/billing.go SetPlanBody reads", () => {
		// Same body as handlers/billing_test.go PUT /api/bench/admin/billing/org1 {"plan":"free"}.
		const r = setOrgPlanRequest("org1", "free");
		expect(r).toMatchObject({ url: "/admin/billing/org1", method: "PUT" });
		expect(JSON.stringify(r.body)).toBe('{"plan":"free"}');
	});

	it("lists leftover Modal apps from GET /open-models/modal-apps", () => {
		expect(modalAppsRequest).toEqual({ url: "/open-models/modal-apps" });
		const wire = JSON.parse('{"apps":[{"name":"heimdall-qwen","app_id":"ap-1","state":"deployed","tasks":"1"}]}') as { apps: Parameters<typeof ModalAppsList>[0]["apps"] };
		const html = renderToStaticMarkup(<ModalAppsList apps={wire.apps} />);
		expect(html).toContain("heimdall-qwen");
		expect(html).toContain("modal app stop");
		expect(renderToStaticMarkup(<ModalAppsList apps={[]} />)).toContain("No leftover apps.");
	});
});
