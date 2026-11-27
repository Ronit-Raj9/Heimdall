import "@/lib/i18n";

import { render } from "@/lib/testing/render";
import { describe, expect, it, vi } from "vitest";
import { EntityLink } from "./entityLink";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

describe("EntityLink to a deleted benchmark (Q-76)", () => {
	it("renders the task as plain text that says the benchmark was deleted", () => {
		const html = render(<EntityLink kind="task" id="t1" suite="coding-core@1.0.0" suiteGone />);
		expect(html).not.toContain("<a");
		expect(html).toContain('title="Benchmark coding-core was deleted"');
	});
	it("still links when the suite exists", () => {
		expect(render(<EntityLink kind="task" id="t1" suite="coding-core" />)).toContain("<a");
	});
});
