import "@/lib/i18n";

import { idProblem } from "@/lib/inputChecks";
import { describe, expect, it, vi } from "vitest";

vi.mock("@/lib/store/apis/mcpTestApi", () => ({ useInspectMcpServerMutation: () => [], useDraftMcpBenchmarkMutation: () => [] }));
vi.mock("@/lib/store/apis/secretsApi", () => ({ useListSecretsQuery: () => ({}) }));
vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

const { EMPTY_FORM, draftRequest, formProblem } = await import("./mcpTestView");

describe("formProblem", () => {
	it("asks for the header name when a credential goes in a custom header", () => {
		expect(formProblem({ ...EMPTY_FORM, url: "https://x.example/mcp", secret: "s", mode: "header", header: " " })).toBe("header");
	});
	it("asks for the address first and passes a complete form", () => {
		expect(formProblem({ ...EMPTY_FORM })).toBe("url");
		expect(formProblem({ ...EMPTY_FORM, url: "https://x.example/mcp", secret: "s", mode: "header", header: "X-Key" })).toBeNull();
		expect(formProblem({ ...EMPTY_FORM, url: "https://x.example/mcp", mode: "header" })).toBeNull();
	});
});

describe("the draft form", () => {
	it("refuses a bad benchmark id", () => {
		expect(idProblem("Bad ID!!", "benchmark id")).not.toBeNull();
	});
	it("sends the normalised server name, so dashes can be typed", () => {
		const body = draftRequest({ url: "https://x.example/mcp", call_read_only: true }, " shop-mcp ", "Ab-Cd-", ["t1"]);
		expect(body.server_id).toBe("ab-cd");
		expect(body.id).toBe("shop-mcp");
	});
});