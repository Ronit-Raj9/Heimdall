import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { TrialsField } from "./experimentForm";

vi.mock("@tanstack/react-router", async () => ({ ...(await import("@/lib/testing/routerMock")).routerMock, useBlocker: () => undefined }));

describe("TrialsField (p07-3)", () => {
	it("shows its error and marks the box invalid", () => {
		const html = renderToStaticMarkup(
			<TrialsField condition="clean" disabled={false} error="experiments.new.validation.trialsRange" inputProps={{ name: "x" }} />,
		);
		expect(html).toContain("Trials is a whole number from 1 to 50.");
		expect(html).toContain('aria-invalid="true"');
		expect(html).toContain('data-testid="experiment-condition-clean-trials-error"');
	});
	it("is quiet without an error", () => {
		const html = renderToStaticMarkup(<TrialsField condition="clean" disabled={false} inputProps={{ name: "x" }} />);
		expect(html).not.toContain('aria-invalid="true"');
		expect(html).not.toContain("trials-error");
	});
});