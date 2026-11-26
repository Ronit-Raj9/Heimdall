import "@/lib/i18n";

import { render } from "@/lib/testing/render";
import { describe, expect, it, vi } from "vitest";
import { agentKnown, FailureTestEstimate, UnknownAgentNote } from "./failureTestsView";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

const old = { runs: 12, est_cost_usd: { known: true, low: 0.1, high: 0.2 }, est_minutes: { known: true, low: 1, high: 2 } } as never;

describe("failure tests (w4m2)", () => {
	it("shows the error, not the old estimate, after a failed re-estimate", () => {
		const html = render(<FailureTestEstimate problem={null} estimate={{ data: old, isError: true }} pending={false} budgetTyped={false} />);
		expect(html).toContain('data-testid="ft-estimate-error"');
		expect(html).not.toContain("12");
	});
	it("knows only agents in the list", () => {
		expect(agentKnown([{ id: "codex" }], "zz-none")).toBe(false);
		expect(agentKnown([{ id: "codex" }], "codex")).toBe(true);
		expect(agentKnown(undefined, "codex")).toBe(false);
	});
	it("says the agent in the link does not exist", () => {
		expect(render(<UnknownAgentNote name="zz-none" />)).toContain("There is no agent named zz-none. Choose an agent.");
	});
});