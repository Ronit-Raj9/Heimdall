import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { MethodHint } from "./methodHint";

describe("MethodHint", () => {
	for (const topic of ["pValue", "mde", "costPerSuccess"] as const) {
		it(topic, () => {
			const html = renderToStaticMarkup(<MethodHint topic={topic} />);
			expect(html).toContain(`data-testid="verdict-method-${topic}"`);
			expect(html).toContain('aria-label="Method: ');
		});
	}
});