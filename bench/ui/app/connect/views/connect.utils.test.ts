import { describe, expect, it } from "vitest";
import type { ConnectCandidate, ConnectDetectResponse, VerifyRow } from "@/lib/types/connect";
import {
	bestCandidate,
	connectionOK,
	estimateMinutes,
	importPlatformFor,
	isTerminalJob,
	otherCandidates,
	platformChoice,
	verifyBenchmarkKey,
	rowTone,
	secretQuestions,
	validAgentName,
	withAnswerPath,
} from "./connect.utils";

const cand = (over: Partial<ConnectCandidate>): ConnectCandidate => ({
	kind: "http",
	label: "x",
	confidence: 0.5,
	evidence: [],
	supported: true,
	nonce_found: false,
	...over,
});

describe("connect utils", () => {
	it("picks the first supported candidate", () => {
		const det: ConnectDetectResponse = {
			class: "url",
			candidates: [cand({ supported: false, label: "mcp" }), cand({ label: "http" })],
			probes: [],
			from: "x",
		};
		expect(bestCandidate(det)?.label).toBe("http");
		expect(otherCandidates(det).map((c) => c.label)).toEqual(["mcp"]);
	});

	it("lists each secret question once", () => {
		const c = cand({
			questions: [
				{ kind: "secret", param: "auth_secret", label: "k", secret: "A" },
				{ kind: "secret", param: "headers", label: "k", secret: "A" },
				{ kind: "text", param: "port", label: "p" },
			],
		});
		expect(secretQuestions(c)).toHaveLength(1);
	});

	it("applies a picked answer path", () => {
		const c = withAnswerPath(cand({ questions: [{ kind: "pick_field", param: "answer_path", label: "f", options: ["a.b"] }] }), "a.b");
		expect(c.mapping?.answer_path).toBe("a.b");
		expect(c.mapping?.body).toContain("{{instruction}}");
		expect(c.questions).toHaveLength(1);
	});

	it("reads row and job states", () => {
		expect(rowTone("pass")).toBe("success");
		expect(rowTone("fail")).toBe("destructive");
		expect(isTerminalJob("succeeded") && isTerminalJob("failed") && !isTerminalJob("running")).toBe(true);
		const rows: VerifyRow[] = [
			{ id: "reach", title: "Reach", status: "pass" },
			{ id: "answer", title: "Answer", status: "pass" },
			{ id: "isolation", title: "Iso", status: "fail" },
		];
		expect(connectionOK(rows)).toBe(true);
		expect(connectionOK([{ id: "auth", title: "a", status: "fail" }])).toBe(false);
	});

	it("checks names and estimates", () => {
		expect(validAgentName("bot-acme")).toBe(true);
		expect(validAgentName("New")).toBe(false);
		expect(validAgentName("new")).toBe(false);
		expect(estimateMinutes(0)).toBe(0);
		expect(estimateMinutes(61)).toBe(2);
	});
});

// B6: picking a platform starts clean and maps to the import platform with the same fields.
describe("platform choice", () => {
	it("clears every earlier result", () => {
		expect(platformChoice("vapi")).toEqual({
			platform: "vapi",
			det: undefined,
			cand: undefined,
			saved: undefined,
			inline: undefined,
			jobId: undefined,
			polling: false,
			probeSecrets: {},
		});
	});

	it("finds the import platform of a Connect platform id", () => {
		const list = [{ id: "vapi" }, { id: "agentcore" }, { id: "copilot_studio" }] as never[];
		expect(importPlatformFor("vapi", list)).toEqual({ id: "vapi" });
		expect(importPlatformFor("bedrock_agentcore", list)).toEqual({ id: "agentcore" });
		expect(importPlatformFor("copilot_studio", list)).toEqual({ id: "copilot_studio" });
		expect(importPlatformFor("bedrock_agent", list)).toBeUndefined();
		expect(importPlatformFor("none", list)).toBeUndefined();
	});
});

// B8.task: a practice benchmark on the real-task row is named as the fallback it is.
describe("verify benchmark line", () => {
	it("says when the real task runs on a practice benchmark", () => {
		expect(verifyBenchmarkKey("acme-memory")).toBe("verify.benchmarkPractice");
		expect(verifyBenchmarkKey("coding-core")).toBe("verify.benchmarkPractice");
		expect(verifyBenchmarkKey("gaia@1")).toBe("verify.benchmark");
		expect(verifyBenchmarkKey(undefined)).toBe("verify.noBenchmark");
	});
});