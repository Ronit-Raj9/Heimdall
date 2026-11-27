import "@/lib/i18n";

import { mutation, okQuery, render } from "@/lib/testing/render";
import { LOCAL_SESSION } from "@/lib/session/sessionContext";
import type { SuiteSummary } from "@/lib/types/bench";
import { describe, expect, it, vi } from "vitest";
import { EditTab, groupFiles, LintResult } from "./editTab";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("@/components/jobs/jobRunButton", () => ({ JobRunButton: ({ kind }: { kind: string }) => <button data-testid={`job-run-${kind}`} /> }));
vi.mock("@/lib/store/apis/suiteAuthoringApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/suiteAuthoringApi")>()),
	useGetSuiteLintQuery: () => okQuery({ suite: "s", version: "0.1.0", tasks: 1, probes: 1, problems: [], errors: 0, warnings: 0 }),
	useGetSuiteFilesQuery: () =>
		okQuery({ suite: "s", truncated: false, files: [{ path: "suite.yaml", size: 1, dir: false, mod_time: "" }, { path: "tasks", size: 0, dir: true, mod_time: "" }] }),
	useGetSuiteFileQuery: () => okQuery({ path: "suite.yaml", size: 1, sha256: "x", binary: false, content: "id: s" }),
	useCreateSuiteTaskMutation: () => mutation(),
	usePutSuiteFileMutation: () => mutation(),
	useDeleteSuiteFileMutation: () => mutation(),
	useUploadSuiteZipMutation: () => mutation(),
}));

const suite = { id: "s", version: "0.1.0", tasks: [], lock_state: "missing" } as unknown as SuiteSummary;

describe("EditTab", () => {
	it("shows files, lint result and the lint and lock jobs for a member", () => {
		const html = render(<EditTab suite={suite} />);
		expect(html).toContain('data-testid="suite-edit-tab"');
		expect(html).toContain("suite.yaml");
		expect(html).toContain('data-testid="suite-edit-lint-ok"');
		expect(html).toContain('data-testid="job-run-suite.lint"');
		expect(html).toContain('data-testid="job-run-suite.lock"');
		expect(html).toContain('data-testid="suite-edit-add-task"');
	});
	it("is read-only for a viewer", () => {
		const html = render(<EditTab suite={suite} />, { ...LOCAL_SESSION, authMode: "hosted", signedIn: true, role: "viewer" });
		expect(html).toContain('data-testid="suite-edit-readonly"');
		expect(html).not.toContain("suite-edit-files");
	});
	it("is read-only for a built-in library suite, even for a member", () => {
		const html = render(<EditTab suite={{ ...suite, builtin: true, source: "library" } as SuiteSummary} />);
		expect(html).toBe("");
		expect(html).not.toContain("suite-edit-tab");
		expect(html).not.toContain("job-run-suite.lock");
	});
	it("lists lint problems and a load error", () => {
		const bad = render(
			<LintResult
				report={{ suite: "s", version: "", tasks: 1, probes: 0, errors: 1, warnings: 0, problems: [{ path: "tasks/t01/instruction.md", message: "EDIT ME", severity: "error" }] }}
			/>,
		);
		expect(bad).toContain("tasks/t01/instruction.md");
		const broken = render(<LintResult report={{ suite: "s", version: "", tasks: 0, probes: 0, errors: 1, warnings: 0, problems: [], load_error: "schema" }} />);
		expect(broken).toContain('data-testid="suite-edit-lint-load-error"');
	});
});

describe("groupFiles (M6-39)", () => {
	it("groups task files by task folder, root files first, numbers in order", () => {
		const groups = groupFiles(["tasks/t10/task.toml", "suite.toml", "tasks/t2/task.toml", "tasks/t2/tests/test_a.py", "base/Dockerfile"]);
		expect(groups.map((g) => g.folder)).toEqual(["", "base", "tasks/t2", "tasks/t10"]);
		expect(groups[2].files.map((f) => f.name)).toEqual(["task.toml", "tests/test_a.py"]);
		expect(groups[2].files[0].path).toBe("tasks/t2/task.toml");
	});
});
