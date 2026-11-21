import "@/lib/i18n";

import { render, tagOf } from "@/lib/testing/render";
import { describe, expect, it, vi } from "vitest";
import { SuiteRunButton } from "./suiteRunButton";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

describe("SuiteRunButton", () => {
	it("starts through /evaluate with the benchmark picked, not the quick run dialog", () => {
		const html = render(<SuiteRunButton suiteId="terminal-bench" />);
		expect(tagOf(html, "suite-run-button")).toContain('href="/evaluate?suite=terminal-bench"');
		expect(html).not.toContain("quickrun");
	});
});
