import "@/lib/i18n";

import { render } from "@/lib/testing/render";
import { describe, expect, it, vi } from "vitest";
import { InlineError, LoadError, listState, slowStage } from "./screenStates";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

const notFound = { status: 404, data: { error: { message: "improvement not found: zz-nope" } } };
const broken = { status: 500, data: { error: { message: "disk full" } } };
const back = { text: "No improvement run named zz-nope.", backTo: "/improvements", backLabel: "Back to improvements" };

describe("LoadError", () => {
	it("says a 404 does not exist and links back instead of offering Retry", () => {
		const html = render(<LoadError title="Could not load" error={notFound} onRetry={() => undefined} testId="x-error" notFound={back} />);
		expect(html).toContain("No improvement run named zz-nope.");
		expect(html).toContain('href="/improvements"');
		expect(html).toContain("Back to improvements");
		expect(html).not.toContain("x-error-retry");
		expect(html).not.toContain("improvement not found: zz-nope");
	});

	it("keeps Retry and the server message for other errors, even with notFound given", () => {
		const html = render(<LoadError title="Could not load" error={broken} onRetry={() => undefined} testId="x-error" notFound={back} />);
		expect(html).toContain("x-error-retry");
		expect(html).toContain("disk full");
		expect(html).not.toContain("Back to improvements");
	});

	it("renders a 404 as the error pattern without Retry when notFound is not given (Spec 5: Retry only when it can help)", () => {
		const html = render(<LoadError title="Could not load" error={notFound} onRetry={() => undefined} testId="x-error" />);
		expect(html).not.toContain("x-error-retry");
		expect(html).toContain('data-error-kind="notFound"');
		expect(html).toContain("improvement not found: zz-nope");
	});

	it("says why in plain words and keeps the server text in Details", () => {
		const html = render(<LoadError title="Could not load" error={broken} testId="x-error" />);
		expect(html).toContain("If it keeps failing, copy the details for support.");
		expect(html).toContain("Details from the server");
		expect(html).not.toContain("500");
	});
});
describe("InlineError", () => {
	it("renders nothing without an error", () => {
		expect(render(<InlineError error={null} testId="a-error" />)).toBe("");
	});
	it("shows the network sentence and Retry", () => {
		const html = render(<InlineError error={{ status: "FETCH_ERROR", error: "x" }} onRetry={() => undefined} testId="a-error" />);
		expect(html).toContain("Could not reach Heimdall.");
		expect(html).toContain("a-error-retry");
	});
});

describe("listState and slowStage", () => {
	it("never reports empty while loading or failed", () => {
		expect(listState({ isLoading: true, data: [] })).toBe("loading");
		expect(listState({ isError: true, data: [] })).toBe("error");
		expect(listState({ data: [] })).toBe("empty");
		expect(listState({ data: [1] })).toBe("ready");
	});
	it("names a slow load at 3 s and gives up at 15 s", () => {
		expect(slowStage(1000)).toBe("none");
		expect(slowStage(3000)).toBe("slow");
		expect(slowStage(15000)).toBe("tooSlow");
	});
});
