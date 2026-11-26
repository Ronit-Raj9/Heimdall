import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import VersionsTab from "./versionsTab";

vi.mock("@/lib/store/apis/evaluationsApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/evaluationsApi")>()),
	useGetAgentVersionsQuery: () => ({
		data: { agent: "codex", versions: [], no_file: true },
		isLoading: false,
		isError: false,
		isSuccess: true,
	}),
	useGetAgentVersionQuery: () => ({ data: undefined, isLoading: false, isError: false }),
	useCreateAgentVersionMutation: () => [vi.fn(), { isLoading: false, isError: false }],
}));

describe("versions tab: agent without a settings file (wave 4, p01-2)", () => {
	it("shows the no file note and no save form", () => {
		const html = renderToStaticMarkup(<VersionsTab agentId="codex" />);
		expect(html).toContain('data-testid="versions-no-file"');
		expect(html).toContain("This agent has no settings file");
		expect(html).not.toContain('data-testid="agent-versions-save"');
	});
});