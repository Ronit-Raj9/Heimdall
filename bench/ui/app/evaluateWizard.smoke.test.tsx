// Smoke test of the /evaluate wizard (audit/AGENTFIRST_CONTRACT.md section 6, lane A22).
// It renders the wizard to static markup through all 5 steps with every API answer mocked from
// the contract shapes: 1 Your agent, 2 What to learn, 3 Benchmarks, 4 Model, trials, faults,
// 5 Launch. Hook names are not fixed by the contract, so every use*Query / use*Mutation export
// of the API modules is replaced by a fake picked by its name; unknown hooks stay loading.
// The whole file skips while the wizard does not exist yet.
import "@/lib/i18n";

import { ReduxProvider } from "@/lib/store/provider";
import { render } from "@/lib/testing/render";
import { routerState } from "@/lib/testing/routerMock";
import fs from "node:fs";
import path from "node:path";
import { NuqsTestingAdapter } from "nuqs/adapters/testing";
import type { ComponentType } from "react";
import { describe, expect, it, vi } from "vitest";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

const fx = vi.hoisted(() => {
	const evaluation = {
		id: "ev_e2e",
		spec: {
			name: "oracle v1 vs v2",
			mode: "versions",
			arms: [
				{ label: "A", agent: "oracle", version: 1, model: "claude-sonnet-4-5" },
				{ label: "B", agent: "oracle", version: 2, model: "claude-sonnet-4-5" },
			],
			benchmarks: ["coding-core"],
			tasks_per_benchmark: 5,
			trials: 2,
			faults: ["clean"],
		},
		status: "queued",
		children: [{ benchmark: "coding-core", experiment_id: "exp_e2e", status: "queued" }],
		created_at: "2026-11-21T00:00:00Z",
	};
	const estimate = {
		runs: 20,
		est_cost_usd: { low: 1.25, high: 2.5, known: true },
		est_minutes: { low: 4, high: 9 },
		per_benchmark: [{ benchmark: "coding-core", runs: 20, est_cost_usd: { low: 1.25, high: 2.5, known: true } }],
	};
	const ok = (data: unknown) => ({ data, currentData: data, error: undefined, isLoading: false, isFetching: false, isError: false, isSuccess: true, isUninitialized: false, refetch: () => undefined });
	const loading = () => ({ data: undefined, currentData: undefined, error: undefined, isLoading: true, isFetching: true, isError: false, isSuccess: false, isUninitialized: false, refetch: () => undefined });
	const data = {
		compatibility: {
			agent: "oracle",
			capabilities: ["shell", "tools"],
			benchmarks: [
				{ id: "coding-core", ok: true, requires: ["shell"], missing: [], reason: "" },
				{ id: "acme-memory", ok: false, requires: ["sessions"], missing: ["sessions"], reason: "needs sessions" },
				{ id: "team-coding", ok: true, requires: ["shell"], missing: [], reason: "" },
				{ id: "team-memory", ok: false, requires: ["sessions"], missing: ["sessions"], reason: "needs sessions" },
			],
		},
		packs: [
			{ id: "coding", name: "Coding agent", benchmarks: ["coding-core"], capability: "coding" },
			{ id: "memory", name: "Memory", benchmarks: ["acme-memory"], capability: "memory" },
		],
		versions: {
			agent: "oracle",
			versions: [
				{ version: 1, hash: "h1", created_at: "2026-11-20T00:00:00Z", created_by: "local", note: "first", runs: 0 },
				{ version: 2, hash: "h2", created_at: "2026-11-21T00:00:00Z", created_by: "local", note: "second", runs: 0 },
			],
		},
		estimate,
		evaluation,
		evaluations: { evaluations: [evaluation] },
		models: {
			providers: [
				{ id: "anthropic", name: "Anthropic", needs_base_url: false, key_hint: "", bifrost_provider: "anthropic", custom_base_url: false, models: [{ id: "claude-sonnet-4-5", purposes: ["agent"], rank: 1 }] },
				{ id: "openrouter", name: "OpenRouter", needs_base_url: false, key_hint: "", bifrost_provider: "openrouter", custom_base_url: false, models: [{ id: "deepseek/deepseek-chat", purposes: ["agent"], rank: 1, open_weights: true, family: "deepseek" }] },
			],
			keys: [],
			keys_ready: true,
		},
		agents: {
			agents: [
				{
					id: "oracle", path: "agents/oracle.yaml", adapter: "scripted", image: "bench/coding-core-base:1", dockerfile: "", base_arg: "", per_suite_build: false,
					parser: "", evidence_level: "gateway", model: "claude-sonnet-4-5", scripted_kind: "oracle", notes_project_file: "CLAUDE.md", notes_user_file: "",
					budgets: { wall_s: 900, cpus: 1, mem_gb: 1 }, gateway: { provider: "anthropic", max_retries: 0 }, env_names: [], file_paths: [], sealed_runs: 0,
					capabilities: ["shell", "tools"],
				},
			],
		},
		suites: undefined as unknown,
	};
	// The fixture a hook gets, by its name; undefined means "stay loading".
	const pick = (name: string): unknown => {
		const n = name.toLowerCase();
		if (n.includes("compatib")) return data.compatibility;
		if (n.includes("pack")) return data.packs;
		if (n.includes("version")) return data.versions;
		if (n.includes("estimate")) return data.estimate;
		if (/evaluations?(list)?query$/.test(n) && n.includes("evaluations")) return data.evaluations;
		if (n.includes("evaluation")) return data.evaluation;
		if (n.includes("models")) return data.models;
		if (/agentsquery$/.test(n)) return data.agents;
		if (/suites(light)?query$/.test(n)) return data.suites; // N194: step 3 reads the light list
		return undefined;
	};
	// The value is picked when the hook runs, so fixtures set after the mocks load still count.
	const fake = (name: string) => {
		if (name.endsWith("Mutation")) {
			return () => {
				const value = pick(name);
				const trigger = () => ({ unwrap: () => Promise.resolve(value) });
				return [trigger, { isLoading: false, error: undefined, data: name.toLowerCase().includes("estimate") ? value : undefined, reset: () => undefined }];
			};
		}
		const result = () => {
			const value = pick(name);
			return value === undefined ? loading() : ok(value);
		};
		if (name.startsWith("useLazy")) return () => [() => ({ unwrap: () => Promise.resolve(pick(name)) }), result(), {}];
		return result;
	};
	// Replaces every hook export of a module by its fake.
	const wrap = (mod: Record<string, unknown>) => {
		const out: Record<string, unknown> = { ...mod };
		for (const key of Object.keys(mod)) if (/^use\w*(Query|Mutation)$/.test(key)) out[key] = fake(key);
		return out;
	};
	const safeOriginal = async (load: () => Promise<unknown>) => {
		try {
			return wrap((await load()) as Record<string, unknown>);
		} catch {
			return {};
		}
	};
	return { data, safeOriginal };
});

