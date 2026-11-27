import "@/lib/i18n";

import { render } from "@/lib/testing/render";
import { describe, expect, it, vi } from "vitest";
import { QuickRunDialog, closeOnEscape, quickRunProblems } from "./quickRunDialog";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("@/components/models/modelPicker", () => ({ ModelPicker: ({ testId }: { testId: string }) => <div data-testid={testId} /> }));
vi.mock("@/lib/store/apis/benchApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/benchApi")>()),
	useGetAgentsQuery: () => ({ data: { agents: [] }, isSuccess: true }),
}));
vi.mock("@/lib/store/apis/suitesLightApi", () => ({ useGetSuitesLightQuery: () => ({ data: { suites: [] }, isSuccess: true }) }));
vi.mock("@/lib/store/apis/agentopsApi", () => ({ useScanAgentSkillMutation: () => [() => undefined, { isLoading: false }] }));
vi.mock("@/lib/store/apis/quickrunApi", () => ({ useQuickRunMutation: () => [() => undefined, { isLoading: false }] }));

describe("quick test dialog (fix wave 3, N229)", () => {
	it("refuses 0 and 99 tasks, 0 and 6 tries and budgets outside $0.50 to $100", () => {
		expect(quickRunProblems({ tasks: "5", trials: "1", budget: "5" })).toEqual({});
		expect(quickRunProblems({ tasks: "0", trials: "1", budget: "5" })).toEqual({ tasks: "limits.tasks" });
		expect(quickRunProblems({ tasks: "99", trials: "1", budget: "5" })).toEqual({ tasks: "limits.tasks" });
		expect(quickRunProblems({ tasks: "", trials: "6", budget: "0.1" })).toEqual({
			tasks: "limits.tasks",
			trials: "limits.trials",
			budget: "limits.budget",
		});
		expect(quickRunProblems({ tasks: "2.5", trials: "0", budget: "101" })).toEqual({
			tasks: "limits.tasks",
			trials: "limits.trials",
			budget: "limits.budget",
		});
	});

	it("shows a message and disables Start for 0 tasks", () => {
		const html = render(
			<QuickRunDialog suite="s" agent="a" model={{ provider: "openai", model: "m" }} tasks={0} onClose={() => undefined} />,
		);
		expect(html).toContain("Pick 1 to 50 tasks.");
		const start = html.match(/<button[^>]*data-testid="quickrun-submit"[^>]*>|<button(?:(?!<button)[^>])*quickrun-submit[^>]*>/)?.[0] ?? "";
		expect(start).toContain("disabled");
		expect(html).not.toContain('data-testid="quickrun-estimate"');
	});

	it("shows the estimate above Start and opens with compare unticked", () => {
		const html = render(
			<QuickRunDialog suite="s" agent="a" model={{ provider: "openai", model: "m" }} tasks={5} onClose={() => undefined} />,
		);
		expect(html).toContain("About 10 runs, up to $5.");
		expect(html.indexOf('data-testid="quickrun-estimate"')).toBeLessThan(html.indexOf('data-testid="quickrun-submit"'));
		const toggle = html.match(/<input[^>]*data-testid="quickrun-compare-toggle"[^>]*>/)?.[0] ?? "";
		expect(toggle).not.toContain("checked");
		expect(html).not.toContain("Pick 1 to 50 tasks.");
	});

	it("closes on Escape only", () => {
		const onClose = vi.fn();
		const key = closeOnEscape(onClose);
		key({ key: "Enter" });
		expect(onClose).not.toHaveBeenCalled();
		key({ key: "Escape" });
		expect(onClose).toHaveBeenCalledOnce();
	});
});
describe("QuickRunDialog paid start (Q-31, Q-32)", () => {
	it("starts through the paid button with the cap and sits in the shared dialog", () => {
		const html = render(<QuickRunDialog suite="s" agent="a" model={{ provider: "openai", model: "m" }} tasks={5} onClose={() => undefined} />);
		const start = html.match(/<button[^>]*data-testid="quickrun-submit"[^>]*>/)?.[0] ?? "";
		expect(start).toContain('data-paid="true"');
		expect(start).toContain('type="button"');
		expect(html).toContain("up to $5.00");
		expect(html).toContain('data-heimdall-dialog="open"');
	});
});
