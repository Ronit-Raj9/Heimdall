import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { ApplyPanel, ApplySteps } from "./applyPanel";

vi.mock("@/lib/store/apis/modelSwitchApi", () => ({ useApplyModelSwitchMutation: () => [async () => ({}), { isLoading: false }] }));

describe("apply to my Bifrost", () => {
	it("offers a preview and an apply, both off until a gateway and the global check are set", () => {
		const html = renderToStaticMarkup(<ApplyPanel evaluationId="ev1" ruleCount={2} />);
		expect(html).toContain('data-testid="model-switch-apply-preview"');
		expect(html).toContain('data-testid="model-switch-apply-run"');
		expect(html).toMatch(/<button[^>]*disabled=""[^>]*data-testid="model-switch-apply-preview"|data-testid="model-switch-apply-preview"[^>]*disabled=""/);
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
