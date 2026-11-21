import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { MaximRunButtonView, maximAction } from "./maximRunButton";

const base = { connected: true, trace_id: "t1" };

describe("maximAction", () => {
	it("hides when not connected or the lookup failed", () => {
		expect(maximAction(undefined, false, true).kind).toBe("hidden");
		expect(maximAction({ ...base, connected: false, status: "not_pushed" }, false, true).kind).toBe("hidden");
		expect(maximAction({ ...base, status: "pushed", url: "https://x" }, true, true).kind).toBe("hidden");
	});
	it("links a pushed run with a url", () => {
		expect(maximAction({ ...base, status: "pushed", url: "https://x" }, false, true)).toEqual({ kind: "open", url: "https://x" });
		expect(maximAction({ ...base, status: "pushed" }, false, true).kind).toBe("hidden");
	});
	it("offers send only for a finished run", () => {
		expect(maximAction({ ...base, status: "not_pushed" }, false, false).kind).toBe("hidden");
		expect(maximAction({ ...base, status: "not_pushed" }, false, true)).toEqual({ kind: "send" });
		expect(maximAction({ ...base, status: "failed", error: "401" }, false, true)).toEqual({ kind: "send", error: "401" });
	});
	it("renders the link in a new tab", () => {
		const html = renderToStaticMarkup(<MaximRunButtonView action={{ kind: "open", url: "https://m/x" }} onSend={() => {}} />);
		expect(html).toContain('target="_blank"');
		expect(html).toContain("noopener");
		expect(html).toContain("Open in Maxim");
		expect(renderToStaticMarkup(<MaximRunButtonView action={{ kind: "send", error: "bad key" }} onSend={() => {}} />)).toContain("Last send failed: bad key");
	});
});
