import "@/lib/i18n";

import { render } from "@/lib/testing/render";
import type { SuiteSummary } from "@/lib/types/bench";
import { describe, expect, it } from "vitest";
import { SuiteStatusPill, suitePillState, suiteReadiness } from "./suiteIdentity";

const base = { error: undefined, task_count: 3, admitted_count: 0, lock_state: "unlocked" } as const;

describe("suitePillState", () => {
	it("says frozen but not checked when the lock is current and no task is admitted (N116)", () => {
		expect(suitePillState({ ...base, lock_state: "locked" })).toBe("frozenNotChecked");
		expect(suitePillState(base)).toBe("notReady");
		expect(suitePillState({ ...base, lock_state: "stale" })).toBe("notReady");
		expect(suitePillState({ ...base, admitted_count: 3, lock_state: "locked" })).toBe("ready");
		// The shared readiness (home page, packs) keeps its states.
		expect(suiteReadiness({ ...base, lock_state: "locked" })).toBe("notReady");
	});
	it("labels the frozen state in the header pill", () => {
		const html = render(<SuiteStatusPill suite={{ ...base, id: "zz", lock_state: "locked" } as unknown as SuiteSummary} testId="pill" />);
		expect(html).toContain('data-status="frozenNotChecked"');
		expect(html).toContain("Next: run the readiness check (free)");
		expect(html).toContain('data-tone="neutral"');
	});
	it("uses the shared pill tones: green only when ready", () => {
		const ready = render(<SuiteStatusPill suite={{ ...base, id: "zz", admitted_count: 3, lock_state: "locked" } as unknown as SuiteSummary} testId="pill" />);
		expect(ready).toContain('data-tone="ok"');
		expect(ready).toContain('data-slot="status-pill"');
	});
});