import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { HeldOutBoardOff, HeldOutBoardToggle, LeaderboardHeldOutBanner, heldOutBoardOffered, isNotFoundError } from "./heldOutBoard";

describe("held-out board", () => {
	it("says the board holds held-out tasks only", () => {
		const html = renderToStaticMarkup(<LeaderboardHeldOutBanner />);
		expect(html).toContain('data-testid="leaderboard-held-out-banner"');
		expect(html).toContain('role="note"');
		expect(html).toContain("Held-out board");
		expect(html).toContain("never mixed into the public board");
	});

	it("is a toggle button that reports whether the held-out board is shown", () => {
		const off = renderToStaticMarkup(<HeldOutBoardToggle heldOut={false} onChange={() => undefined} />);
		expect(off).toMatch(/<button type="button" aria-pressed="false"/);
		const on = renderToStaticMarkup(<HeldOutBoardToggle heldOut onChange={() => undefined} disabled />);
		expect(on).toMatch(/aria-pressed="true" disabled=""/);
	});

	it("is offered only when some suite has held-out tasks the server shows", () => {
		const shown = { held_out_count: 1, tasks: [{ redacted: false }, {}] } as never;
		const redacted = { held_out_count: 1, tasks: [{ redacted: true }] } as never;
		const none = { held_out_count: 0, tasks: [{}] } as never;
		expect(heldOutBoardOffered([none])).toBe(false);
		expect(heldOutBoardOffered([none, shown])).toBe(true);
		expect(heldOutBoardOffered([shown, redacted])).toBe(false);
		expect(heldOutBoardOffered([])).toBe(false);
	});

	it("reads the light list's redacted_tasks count when the list has no tasks (M4-34)", () => {
		expect(heldOutBoardOffered([{ held_out_count: 2, redacted_tasks: 0 }])).toBe(true);
		expect(heldOutBoardOffered([{ held_out_count: 2, redacted_tasks: 2 }])).toBe(false);
		expect(heldOutBoardOffered([{ held_out_count: 2, redacted_tasks: 0, tasks: [{ redacted: true }] as never }])).toBe(true);
		expect(heldOutBoardOffered([{ held_out_count: 2, tasks: [] }])).toBe(true);
	});

	it("says why a server without held_out.visible answers 404", () => {
		expect(isNotFoundError({ status: 404, data: {} })).toBe(true);
		expect(isNotFoundError({ status: 500 })).toBe(false);
		expect(isNotFoundError(undefined)).toBe(false);
		const html = renderToStaticMarkup(<HeldOutBoardOff onBack={() => undefined} />);
		expect(html).toContain('data-testid="leaderboard-held-out-off"');
		expect(html).toContain("held_out.visible");
	});
});