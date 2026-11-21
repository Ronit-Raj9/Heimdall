import "@/lib/i18n";

import { LOCAL_SESSION } from "@/lib/session/sessionContext";
import { hostedSession } from "@/lib/testing/platformFixtures";
import { mutation, render } from "@/lib/testing/render";
import type { SuiteSummary } from "@/lib/types/bench";
import { describe, expect, it, vi } from "vitest";
import { OpsTab, repoParams } from "./opsTab";

vi.mock("@/lib/store/apis/suiteOpsApi", () => ({
	useQuarantineTaskMutation: () => mutation(),
	useRestoreTaskMutation: () => mutation(),
}));
vi.mock("@/lib/store/apis/jobRunApi", () => ({ useCreateJobMutation: () => mutation() }));
vi.mock("@/lib/store/hooks", () => ({ useAppDispatch: () => vi.fn() }));

const suite = {
	id: "coding-core",
	tasks: [
		{ id: "fix-a", quarantine: { reason: "flaky verifier", at: "2026-11-01T00:00:00Z", source: "manual" } },
		{ id: "fix-b" },
	],
} as unknown as SuiteSummary;

describe("OpsTab", () => {
	it("offers checks, quarantine, restore, task from commit and mine to a member", () => {
		const html = render(<OpsTab suite={suite} />);
		expect(html).toContain('data-testid="job-run-suite.lint"');
		expect(html).toContain('data-testid="job-run-suite.admit"');
		expect(html).toContain('data-testid="job-run-suite.quarantine-flaky"');
		expect(html).toContain('data-testid="suite-ops-restore-fix-a"');
		expect(html).toContain('data-testid="suite-ops-quarantine-fix-b"');
		expect(html).toContain("flaky verifier");
		expect(html).toContain('data-testid="suite-ops-from-commit-disabled"');
		expect(html).toContain('data-testid="suite-ops-mine-disabled"');
	});
	it("shows a viewer the state but no actions", () => {
		const html = render(<OpsTab suite={suite} />, { ...LOCAL_SESSION, authMode: "hosted", role: "viewer" });
		expect(html).toContain('data-testid="suite-ops-viewer"');
		expect(html).not.toContain("job-run-");
		expect(html).not.toContain("suite-ops-restore-fix-a");
		expect(html).toContain('data-testid="suite-ops-hosted-admit"');
	});
	it("offers hosted admission and a full mine to a hosted member", () => {
		const html = render(<OpsTab suite={suite} />, hostedSession("member"));
		expect(html).toContain('data-testid="job-run-suite.admit"');
		expect(html).toContain('data-testid="suite-ops-hosted-admit"');
		expect(html).toContain('data-testid="suite-ops-mine-dry-run"');
	});
	it("builds repository params", () => {
		expect(repoParams({ github: " acme/app " })).toEqual({ github: "acme/app" });
		expect(repoParams({ github: "acme/app", branch: "main" })).toEqual({ github: "acme/app", branch: "main" });
		expect(repoParams({ github: "" })).toBeNull();
		expect(repoParams({ repo: "" })).toEqual({ repo: "." });
	});
});
