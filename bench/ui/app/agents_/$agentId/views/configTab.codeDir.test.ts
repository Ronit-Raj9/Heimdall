import { describe, expect, it } from "vitest";
import { agentCodeDir } from "./configTab";

describe("agentCodeDir: the folder the Setup tab's Edit code link opens", () => {
	it("is the folder of build.dockerfile inside the agents area", () => {
		const yaml = "id: my-custom-python\nbuild:\n  dockerfile: agents/images/my-custom-python/Dockerfile\n  tag: bench/agent-my-custom-python:1\n";
		expect(agentCodeDir(yaml)).toBe("images/my-custom-python");
	});
	it("reads a quoted path with a trailing comment", () => {
		expect(agentCodeDir('build:\n  dockerfile: "agents/images/a/Dockerfile" # own image\n')).toBe("images/a");
	});
	it("is empty without a build, outside the agents area, or with a parent step", () => {
		expect(agentCodeDir("id: remote\nadapter: remote_http\n")).toBe("");
		expect(agentCodeDir("build:\n  dockerfile: examples/frameworks/x/Dockerfile\n")).toBe("");
		expect(agentCodeDir("build:\n  dockerfile: agents/../secrets/Dockerfile\n")).toBe("");
	});
});
