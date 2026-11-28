import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { traceDraftJob, draftEstimateText } from "@/components/inbox/proposalBar";
import type { ModelKey } from "@/lib/types/models";
import { failedExperiments, investigationBody, rerunBody, rerunCap } from "./findingPlans";
import type { ExperimentStatus } from "@/lib/types/bench";
import { defaultJobModel, plainJobError } from "./jobModel";

// Lane F1: every paid body these forms send is held against the Go struct that reads it. The
// server refuses unknown fields and a job with no model; the old Find out why body had no
// model, and its unit tests never compared it with the server, so it failed only on the site.

const goSrc = (file: string) => readFileSync(resolve(__dirname, "../../..", file), "utf8");

/** The json tags of a Go struct, with the embedded modelParams expanded. */
function goFields(file: string, struct: string): string[] {
	const src = goSrc(file);
	const body = src.match(new RegExp(`type ${struct} struct \\{([\\s\\S]*?)\\n\\}`))?.[1] ?? "";
	const tags = [...body.matchAll(/json:"([a-z_]+)/g)].map((m) => m[1]);
	if (/^\s*modelParams\b/m.test(body)) tags.push(...goFields("cmd/bifrost-bench/investigatorhook.go", "modelParams"));
	return tags;
}

const key = (provider: string, status: ModelKey["status"], models: string[]): ModelKey =>
	({
		id: provider + status,
		provider,
		label: "k",
		last4: "1234",
		status,
		models,
		budget_usd: 0,
		month_spent_usd: 0,
		over_budget: false,
	}) as unknown as ModelKey;

describe("Find out why sends what the server reads (PH2-1)", () => {
	const fields = goFields("cmd/bifrost-bench/jobs_findings_w442.go", "investigateParams");
	it("reads the Go struct", () => expect(fields).toEqual(expect.arrayContaining(["exp_id", "provider", "model", "budget_usd", "yes"])));
	it("sends only known fields, with a model and its provider", () => {
		const model = defaultJobModel({ keys: [key("openai", "ok", ["gpt-5.4-mini"])] });
		const body = investigationBody("exp1", model, 0.75);
		for (const k of Object.keys(body)) expect(fields).toContain(k);
		expect(body).toEqual({ exp_id: "exp1", provider: "openai", model: "gpt-5.4-mini", budget_usd: 0.75, yes: true });
	});
});

describe("Draft with a model sends what the server reads (PT-2)", () => {
	const fields = goFields("cmd/bifrost-bench/jobs_tracedraft_w445.go", "traceDraftParams");
	it("sends only known fields, with the picked model", () => {
		const job = traceDraftJob("it1", "s1", "qa", { provider: "openai", model: "gpt-5.4-mini" }, 0.5);
		expect(job.kind).toBe("trace.draft");
		for (const k of Object.keys(job.params)) expect(fields).toContain(k);
		expect(job.params.provider).toBe("openai");
		expect(job.params.model).toBe("gpt-5.4-mini");
	});
	it("names an estimate in the confirm, and says when it has none (PT-8)", () => {
		const u = (k: string, o?: Record<string, unknown>) => `${k} ${JSON.stringify(o ?? {})}`;
		expect(draftEstimateText(u, { priced: true, usd: 0.004, max_usd: 0.009 })).toContain("draft.estimate");
		expect(draftEstimateText(u, { priced: false, usd: 0, max_usd: 0 })).toContain("draft.estimateUnknown");
		expect(draftEstimateText(u, undefined)).toBeNull();
	});
});

describe("Check fix sends its cap (lane F1)", () => {
	it("sends only fields of FindingRerunRequest, the cap among them", () => {
		const fields = goFields("handlers/findings_w443.go", "FindingRerunRequest");
		const body = rerunBody("exp1", "A", 1, rerunCap(0.2));
		for (const k of Object.keys(body)) expect(fields).toContain(k);
		expect(body.budget_usd).toBe(0.3);
	});
	it("opens the cap on the estimate with room, at least 0.25", () => {
		expect(rerunCap(null)).toBe(1);
		expect(rerunCap(0.01)).toBe(0.25);
		expect(rerunCap(2)).toBe(3);
	});
});

describe("defaultJobModel", () => {
	it("takes the org default only when its provider has a working key", () => {
		const def = { provider: "anthropic" as const, model: "claude-x" };
		expect(defaultJobModel({ default: def, keys: [key("anthropic", "ok", [])] })).toEqual(def);
		expect(defaultJobModel({ default: def, keys: [key("anthropic", "invalid", []), key("openai", "ok", ["gpt-5.4-mini"])] })).toEqual({
			provider: "openai",
			model: "gpt-5.4-mini",
		});
		expect(defaultJobModel({ default: def, keys: [] })).toBeNull();
	});
});

describe("plainJobError", () => {
	it("drops the server's prefixes", () => {
		expect(plainJobError("create job: invalid job parameters: Pick a model first.")).toBe("Pick a model first.");
	});
	it("never shows the gateway's key error (PT-1)", () => {
		const m = plainJobError("mint the job's model key: mint key for run r_iv_job_01: run id does not match ^r_[a-z0-9]+$");
		expect(m).not.toContain("mint");
		expect(m).not.toContain("^r_");
	});
});

describe("failedExperiments (PH2-5)", () => {
	it("lists only comparisons with failed runs", () => {
		const e = (id: string, failed: number) =>
			({ exp_id: id, name: id, slots: { failed }, finished_at: "2026-11-01T00:00:00Z" }) as unknown as ExperimentStatus;
		expect(failedExperiments([e("a", 0), e("b", 2), e("c", 0)]).map((x) => x.exp_id)).toEqual(["b"]);
	});
});