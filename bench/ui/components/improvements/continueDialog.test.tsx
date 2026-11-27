import "@/lib/i18n";

import { render } from "@/lib/testing/render";
import { describe, expect, it } from "vitest";
import { ContinueDialogBody } from "./continueDialog";

const offer = {
	extra_trials: 2,
	new_tasks: 12,
	runs: 96,
	usd: [3.1, 6.4] as [number, number],
	can_confirm: 9,
	text: "To confirm a 5 point gain: about 30 more held-out tasks.",
};

describe("ContinueDialogBody", () => {
	it("shows the offer text and the confirmation with the cost; nothing is sent before Continue", () => {
		const html = render(<ContinueDialogBody offer={offer} onConfirm={() => undefined} onCancel={() => undefined} />);
		expect(html).toContain("To confirm a 5 point gain: about 30 more held-out tasks.");
		expect(html).toContain(
			"Continue? Spends up to $6.40 more on 96 runs. Retested with reserved error budget.",
		);
		expect(html).toContain('data-testid="continue-confirm"');
		expect(html).toContain('data-testid="continue-cancel"');
	});

	it("disables Continue while the offer loads", () => {
		const html = render(<ContinueDialogBody loading onConfirm={() => undefined} onCancel={() => undefined} />);
		expect(html).toContain("Estimating cost...");
		expect(html).toMatch(
			/<button[^>]*disabled=""[^>]*data-testid="continue-confirm"|<button[^>]*data-testid="continue-confirm"[^>]*disabled=""/,
		);
	});
});