const fixtures = path.resolve(__dirname, "../public/fixtures");
fx.data.suites = JSON.parse(fs.readFileSync(path.join(fixtures, "suites.json"), "utf8"));
{
	// The wizard offers only the org's own and built-in benchmarks with ready tasks (no practice
	// examples), so add two of the org's own next to the fixture list.
	const list = fx.data.suites as { suites: Array<Record<string, unknown>> };
	const core = list.suites.find((x) => x.id === "coding-core")!;
	list.suites.push({ ...core, id: "team-coding" }, { ...core, id: "team-memory", track: "memory" });
}

vi.mock("@/lib/store/apis/evaluationsApi", async (importOriginal) => fx.safeOriginal(importOriginal));
vi.mock("@/lib/store/apis/modelsApi", async (importOriginal) => fx.safeOriginal(importOriginal));
vi.mock("@/lib/store/apis/benchApi", async (importOriginal) => fx.safeOriginal(importOriginal));
vi.mock("@/lib/store/apis/agentBuilderApi", async (importOriginal) => fx.safeOriginal(importOriginal));
vi.mock("@/lib/store/apis/suitesLightApi", async (importOriginal) => fx.safeOriginal(importOriginal));

// The wizard: the first of these that exists (a views/ file named like the wizard, else the page).
const candidates = {
	...import.meta.glob<Record<string, unknown>>("./evaluate/**/*.tsx"),
	...import.meta.glob<Record<string, unknown>>("../components/evaluate/**/*.tsx"),
};
const files = Object.keys(candidates).filter((file) => !/\.test\.tsx$/.test(file));
const rank = (file: string) =>
	/wizard/i.test(file) ? 0 : /evaluate\/views\/\w*view\.tsx$/i.test(file) ? 1 : /evaluate\/page\.tsx$/.test(file) ? 2 : 9;
