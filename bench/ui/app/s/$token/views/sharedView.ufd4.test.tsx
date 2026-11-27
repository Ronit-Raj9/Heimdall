import "@/lib/i18n";

import { render } from "@/lib/testing/render";
import type { SharedItem } from "@/lib/store/apis/shareApi";
import { describe, expect, it } from "vitest";
import { rateOrTooFew, SharedItemView, spentNumber } from "./sharedView";

function item(den: number, extra: Partial<SharedItem["share"]> = {}): SharedItem {
	return {
		share: { kind: "verdict", target: "exp_1", hide_prompts: false, created_at: "2026-11-20T00:00:00Z", badge: true, ...extra },
		data: {
			experiment: {
				spent_usd: { state: "observed", v: 1.5 },
				finished_at: "2026-11-21T10:00:00Z",
				setups: [{ arm: "A", agent: "bot", model: "m-1" }],
			},
			verdict: {
				exp_id: "exp_1",
				analysis_id: "an_1",
				grade_source: "sealed",
				suite: "s",
				naive: { A: { num: 5, den, unknown: 0 }, B: { num: 5, den, unknown: 0 } },
				gates: [],
				config_diff: [],
				verdict: { decision: "inconclusive", action: "no_decision", guards: [] },
			},
		},
	};
}

describe("shared comparison page", () => {
	it("rates only from 10 runs and reads the total spend", () => {
		expect(rateOrTooFew({ num: 3, den: 9, unknown: 0 } as never)).toEqual({ rate: 3 / 9, tooFew: true });
		expect(rateOrTooFew({ num: 5, den: 10, unknown: 0 } as never).tooFew).toBe(false);
		expect(rateOrTooFew(undefined)).toEqual({ rate: null, tooFew: false });
		expect(spentNumber(2)).toBe(2);
		expect(spentNumber({ state: "unknown", v: null })).toBeNull();
	});

	it("shows both setups, sample size, cost, date, the app headline and the creator when sent", () => {
		const html = render(<SharedItemView item={item(20, { created_by_name: "Asha" })} token="s_1" />);
		expect(html).toContain("No clear winner yet");
		expect(html).toContain("rate from 20 runs");
		expect(html).toContain('data-testid="shared-setup-B"');
		expect(html).toContain("bot · m-1");
		expect(html).toContain("Total cost");
		expect(html).toContain("Finished");
		expect(html).toContain("Link made by Asha");
		expect(html).not.toMatch(/[–—]/);
	});

	it("says too few runs under 10 and invents no creator", () => {
		const html = render(<SharedItemView item={item(4)} token="s_1" />);
		expect(html).toContain("Too few runs to rate");
		expect(html).not.toContain('data-testid="shared-made-by"');
	});
});
