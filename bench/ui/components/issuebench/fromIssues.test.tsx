import "@/lib/i18n";

import { JOB_KIND_LABELS } from "@/components/jobs/jobKindLabels";
import { hostedSession } from "@/lib/testing/platformFixtures";
import { mutation, okQuery, render, tagOf } from "@/lib/testing/render";
import type { IssueItem, IssueReadiness, IssueRun } from "@/lib/types/issuebench";
import { useTranslation } from "react-i18next";
import { beforeEach, describe, expect, it, vi } from "vitest";
import {
	FromIssues,
	IssueJobLog,
	IssueRow,
	READINESS_POLL_MS,
	ReadinessBanner,
	RunView,
	normalizeRepo,
	retryBody,
	startBlocked,
	startBody,
	stageWords,
} from "./fromIssues";

const api = vi.hoisted(() => ({
	run: {} as unknown,
	readiness: {} as unknown,
	readinessArgs: [] as unknown[],
	github: { connected: true } as { connected: boolean },
}));
vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
// M6-27: the builder model comes from the shared model picker; stub its key list and the picker.
vi.mock("@/lib/store/apis/modelsApi", async (orig) => ({
	...(await orig<typeof import("@/lib/store/apis/modelsApi")>()),
	useGetModelsQuery: () => ({ data: { providers: [], keys: [] } }),
}));
vi.mock("@/components/models/groupedModelPicker", async (orig) => ({
	...(await orig<typeof import("@/components/models/groupedModelPicker")>()),
	GroupedModelPicker: ({ testId }: { testId?: string }) => <select data-testid={`${testId}-model`} />,
}));
vi.mock("@/lib/store/apis/issuebenchApi", () => ({
	useGetIssueReadinessQuery: (...args: unknown[]) => {
		api.readinessArgs = args;
		return api.readiness;
	},
	useGetIssueRunQuery: () => api.run,
	useScanIssuesMutation: () => mutation(),
	useStartIssueRunMutation: () => mutation(),
	useReviewIssueMutation: () => mutation(),
	useRetryIssueMutation: () => mutation(),
}));
vi.mock("@/lib/store/apis/githubApi", () => ({
	useGetGithubStatusQuery: () => okQuery(api.github),
	useListGithubReposQuery: () => okQuery({ repos: [{ full_name: "Ronit-Raj9/Heimdall" }] }),
}));
vi.mock("@/components/jobs/jobLogPanel", () => ({
	JobLogPanel: ({ jobId }: { jobId: string }) => <div data-testid="job-log-panel-mock" data-job={jobId} />,
}));

const READY: IssueReadiness = { hosted: true, worker_online: true, github: true, gateway: true, default_model: true };
const NO_WORKER: IssueReadiness = { ...READY, worker_online: false, message: "No worker online: this job starts containers" };

const item = (over: Partial<IssueItem> = {}): IssueItem => ({
	candidate: {
		repo: "acme/widgets",
		pr: 7,
		issue: 12,
		issue_title: "Crash on empty input",
		issue_body: "It crashes.",
		pr_merged: "2026-10-01T00:00:00Z",
		test_files: ["tests/test_a.py"],
		code_files: ["a.py"],
	},
	version: "2026.10",
	status: "queued",
	...over,
});

const run = (items: IssueItem[]): IssueRun => ({
	id: "ib_1",
	repo: "acme/widgets",
	status: "scanned",
	consent: false,
	funnel: { merged_prs: 3, linked: 2, qualified: 1, reasons: null },
	items,
	cost_usd: 0,
	created: "2026-11-22T00:00:00Z",
});

type T = ReturnType<typeof useTranslation<"issuebench">>["t"];
function WithT({ children }: { children: (t: T) => React.ReactNode }) {
	const { t } = useTranslation("issuebench");
	return <>{children(t)}</>;
}
const html = (f: (t: T) => React.ReactNode) => render(<WithT>{f}</WithT>, hostedSession("member"));

beforeEach(() => {
	api.run = okQuery(run([item()]));
	api.readiness = okQuery(READY);
	api.github = { connected: true };
});

