import "@/lib/i18n";

import { hostedSession } from "@/lib/testing/platformFixtures";
import { render } from "@/lib/testing/render";
import { describe, expect, it, vi } from "vitest";

// N288, X4: the agents list card Delete. The mutation state is set per test.
const mutation = vi.hoisted(() => ({ state: { isLoading: false, error: undefined as unknown } }));
vi.mock("@/lib/store/apis/agentDeleteApi", () => ({
	useDeleteAgentMutation: () => [() => ({ unwrap: () => Promise.resolve() }), mutation.state],
}));
vi.mock("@/lib/store/apis/agentBuilderApi", () => ({
	agentBuilderApi: { endpoints: { getAgentConfig: { useLazyQuery: () => [() => ({ unwrap: () => Promise.resolve({}) })] } } },
}));

const { AgentRowDelete, agentRowDeletable, deleteAgentRow } = await import("./agentRowDelete");

const own = { id: "my-agent" };

describe("agent card Delete (X4)", () => {
	// One rule (lead's rule 15): the role the server's DELETE /agents/{id} allows, member, as on the agent's own page.
	it("is shown from member up, hidden for viewers and on a built-in baseline agent", () => {
		expect(agentRowDeletable(hostedSession("admin"), own)).toBe(true);
		expect(agentRowDeletable(hostedSession("member"), own)).toBe(true);
		expect(agentRowDeletable(hostedSession("viewer"), own)).toBe(false);
		expect(agentRowDeletable(hostedSession("owner"), { id: "codex" })).toBe(false);
		expect(agentRowDeletable(hostedSession("owner"), { id: "" })).toBe(false);
		mutation.state = { isLoading: false, error: undefined };
		expect(render(<AgentRowDelete agent={own} name="my-agent" testId="agents-row-my-agent" />, hostedSession("viewer"))).toBe("");
		expect(
			render(<AgentRowDelete agent={{ id: "claude-code" }} name="claude-code" testId="agents-row-claude-code" />, hostedSession("owner")),
		).toBe("");
		expect(render(<AgentRowDelete agent={own} name="my-agent" testId="agents-row-my-agent" />, hostedSession("admin"))).toContain(
			'data-testid="agents-row-my-agent-delete"',
		);
	});

	it("asks the confirm question for an agent with its own file, and never for a library agent", async () => {
		const asked: string[] = [];
		const removed: string[] = [];
		const remove = async (id: string) => void removed.push(id);
		const yes = async (message: string) => (asked.push(message), true);
		const question = "Delete the agent my-agent? Past results stay.";
		const labels = { title: "Delete agent my-agent?", confirm: "Delete", cancel: "Cancel" };
		expect(await deleteAgentRow(async () => false, yes, remove, "lib-agent", question, labels)).toBe("builtin");
		expect(asked).toEqual([]);
		expect(removed).toEqual([]);
		expect(
			await deleteAgentRow(
				async () => true,
				async () => false,
				remove,
				"my-agent",
				question,
				labels,
			),
		).toBe("cancelled");
		expect(removed).toEqual([]);
		expect(await deleteAgentRow(async () => true, yes, remove, "my-agent", question, labels)).toBe("asked");
		expect(asked).toEqual([question]);
		expect(removed).toEqual(["my-agent"]);
	});

	it("shows the server's refusal sentence", () => {
		const sentence = "This agent has an evaluation still running. Wait for it to finish or stop it first.";
		mutation.state = { isLoading: false, error: { status: 409, data: { error: { message: sentence } } } };
		const html = render(<AgentRowDelete agent={own} name="my-agent" testId="agents-row-my-agent" />, hostedSession("admin"));
		expect(html).toContain('data-testid="agents-row-my-agent-delete-error"');
		expect(html).toContain(sentence);
		mutation.state = { isLoading: false, error: undefined };
	});
});