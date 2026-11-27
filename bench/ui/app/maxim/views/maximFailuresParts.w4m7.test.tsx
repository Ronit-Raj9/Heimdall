import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { ImportsEmpty } from "./maximFailuresParts";

describe("ImportsEmpty (wave 4, p13-4)", () => {
	it("points to the pull form only when Maxim is connected", () => {
		expect(renderToStaticMarkup(<ImportsEmpty connected />)).toContain("No imports yet.");
		const off = renderToStaticMarkup(<ImportsEmpty connected={false} />);
		expect(off).toContain("No import yet. Connect Maxim in Settings first.");
		expect(off).not.toContain("above");
	});
});