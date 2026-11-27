import "@/lib/i18n";

import { mutation, render } from "@/lib/testing/render";
import { describe, expect, it, vi } from "vitest";
import { DeleteAgentDialog } from "./deleteAgentDialog";

vi.mock("@/lib/store/apis/agentDeleteApi", () => ({ useDeleteAgentMutation: () => mutation({}) }));

describe("DeleteAgentDialog", () => {
	it("opens with focus on Keep, never on the destructive button", () => {
		const html = render(<DeleteAgentDialog agentId="a1" onDeleted={() => {}} onClose={() => {}} />);
		const keep = html.match(/<button[^>]*data-testid="delete-agent-keep"[^>]*>/)?.[0] ?? "";
		const del = html.match(/<button[^>]*data-testid="delete-agent-confirm"[^>]*>/)?.[0] ?? "";
		expect(keep).toContain("data-initial-focus");
		expect(del).not.toContain("data-initial-focus");
	});
});
