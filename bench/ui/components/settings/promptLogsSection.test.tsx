import "@/lib/i18n";

import { retentionChoices } from "@/lib/store/apis/promptLogsApi";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { PromptLogsState } from "./promptLogsSection";

const noop = () => undefined;

describe("PromptLogsState", () => {
	it("shows the keep-text state and controls to admins", () => {
		const html = renderToStaticMarkup(<PromptLogsState keep days={7} choices={[1, 7, 30]} canWrite onKeep={noop} onDays={noop} />);
		expect(html).toContain('data-keep="true"');
		expect(html).toContain("prompt and response text");
		expect(html).toContain('data-testid="prompt-logs-keep-toggle"');
		expect(html).toContain('data-testid="prompt-logs-retention"');
	});

	it("explains what is lost with text off, read only for members", () => {
		const html = renderToStaticMarkup(<PromptLogsState keep={false} days={3} choices={[3]} canWrite={false} onKeep={noop} onDays={noop} />);
		expect(html).toContain('data-keep="false"');
		expect(html).toContain("token counts, cost and timing only");
		expect(html).not.toContain("prompt-logs-keep-toggle");
		expect(html).toContain("3 days");
	});
});

describe("retentionChoices", () => {
	it("caps at the server maximum and keeps the current value", () => {
		expect(retentionChoices(30, 7)).toEqual([1, 3, 7, 14, 30]);
		expect(retentionChoices(10, 5)).toEqual([1, 3, 5, 7]);
		expect(retentionChoices(30, 0)).toEqual([1, 3, 7, 14, 30]);
	});
});