describe("repository field", () => {
	it("accepts any owner/name, including a GitHub link, and normalizes it", () => {
		expect(normalizeRepo("marshmallow-code/marshmallow")).toBe("marshmallow-code/marshmallow");
		expect(normalizeRepo(" https://github.com/marshmallow-code/marshmallow.git/ ")).toBe("marshmallow-code/marshmallow");
		expect(normalizeRepo("github.com/acme/widgets/")).toBe("acme/widgets");
		expect(normalizeRepo("http://www.github.com/acme/widgets")).toBe("acme/widgets");
		expect(normalizeRepo("")).toBe("");
		expect(normalizeRepo("acme")).toBe("");
		expect(normalizeRepo("acme/widgets/tree/main")).toBe("");
		expect(normalizeRepo("../x")).toBe("");
		expect(normalizeRepo("a b/c")).toBe("");
	});

	it("is a text input with the connected account's repositories as suggestions", () => {
		const out = render(<FromIssues writable />, hostedSession("member"));
		const input = tagOf(out, "ib-repo-input") ?? "";
		expect(input).toContain("<input");
		expect(input).toContain('list="');
		expect(out).toContain("<datalist");
		expect(out).toContain('value="Ronit-Raj9/Heimdall"');
		expect(out).toContain('placeholder="owner/repository or a GitHub link"');
		expect(out).not.toContain("<select");
		expect(tagOf(out, "issuebench-scan") ?? "").toContain("disabled");
	});

	it("shows the field when GitHub is not connected and says any account works", () => {
		api.github = { connected: false };
		const out = render(<FromIssues writable />, hostedSession("member"));
		expect(out).toContain('data-testid="ib-repo-input"');
		expect(out).toContain(
			"Connect GitHub in Settings &gt; GitHub (any account). GitHub needs a sign-in to list a repository&#x27;s pull requests, public ones included.",
		);
		expect(tagOf(out, "issuebench-scan") ?? "").toContain("disabled");
	});
});

describe("retryBody", () => {
	it("builds the fix body or null", () => {
		expect(retryBody("retry", null)).toEqual({ fix: "retry" });
		expect(retryBody("open_workers", null)).toEqual({ fix: "open_workers" });
		expect(retryBody("add_secret", " API_TOKEN ")).toEqual({ fix: "add_secret", secrets: ["API_TOKEN"] });
		expect(retryBody("allow_service", "postgres")).toEqual({ fix: "allow_service", allow_services: ["postgres"] });
		expect(retryBody("raise_budget", "5")).toEqual({ fix: "raise_budget", budget_usd: 5 });
		expect(retryBody("raise_budget", "x")).toBeNull();
		expect(retryBody("add_secret", null)).toBeNull();
	});
});

describe("startBody", () => {
	it("sends the model only when both fields are filled", () => {
		expect(startBody(true, " anthropic ", "claude-x")).toEqual({ consent: true, provider: "anthropic", model: "claude-x" });
		expect(startBody(true, "openai", "")).toEqual({ consent: true });
		expect(startBody(false, "", "")).toEqual({ consent: false });
	});
});

