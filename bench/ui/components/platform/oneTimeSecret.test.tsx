import "@/lib/i18n";

import { render } from "@/lib/testing/render";
import { describe, expect, it } from "vitest";
import { CopyBlock, OneTimeSecret } from "./oneTimeSecret";

describe("OneTimeSecret", () => {
	it("shows the secret once, says it cannot be shown again, and offers copy and done", () => {
		const html = render(
			<OneTimeSecret title="New token ci" secret="hm_abcd1234_secret" secretLabel="Token" onDone={() => undefined} testId="t">
				<p>usage</p>
			</OneTimeSecret>,
		);
		expect(html).toContain('data-testid="t-once"');
		expect(html).toContain("Shown once");
		expect(html).toContain("cannot be shown again");
		expect(html).toContain("hm_abcd1234_secret");
		expect(html).toContain('data-testid="t-value-copy"');
		expect(html).toContain('data-testid="t-done"');
		expect(html).toContain("usage");
	});
	it("renders a copy block for commands", () => {
		const html = render(<CopyBlock value="bifrost-bench runner start" label="Start" testId="cmd" />);
		expect(html).toContain("bifrost-bench runner start");
		expect(html).toContain('data-testid="cmd-copy"');
	});
});