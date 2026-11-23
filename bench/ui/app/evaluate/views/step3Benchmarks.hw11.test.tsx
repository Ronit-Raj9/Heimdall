import "@/lib/i18n";

import { render } from "@/lib/testing/render";
import type { FC } from "react";
import { describe, expect, it, vi } from "vitest";
import * as step3 from "./step3Benchmarks";

vi.mock("@/lib/store/apis/jobRunApi", () => ({ useCreateJobMutation: () => [vi.fn(), { isLoading: false }] }));
vi.mock("@/lib/store/hooks", () => ({ useAppDispatch: () => vi.fn() }));

// Fix wave 3, N187: a setup whose compatibility check failed blocks step 3 with Retry, instead of
// reading as runnable.
describe("failed setup check", () => {
	const mod = step3 as Record<string, unknown>;
	it("lists every setup whose check failed, setup A first", () => {
		const failedSetupChecks = mod.failedSetupChecks as
			| ((p: { label: string; agent: string; failed: boolean }, o: { label: string; agent: string; failed: boolean }[]) => unknown)
			| undefined;
		expect(failedSetupChecks).toBeTypeOf("function");
		expect(
			failedSetupChecks!({ label: "A", agent: "codex", failed: false }, [
				{ label: "B", agent: "bare-model", failed: true },
				{ label: "C", agent: "claude", failed: false },
			]),
		).toEqual([{ label: "B", agent: "bare-model" }]);
		expect(failedSetupChecks!({ label: "A", agent: "", failed: true }, [])).toEqual([]);
	});
	it("says which setup could not be checked and offers Retry", () => {
		const View = mod.SetupCheckFailed as FC<{ failed: { label: string; agent: string }[]; onRetry: () => void }> | undefined;
		expect(View).toBeTypeOf("function");
		const V = View!;
		const html = render(<V failed={[{ label: "B", agent: "bare-model" }]} onRetry={() => undefined} />);
		expect(html).toContain("Could not check Setup B (bare-model). Pick it again or retry.");
		expect(html).toContain('data-testid="step3-setup-check-retry"');
		expect(html).toContain("Retry");
	});
});