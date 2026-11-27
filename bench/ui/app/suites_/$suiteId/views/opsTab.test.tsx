import "@/lib/i18n";

import { LOCAL_SESSION } from "@/lib/session/sessionContext";
import { hostedSession } from "@/lib/testing/platformFixtures";
import { mutation, render } from "@/lib/testing/render";
import type { SuiteSummary } from "@/lib/types/bench";
import { describe, expect, it, vi } from "vitest";
import { ReadinessChecklist } from "@/components/suites/readinessChecklist";
import { useTranslation } from "react-i18next";
import { CommitSources, OpsTab, repoParams } from "./opsTab";

// M6-12, M6-29: the readiness steps moved to ReadinessChecklist and task from a commit and history
// mining to CommitSources (Build "From a bug fix commit"); OpsTab keeps pausing tasks.
function Commit({ hosted = false }: { hosted?: boolean }) {
	const { t } = useTranslation("suiteops");
	return <CommitSources suite={{ id: "coding-core", tasks: [] } as unknown as SuiteSummary} hosted={hosted} t={t} />;
}
const own = (s: SuiteSummary) => ({ ...s, lock_state: "unlocked", admitted_count: 0, task_count: 2 }) as unknown as SuiteSummary;

vi.mock("@/lib/store/apis/suiteOpsApi", () => ({
	useQuarantineTaskMutation: () => mutation(),
	useRestoreTaskMutation: () => mutation(),
}));
vi.mock("@/lib/store/apis/jobRunApi", () => ({ useCreateJobMutation: () => mutation() }));
vi.mock("@/lib/store/hooks", () => ({ useAppDispatch: () => vi.fn() }));
// The real button asks its confirm in a dialog; the stand-in shows the question so it can be read.
vi.mock("@/components/jobs/jobRunButton", () => ({
	JobRunButton: (p: { kind: string; label: string; confirm?: string }) => (
		<button type="button" data-testid={`job-run-${p.kind}`} data-confirm={p.confirm ?? ""}>
			{p.label}
		</button>
	),
}));

const suite = {
	id: "coding-core",
	tasks: [{ id: "fix-a", quarantine: { reason: "flaky verifier", at: "2026-11-01T00:00:00Z", source: "manual" } }, { id: "fix-b" }],
} as unknown as SuiteSummary;

describe("OpsTab", () => {
	it("offers checks, quarantine, restore, task from commit and mine to a member", () => {
		const html = render(<OpsTab suite={suite} />);
		const steps = render(<ReadinessChecklist suite={own(suite)} writable />);
		expect(steps).toContain('data-testid="job-run-suite.lint"');
		expect(steps).toContain('data-testid="job-run-suite.admit"');
		expect(html).not.toContain("job-run-suite.admit");
		expect(html).toContain('data-testid="job-run-suite.quarantine-flaky"');
		expect(html).toContain('data-testid="suite-ops-restore-fix-a"');
		expect(html).toContain('data-testid="suite-ops-quarantine-fix-b"');
		expect(html).toContain("flaky verifier");
		const commit = render(<Commit />);
		expect(commit).toContain('data-testid="suite-ops-from-commit-disabled"');
		expect(commit).toContain('data-testid="suite-ops-mine-disabled"');
		// One repository field for both.
		expect(commit.match(/data-testid="suite-ops-commit-repo"/g)).toHaveLength(1);
	});
	it("shows a viewer the state but no actions", () => {
		const html = render(<OpsTab suite={suite} />, { ...LOCAL_SESSION, authMode: "hosted", role: "viewer" });
		expect(html).toContain('data-testid="suite-ops-viewer"');
		expect(html).not.toContain("job-run-");
		expect(html).not.toContain("suite-ops-restore-fix-a");
		expect(render(<ReadinessChecklist suite={own(suite)} writable={false} hosted />, { ...LOCAL_SESSION, authMode: "hosted", role: "viewer" })).not.toContain(
			"job-run-",
		);
	});
	it("offers hosted admission and a full mine to a hosted member", () => {
		const html = render(<ReadinessChecklist suite={own(suite)} writable hosted />, hostedSession("member"));
		expect(html).toContain('data-testid="job-run-suite.admit"');
		expect(html).toContain("on a worker. Ready tasks can be run right after.");
		expect(render(<Commit hosted />, hostedSession("member"))).toContain('data-testid="suite-ops-mine-dry-run"');
	});
	it("keeps a built-in library suite read-only, even for a member", () => {
		const lib = { ...suite, id: "hd-harbor-tb2", builtin: true, source: "library" } as unknown as SuiteSummary;
		const html = render(<OpsTab suite={lib} />, hostedSession("member"));
		expect(html).toContain('data-testid="suite-ops-builtin"');
		expect(html).toContain("Built-in benchmark, read-only");
		expect(html).not.toContain("job-run-");
		expect(html).not.toContain("suite-ops-restore-fix-a");
		expect(html).not.toContain("suite-ops-mine");
		expect(html).not.toContain("suite-ops-hosted-admit");
	});
	it("builds repository params", () => {
		expect(repoParams({ github: " acme/app " })).toEqual({ github: "acme/app" });
		expect(repoParams({ github: "acme/app", branch: "main" })).toEqual({ github: "acme/app", branch: "main" });
		expect(repoParams({ github: "" })).toBeNull();
		expect(repoParams({ repo: "" })).toEqual({ repo: "." });
	});
	it("asks before Freeze and says what it does (N116)", () => {
		const html = render(<ReadinessChecklist suite={own(suite)} writable />);
		expect(html).toMatch(/data-testid="job-run-suite.lock" data-confirm="Freeze coding-core\? Every task is pinned to its current files\./);
	});
	it("shows each task's readiness instead of In use, and the pause reason when paused (N117)", () => {
		const s = {
			id: "zz-test-mcp",
			tasks: [
				{ id: "t1", status: "not_locked", checks: [] },
				{ id: "t2", status: "draft", checks: [] },
				{ id: "t3", status: "quarantined", quarantine: { reason: "flaky", at: "2026-11-01T00:00:00Z", source: "manual" }, checks: [] },
			],
		} as unknown as SuiteSummary;
		const html = render(<OpsTab suite={s} />);
		expect(html).not.toContain("In use");
		expect(html).toMatch(/data-testid="suite-ops-task-t1"[\s\S]*?Not frozen yet/);
		expect(html).toMatch(/data-testid="suite-ops-task-t2"[\s\S]*?Not checked yet/);
		expect(html).toContain("Paused: flaky");
	});
});