describe("readiness", () => {
	it("blocks Start only on the hosted site without a worker or a gateway", () => {
		expect(startBlocked(undefined)).toBe("");
		expect(startBlocked(READY)).toBe("");
		expect(startBlocked({ ...READY, hosted: false, worker_online: false, gateway: false })).toBe("");
		expect(startBlocked(NO_WORKER)).toBe("worker");
		expect(startBlocked({ ...READY, gateway: false })).toBe("gateway");
	});

	it("polls readiness every 15 s while the form is open", () => {
		render(<FromIssues writable />, hostedSession("member"));
		expect(READINESS_POLL_MS).toBe(15000);
		expect(api.readinessArgs).toEqual([undefined, { pollingInterval: 15000 }]);
	});

	it("shows the banner with the message and a Settings > Workers link when no worker is online", () => {
		const out = html((t) => <ReadinessBanner readiness={NO_WORKER} t={t} />);
		expect(out).toContain('data-testid="issuebench-readiness"');
		expect(out).toContain("No worker online: this job starts containers");
		expect(tagOf(out, "issuebench-readiness-workers")).toContain('href="/settings?tab=runners"');
		expect(out).toContain("Open Settings &gt; Workers");
	});

	it("shows the banner without a workers link when the gateway is missing", () => {
		const out = html((t) => <ReadinessBanner readiness={{ ...READY, gateway: false }} t={t} />);
		expect(out).toContain('data-testid="issuebench-readiness"');
		expect(out).toContain("no model gateway");
		expect(out).not.toContain('data-testid="issuebench-readiness-workers"');
	});

	it("hides the banner when ready or local", () => {
		expect(html((t) => <ReadinessBanner readiness={READY} t={t} />)).not.toContain("issuebench-readiness");
		expect(html((t) => <ReadinessBanner readiness={{ ...NO_WORKER, hosted: false }} t={t} />)).not.toContain("issuebench-readiness");
	});

	it("disables Start with a plain title when no worker is online", () => {
		const out = html((t) => <RunView id="ib_1" writable readiness={NO_WORKER} t={t} />);
		expect(out).toContain('data-testid="issuebench-readiness"');
		const start = tagOf(out, "issuebench-start") ?? "";
		expect(start).toContain("disabled");
		expect(start).toContain('title="Start needs a worker online"');
	});

	it("leaves Start without a block title when ready", () => {
		const out = html((t) => <RunView id="ib_1" writable readiness={READY} t={t} />);
		expect(out).not.toContain('data-testid="issuebench-readiness"');
		expect(tagOf(out, "issuebench-start") ?? "").not.toContain("title=");
	});

	it("disables Start when the gateway is missing", () => {
		const out = html((t) => <RunView id="ib_1" writable readiness={{ ...READY, gateway: false }} t={t} />);
		expect(tagOf(out, "issuebench-start") ?? "").toContain('title="Start needs the server&#x27;s model gateway"');
	});
});

describe("issue rows", () => {
	const row = (it: IssueItem) =>
		html((t) => (
			<table>
				<tbody>
					<IssueRow runId="ib_1" it={it} writable t={t} />
				</tbody>
			</table>
		));

	it("names the stage a worker runs", () => {
		const out = row(item({ status: "building", stage: "build", job_id: "job_b1" }));
		expect(out).toContain("Building the environment on a worker");
		expect(row(item({ status: "built", stage: "validate" }))).toContain("Running the tests on a worker");
		expect(row(item({ status: "validated", stage: "checks" }))).toContain("Checking the issue text");
		expect(row(item({ status: "ready", stage: "checks" }))).not.toContain("issuebench-stage-12");
	});

	it("offers Show log for the issue's job and nothing without one", () => {
		const out = row(item({ status: "building", stage: "build", job_id: "job_b1" }));
		expect(out).toContain('data-testid="issuebench-log-toggle-job_b1"');
		expect(out).toContain("Show log");
		expect(out).not.toContain("job-log-panel-mock");
		expect(row(item())).not.toContain("issuebench-log-toggle");
	});

	it("renders JobLogPanel with the job id when the log is open", () => {
		const out = html((t) => <IssueJobLog jobId="job_b1" t={t} initialOpen />);
		expect(out).toContain('data-testid="job-log-panel-mock"');
		expect(out).toContain('data-job="job_b1"');
		expect(out).toContain("Hide log");
	});

	it("links open_workers to Settings > Workers and keeps a retry", () => {
		const out = row(item({ status: "failed", reason: "No worker online: this job starts containers", fix: "open_workers" }));
		expect(tagOf(out, "issuebench-open-workers-12")).toContain('href="/settings?tab=runners"');
		expect(out).toContain("Open workers");
		expect(out).toContain('data-testid="issuebench-fix-12"');
		expect(out).toContain(">Retry<");
	});

	it("keeps the other fixes as before", () => {
		const out = row(item({ status: "failed", fix: "raise_budget" }));
		expect(out).toContain("Raise budget");
		expect(out).not.toContain("issuebench-open-workers-12");
	});

	it("words a stage only while the issue is in progress", () => {
		const t = ((k: string) => k) as unknown as T;
		expect(stageWords({ status: "building", stage: "build" }, t)).toBe("stage.build");
		expect(stageWords({ status: "failed", stage: "build" }, t)).toBe("");
		expect(stageWords({ status: "queued" }, t)).toBe("");
	});
});

describe("job kind label", () => {
	it("names issuebench.work", () => {
		expect(JOB_KIND_LABELS["issuebench.work"]).toBe("Build and test an issue on a worker");
	});
});