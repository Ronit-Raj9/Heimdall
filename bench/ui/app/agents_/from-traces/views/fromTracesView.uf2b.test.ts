import { describe, expect, it } from "vitest";
import { connectProblem, envBlock, setupChip } from "./fromTracesView";

// Fix wave lane 2b: helpers of Connect from traces.
describe("from traces helpers (fix wave 2b)", () => {
	it("adds an export line when no token was made on the page", () => {
		const env = "A=Bearer%20$HEIMDALL_INGEST_TOKEN";
		expect(envBlock(env, null)).toEqual({ text: `export HEIMDALL_INGEST_TOKEN=<your hmi_ token>\n${env}`, needsExport: true });
		expect(envBlock(env, "hmi_x")).toEqual({ text: "A=Bearer%20hmi_x", needsExport: false });
		expect(envBlock("B=1", null)).toEqual({ text: "B=1", needsExport: false });
	});

	it("shortens a setup chip and keeps what it covers", () => {
		expect(setupChip("OpenInference (LangChain, LangGraph)")).toEqual({ name: "OpenInference", covers: "LangChain, LangGraph" });
		expect(setupChip("Pydantic Logfire")).toEqual({ name: "Pydantic Logfire", covers: "" });
	});

	it("names the first wrong Connect field", () => {
		expect(connectProblem({ id: "Bad", url: "https://x.io", authSecret: "" })).toBe("name");
		expect(connectProblem({ id: "bot", url: "ftp://x", authSecret: "" })).toBe("url");
		expect(connectProblem({ id: "bot", url: "https://x.io", authSecret: "sk-not a name" })).toBe("secret");
		expect(connectProblem({ id: "bot", url: "https://x.io", authSecret: "BOT_TOKEN" })).toBeNull();
	});
});
