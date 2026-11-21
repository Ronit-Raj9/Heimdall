import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { TraceDownloadLink, otlpDownloadUrl } from "./runTraceLink";

const render = (props: Partial<React.ComponentProps<typeof TraceDownloadLink>> = {}) =>
	renderToStaticMarkup(<TraceDownloadLink runId="r_abc" arm="A" sealed {...props} />);

describe("otlpDownloadUrl", () => {
	it("builds the trace route with the run id encoded", () => {
		expect(otlpDownloadUrl("r_abc")).toMatch(/\/runs\/r_abc\/otlp$/);
		expect(otlpDownloadUrl("r/../x?y")).toMatch(/\/runs\/r%2F\.\.%2Fx%3Fy\/otlp$/);
	});
});

describe("TraceDownloadLink", () => {
	it("offers a sealed run's trace as a download", () => {
		const html = render();
		expect(html).toContain("Download trace (OTLP)");
		expect(html).toContain('data-testid="run-trace-A-download"');
		expect(html).toContain('download="r_abc.otlp.json"');
		expect(html).toMatch(/href="[^"]*\/runs\/r_abc\/otlp"/);
	});

	it("shows a note instead of a link for an unsealed run", () => {
		const html = render({ sealed: false });
		expect(html).not.toContain("<a");
		expect(html).toContain("Trace download is available once the run is saved");
	});

	it("shows a note instead of a link when the run does not match its seal", () => {
		const html = render({ sealError: "evidence.json changed" });
		expect(html).not.toContain("<a");
		expect(html).toContain("files changed after the run was saved");
	});
});