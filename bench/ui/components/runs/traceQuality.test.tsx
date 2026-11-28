import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { TraceQualityDetails } from "./traceQuality";

describe("TraceQualityDetails", () => {
	it("shows missing diagnostics as unknown for legacy runs", () => {
		const html = renderToStaticMarkup(<TraceQualityDetails arm="A" />);
		expect(html).toContain("unknown (unknown)");
		expect(html).toContain("not recorded");
	});
	it("displays parser loss and truncation with inert text", () => {
		const html = renderToStaticMarkup(
			<TraceQualityDetails
				arm="B"
				trace={{
					source: "protocol",
					quality: "partial",
					parser: "protocol",
					parser_version: "1",
					lines: 5,
					emitted: 3,
					malformed: 1,
					oversized: 1,
					truncated: true,
					reason: "<script>alert(1)</script>",
				}}
			/>,
		);
		expect(html).toContain('data-testid="runs-trace-quality-B"');
		expect(html).toContain("partial (protocol)");
		expect(html).toContain("malformed: 1; oversized: 1");
		expect(html).toContain("Captured output was truncated");
		expect(html).not.toContain("<script>");
	});
	it("says what is missing when the agent has no step parser", () => {
		const html = renderToStaticMarkup(<TraceQualityDetails arm="A" trace={{ source: "stdout_tail", quality: "degraded", truncated: true }} />);
		expect(html).toContain('data-testid="runs-trace-missing-A"');
		expect(html).toContain("Steps come from the gateway only: this agent has no step parser.");
	});
	it("shows no missing line for a complete trace", () => {
		const html = renderToStaticMarkup(
			<TraceQualityDetails arm="A" trace={{ source: "native_parser", quality: "complete", parser: "codex", parser_version: "1.1.0" }} />,
		);
		expect(html).not.toContain("runs-trace-missing");
	});
	it("names the parser when it read only part of the output", () => {
		const html = renderToStaticMarkup(<TraceQualityDetails arm="B" trace={{ source: "protocol", quality: "partial", parser: "protocol" }} />);
		expect(html).toContain("Parser protocol read only part of the agent&#x27;s output.");
	});
});
