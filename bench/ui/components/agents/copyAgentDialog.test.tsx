import "@/lib/i18n";

import { errorQuery, mutation, okQuery, render, tagOf } from "@/lib/testing/render";
import { describe, expect, it, vi } from "vitest";
import { CopyAgentDialog, copyNameProblem, defaultCopyName } from "./copyAgentDialog";

const state = vi.hoisted(() => ({ copyError: undefined as unknown }));

vi.mock("@/lib/store/apis/agentDeleteApi", () => ({ useCopyAgentMutation: () => mutation({ error: state.copyError }) }));
vi.mock("@/lib/store/apis/benchApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/benchApi")>()),
	useGetAgentsQuery: () => okQuery({ agents: [{ id: "zz-test-loop" }, { id: "zz-test-loop-copy" }, { id: "codex" }] }),
}));

const noop = () => undefined;

describe("CopyAgentDialog", () => {
	it("duplicates under the first free -copy name", () => {
		const html = render(<CopyAgentDialog agentId="zz-test-loop" mode="duplicate" onClose={noop} onCopied={noop} />);
		expect(html).toContain('data-testid="copy-agent-dialog"');
		expect(html).toContain("Duplicate agent zz-test-loop");
		expect(html).toContain("Starts at version 1.");
		expect(tagOf(html, "copy-agent-name")).toContain('value="zz-test-loop-copy-2"');
		expect(html).toContain(">Duplicate</button>");
		expect(html).toContain("Cancel");
	});

	it("explains that a rename keeps past runs under the old name", () => {
		const html = render(<CopyAgentDialog agentId="zz-test-loop" mode="rename" onClose={noop} onCopied={noop} />);
		expect(html).toContain("Rename agent zz-test-loop");
		expect(html).toContain("and removes zz-test-loop. Past runs and versions keep zz-test-loop.");
		expect(html).toContain(">Rename</button>");
		// The box starts on the old name, which is not a new name: Rename waits.
		expect(tagOf(html, "copy-agent-confirm")).toContain("disabled");
		expect(html).toContain("Pick a different name.");
	});

	it("shows the server's refusal", () => {
		state.copyError = errorQuery(409, "An agent named codex already exists. Pick another name.").error;
		const html = render(<CopyAgentDialog agentId="zz-test-loop" mode="duplicate" onClose={noop} onCopied={noop} />);
		expect(html).toContain('data-testid="copy-agent-error"');
		expect(html).toContain("An agent named codex already exists. Pick another name.");
		state.copyError = undefined;
	});

	it("checks the new name like the new agent page", () => {
		const taken = new Set(["zz-test-loop", "codex"]);
		expect(copyNameProblem("zz-test-loop", "zz-test-loop", taken)).toBe("Pick a different name.");
		expect(copyNameProblem("codex", "zz-test-loop", taken)).toBe("codex already exists. Pick another name.");
		expect(copyNameProblem("a".repeat(120), "zz-test-loop", taken)).toBe("Use at most 63 characters (120 now).");
		expect(copyNameProblem("zz-new", "zz-test-loop", taken)).toBe("");
		expect(defaultCopyName("codex", new Set(["codex"]))).toBe("codex-copy");
	});
});