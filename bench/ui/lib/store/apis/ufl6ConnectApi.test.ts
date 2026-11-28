import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { githubAgentFilePath, verifyRequestBody } from "./ufl6ConnectApi";

// Read, not imported: the hosted server image builds the UI without handlers/testdata (lib/serverImage.test.ts).
const bodies = JSON.parse(readFileSync(fileURLToPath(new URL("../../../../handlers/testdata/ufl6_web_bodies.json", import.meta.url)), "utf8"));

// The same file is posted to the Go handlers in bench/handlers/ufl6_webbodies_test.go.
describe("Connect verify body", () => {
	it("sends the benchmark and task for a verify on your own task", () => {
		expect(verifyRequestBody("ask-bot", { suite: "smoke", task: " t1 ", connectionOnly: false })).toEqual(bodies.verify_own_task);
	});
	it("sends only skip_real_task for connection only", () => {
		expect(verifyRequestBody("ask-bot", { suite: "smoke", task: "t1", connectionOnly: true })).toEqual(bodies.verify_connection_only);
	});
	it("sends the agent alone by default, and no task without a benchmark", () => {
		expect(verifyRequestBody("ask-bot", { suite: "", task: "", connectionOnly: false })).toEqual(bodies.verify_default);
		expect(verifyRequestBody("ask-bot", { suite: "", task: "t1", connectionOnly: false })).toEqual(bodies.verify_default);
	});
	it("explain and agent file bodies carry one field each", () => {
		expect(Object.keys(bodies.explain)).toEqual(["text"]);
		expect(Object.keys(bodies.agent_file)).toEqual(["yaml"]);
	});
});

describe("GitHub agent file path", () => {
	it("reads owner/repo and a github.com URL", () => {
		expect(githubAgentFilePath("acme/bot")).toBe("/github/repos/acme/bot/agent-file");
		expect(githubAgentFilePath("https://github.com/acme/bot.git")).toBe("/github/repos/acme/bot/agent-file");
		expect(githubAgentFilePath("acme")).toBeNull();
	});
});
