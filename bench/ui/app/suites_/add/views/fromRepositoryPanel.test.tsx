import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { RepoTaskResult, repoCandidates, repoTaskBody } from "./fromRepositoryPanel";

describe("From a repository", () => {
	it("posts the repotask.build body the job validates", () => {
		expect(repoTaskBody({ repo_url: " https://github.com/acme/durations.git ", base: "a1", reference: "b2", suite: "core", template: "python-pytest" })).toEqual({
			repo_url: "https://github.com/acme/durations.git",
			base: "a1",
			reference: "b2",
			suite: "core",
			template: "python-pytest",
		});
		expect(repoTaskBody({ repo_url: "https://github.com/a/b", base: "", reference: "b", suite: "core", template: "" })).toBeNull();
		expect(repoTaskBody({ repo_url: "https://github.com/a/b", base: "a", reference: "b", suite: "core", template: "" })).not.toHaveProperty("template");
	});

	it("shows a refused commit pair with the failing control", () => {
		const html = renderToStaticMarkup(
			<RepoTaskResult
				result={{
					suite: "core",
					task: "t02",
					admitted: false,
					reason: "reference_1: want pass, got fail",
					controls: [
						{ name: "reference_1", want: "pass", got: "fail", passed: false },
						{ name: "empty_patch", want: "fail", got: "fail", passed: true },
					],
				}}
			/>,
		);
		expect(html).toContain('data-testid="repotask-refused"');
		expect(html).toContain("reference_1: want pass, got fail");
		expect(html).toContain('data-testid="repotask-control-empty_patch"');
	});

	it("shows an admitted task", () => {
		const html = renderToStaticMarkup(<RepoTaskResult result={{ suite: "core", task: "t01", admitted: true, controls: [] }} />);
		expect(html).toContain("Admitted: t01 joins core.");
	});

	it("reads the candidates the find job returns", () => {
		const c = { base: "a1", reference: "b2", subject: "Fix", lines: 10, files: 2 };
		expect(repoCandidates({ candidates: [c], skipped: 0 })).toEqual([c]);
		expect(repoCandidates(undefined)).toEqual([]);
	});
});
