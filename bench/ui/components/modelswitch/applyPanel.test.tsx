import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { ApplyPanel, ApplySteps, applyReady } from "./applyPanel";

vi.mock("@/lib/store/apis/modelSwitchApi", () => ({ useApplyModelSwitchMutation: () => [async () => ({}), { isLoading: false }] }));
vi.mock("@/lib/store/apis/secretsApi", () => ({
	useListSecretsQuery: () => ({ data: { secrets: [{ id: "s2", name: "GW_TOKEN", created_at: "", updated_at: "" }, { id: "s1", name: "BIFROST_ADMIN", created_at: "", updated_at: "" }] } }),
}));

describe("apply to my Bifrost", () => {
	it("offers a preview and an apply, both off until a gateway and the global check are set", () => {
		const html = renderToStaticMarkup(<ApplyPanel evaluationId="ev1" ruleCount={2} />);
		expect(html).toContain('data-testid="model-switch-apply-preview"');
		expect(html).toContain('data-testid="model-switch-apply-run"');
		expect(html).toMatch(/<button[^>]*disabled=""[^>]*data-testid="model-switch-apply-preview"|data-testid="model-switch-apply-preview"[^>]*disabled=""/);
	});

	it("picks the credential from the org secrets and scopes to a virtual key by default", () => {
		const html = renderToStaticMarkup(<ApplyPanel evaluationId="ev1" ruleCount={2} />);
		expect(html).toMatch(/<select[^>]*data-testid="model-switch-apply-secret"/);
		expect(html.indexOf("BIFROST_ADMIN")).toBeLessThan(html.indexOf("GW_TOKEN"));
		expect(html).toContain('data-testid="model-switch-apply-scope"');
		expect(html).toContain("Virtual key id");
		expect(html).not.toContain('data-testid="model-switch-apply-global"');
	});

	it("needs a scope id, or global with the explicit check", () => {
		expect(applyReady("", "team", "t1", false)).toBe(false);
		expect(applyReady("https://gw", "team", "", false)).toBe(false);
		expect(applyReady("https://gw", "team", "t1", false)).toBe(true);
		expect(applyReady("https://gw", "global", "", false)).toBe(false);
		expect(applyReady("https://gw", "global", "", true)).toBe(true);
	});

	it("lists the dry run plan in plain words", () => {
		const html = renderToStaticMarkup(
			<ApplySteps
				result={{
					gateway: "https://gw",
					dry_run: true,
					scope: "global",
					steps: [{ rule: "heimdall-ev1-default", action: "create", priority: 100, enabled: false, status: "planned" }],
					stale: [],
					notes: [],
				}}
			/>,
		);
		expect(html).toContain("Would create heimdall-ev1-default at priority 100");
	});
});
