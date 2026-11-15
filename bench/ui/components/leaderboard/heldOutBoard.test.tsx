import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { HeldOutBoardToggle, LeaderboardHeldOutBanner } from "./heldOutBoard";

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
});