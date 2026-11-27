import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { Button } from "./button";

describe("Button busyGuard (Q-221)", () => {
	it("renders a normal button and does not leak the prop to the DOM", () => {
		const html = renderToStaticMarkup(
			<Button busyGuard onClick={() => Promise.resolve()}>
				Save
			</Button>,
		);
		expect(html).toContain("<button");
		expect(html.toLowerCase()).not.toContain("busyguard");
		expect(html).not.toContain('disabled=""');
	});
});
