import type { ClusterSummary } from "@/lib/types/bench";
import { describe, expect, it } from "vitest";
import { clusterKind, filterClusters, plainClusterTitle } from "./clustersTable";

const c = (title: string, extra: Partial<ClusterSummary> = {}) =>
	({ signature: "c_1", title, layers: [], tasks: [], agents: [], ...extra }) as unknown as ClusterSummary;

describe("failure group names", () => {
	it("drops repeated error prefixes and raises the first letter", () => {
		expect(plainClusterTitle("timeout: error: error: reconnecting...")).toBe("Timeout: reconnecting...");
		expect(plainClusterTitle("c_16de4c2f8378af3a: fail")).toBe("Fail");
		expect(plainClusterTitle("Already plain")).toBe("Already plain");
	});
	it("reads the kind from the signature text", () => {
		expect(clusterKind(c("aborted_infra: container died"))).toBe("infra");
		expect(clusterKind(c("timeout: error: reconnecting"))).toBe("infra");
		expect(clusterKind(c("tool_error in bash"))).toBe("tool");
		expect(clusterKind(c("wrong answer"))).toBe("agent");
	});
	it("narrows by title, task or agent", () => {
		const list = [c("wrong answer", { signature: "c_a", tasks: ["s/t01"] }), c("tool_error", { signature: "c_b", agents: ["codex"] })];
		expect(filterClusters(list, "t01").map((x) => x.signature)).toEqual(["c_a"]);
		expect(filterClusters(list, "CODEX").map((x) => x.signature)).toEqual(["c_b"]);
	});
});