const wizardFile = files.filter((file) => rank(file) < 9).sort((a, b) => rank(a) - rank(b))[0];

// Choices of earlier steps, under every name a wizard is likely to keep them in the URL.
const chosen: Record<string, string> = {
	agent: "oracle",
	version: "1",
	versions: "1,2",
	a: "1",
	b: "2",
	mode: "versions",
	goal: "versions",
	learn: "versions",
	benchmarks: "coding-core",
	bench: "coding-core",
	pack: "coding",
	model: "claude-sonnet-4-5",
	provider: "anthropic",
	trials: "2",
	tasks: "5",
	faults: "clean",
};
// The same choices as a wizard draft, for a wizard that keeps them in state instead of the URL.
const initialDraft = {
	agent: "oracle",
	version: 1,
	mode: "versions",
	compareVersion: 2,
	arms: [
		{ label: "A", agent: "oracle", version: 1, model: "claude-sonnet-4-5", provider: "anthropic" },
		{ label: "B", agent: "oracle", version: 2, model: "claude-sonnet-4-5", provider: "anthropic" },
	],
	benchmarks: ["coding-core"],
	pack: null,
	tasksPerBenchmark: 5,
	trials: 2,
	faults: ["clean"],
};
const stepNames: string[][] = [
	["agent"],
	["goal", "learn", "mode", "what"],
	["benchmarks", "bench"],
	["model", "settings", "options"],
	["launch", "review", "run", "confirm"],
];

describe.skipIf(!wizardFile)("evaluate wizard smoke", () => {
	const load = async (): Promise<ComponentType<Record<string, unknown>>> => {
		const mod = await candidates[wizardFile]();
		const named = Object.entries(mod).find(([key, value]) => /Wizard|View|Page/.test(key) && typeof value === "function");
		const component = (mod.default ?? named?.[1]) as ComponentType<Record<string, unknown>> | undefined;
		if (!component) throw new Error(`${wizardFile} exports no component`);
		return component;
	};

	const renderStep = (Wizard: ComponentType<Record<string, unknown>>, step: string, index: number) => {
		const search = { ...chosen, step };
		routerState.location = { pathname: "/evaluate", href: `/evaluate?step=${step}`, search };
		return render(
			<NuqsTestingAdapter searchParams={search}>
				<ReduxProvider>
					<Wizard step={index + 1} initialStep={index + 1} initial={initialDraft} />
				</ReduxProvider>
			</NuqsTestingAdapter>,
		);
	};

	it("renders all 5 steps with mocked API answers", async () => {
		const Wizard = await load();
		const pages: string[] = [];
		for (let i = 0; i < 5; i++) {
			let html = "";
			for (const step of [String(i + 1), ...stepNames[i]]) {
				const candidate = renderStep(Wizard, step, i);
				if (!pages.includes(candidate)) {
					html = candidate;
					break;
				}
			}
			expect(html, `step ${i + 1} renders a page of its own`).not.toBe("");
			pages.push(html);
		}
		expect(pages[0]).toMatch(/agent/i);
		expect(pages[1]).toMatch(/v1|v2|version/i);
		// Benchmarks: the compatible one offered, the incompatible one shown with its reason.
		expect(pages[2]).toContain("team-coding");
		expect(pages[2]).toMatch(/needs sessions/i);
		// No practice examples and no benchmark without a ready task.
		expect(pages[2]).not.toContain('data-testid="step3-benchmark-coding-core"');
		expect(pages[2]).not.toContain('data-testid="step3-benchmark-acme-payments"');
		expect(pages[2]).not.toContain('data-testid="step3-benchmark-harbor-shell"');
		// Model step: the model catalog and the live estimate.
		expect(pages[3]).toMatch(/claude-sonnet-4-5|deepseek/);
		expect(pages[3]).toMatch(/1\.25|2\.50?|20 runs|\b20\b/);
		expect(pages[4]).toMatch(/launch/i);
	});
});
