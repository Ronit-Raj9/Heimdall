import i18n from "@/lib/i18n";

import { normalizeSecretName, secretNameProblem } from "@/lib/types/secrets";
import { typedSecretName } from "./secretsTab";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { SecretsTable } from "./secretsTab";

const rows = [{ id: "key_1", name: "TAVILY_API_KEY", created_by: "a@x.io", created_at: "2026-11-01T00:00:00Z", updated_at: "2026-11-02T00:00:00Z" }];

describe("SecretsTable", () => {
	it("lists names with a stored badge and never a value", () => {
		const html = renderToStaticMarkup(<SecretsTable secrets={rows} canWrite />);
		expect(html).toContain('data-testid="secret-row-key_1"');
		expect(html).toContain("TAVILY_API_KEY");
		expect(html).toContain("Stored");
		expect(html).toContain('data-testid="secret-rotate-key_1"');
		expect(html).toContain('data-testid="secret-delete-key_1"');
	});

	it("hides write actions from viewers and members", () => {
		const html = renderToStaticMarkup(<SecretsTable secrets={rows} canWrite={false} />);
		expect(html).not.toContain("secret-rotate-key_1");
		expect(html).not.toContain("secret-delete-key_1");
	});

	it("offers a next step when empty", () => {
		expect(renderToStaticMarkup(<SecretsTable secrets={[]} canWrite />)).toContain("No secrets yet.");
		expect(renderToStaticMarkup(<SecretsTable secrets={[]} canWrite={false} />)).toContain("Ask an admin.");
	});
});

describe("secret names", () => {
	it("normalizes and checks like the server", () => {
		expect(normalizeSecretName("tavily api-key!")).toBe("TAVILY_API_KEY");
		// Q-242: the field no longer rewrites the name; a bad character stays and fails the format check.
		expect(typedSecretName("tavily api-key")).toBe("TAVILY API-KEY");
		expect(secretNameProblem(typedSecretName("tavily api-key"))).toBe("format");
		expect(secretNameProblem("")).toBe("required");
		expect(secretNameProblem("1A")).toBe("format");
		expect(secretNameProblem("HEIMDALL_MASTER_KEY")).toBe("reserved");
		expect(secretNameProblem("PATH")).toBe("reserved");
		expect(secretNameProblem("TAVILY_API_KEY")).toBeNull();
	});
});

describe("secrets usage note", () => {
	it("documents the agent env {{secret.NAME}} reference verbatim", () => {
		const note = i18n.t("agentUsage", { ns: "secrets", ref: "{{secret.TAVILY_API_KEY}}" });
		expect(note).toContain("{{secret.TAVILY_API_KEY}}");
		expect(note).toContain("A missing secret stops the run.");
	});
});
