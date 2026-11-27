import "@/lib/i18n";

import { mutation, okQuery, render } from "@/lib/testing/render";
import type { DataPolicyView, ImportPlatform, PlatformImportResult } from "@/lib/types/annotation";
import type { SuiteSummary } from "@/lib/types/bench";
import i18n from "i18next";
import { describe, expect, it, vi } from "vitest";
import { DataTab, ImportForm, ImportPreview, PolicyForm, VersionTable, missingSecrets, policyRequest } from "./dataTab";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

const view: DataPolicyView = {
	benchmark: "support-faq",
	policy: { pii: { mode: "redact", kinds: ["email"] }, access: { mode: "restricted", users: ["u_2"] }, updated_by: "a@x.io" },
	can_edit_access: false,
	modes: ["off", "warn", "mask", "redact", "block"],
	kinds: ["email", "phone", "card", "national_id", "bank_account", "name"],
};

const langfuse: ImportPlatform = {
	id: "langfuse",
	name: "Langfuse",
	base_url: "https://cloud.langfuse.com",
	kinds: ["dataset", "traces"],
	secrets: [
		{ role: "public_key", label: "Public key", default: "LANGFUSE_PUBLIC_KEY" },
		{ role: "secret_key", label: "Secret key", default: "LANGFUSE_SECRET_KEY" },
	],
	dataset_hint: "Dataset name",
	project_hint: "Not needed: the keys name the project",
	docs: "https://langfuse.com/docs/api",
	configured: { public_key: true, secret_key: false },
};

vi.mock("@/lib/store/apis/annotationApi", () => ({
	useGetDataPolicyQuery: () => okQuery(view),
	useSetDataPolicyMutation: () => mutation(),
	useGetDatasetVersionsQuery: () => okQuery({ benchmark: "support-faq", versions: [] }),
	useGetImportPlatformsQuery: () =>
		okQuery({ platforms: [langfuse], secrets: ["LANGFUSE_PUBLIC_KEY"], secrets_link: "/settings?tab=secrets" }),
	useImportFromPlatformMutation: () => mutation(),
}));

const t = i18n.getFixedT("en", "benchData") as never;
const text = (html: string) => html.replace(/<[^>]+>/g, " ");

describe("benchmark data tab", () => {
	it("shows the policy, import, versions and code sections", () => {
		const html = render(<DataTab suite={{ id: "support-faq", tasks: [] } as unknown as SuiteSummary} />);
		for (const id of ["suite-data-policy", "suite-data-import", "suite-data-versions-empty", "suite-data-api-code"])
			expect(html).toContain(`data-testid="${id}"`);
		// M6-32: the import sits after the versions, under an #import anchor.
		expect(html.indexOf('data-testid="suite-data-versions')).toBeLessThan(html.indexOf('data-testid="suite-data-import"'));
		// Q-189: the import form sits in a closed fold, one line on the Settings tab.
		expect(html).toMatch(/<details id="import"[^>]*data-testid="suite-data-import-fold"><summary/);
		expect(html).toContain('id="import"');
		expect(text(html)).not.toMatch(/sealed|suite |probe|canary|lease/i);
	});

	it("keeps access read only for non admins and sends only the personal data part", () => {
		const html = render(<PolicyForm view={view} writable t={t} />);
		expect(html).toMatch(/disabled=""[^>]*data-testid="suite-data-access-restricted"/);
		expect(html).toContain('data-testid="suite-data-access-admin-only"');
		expect(policyRequest({ mode: "redact", kinds: [], access: "restricted", users: "u_2" }, false)).toEqual({ pii: { mode: "redact" } });
		expect(policyRequest({ mode: "mask", kinds: ["card"], access: "restricted", users: "u_2, u_3 u_2" }, true)).toEqual({
			pii: { mode: "mask", kinds: ["card"] },
			access: { mode: "restricted", users: ["u_2", "u_3"] },
		});
		expect(policyRequest({ mode: "warn", kinds: [], access: "org", users: "u_2" }, true)).toEqual({
			pii: { mode: "warn" },
			access: { mode: "org" },
		});
	});

	it("lists versions with their changes", () => {
		const html = text(
			render(
				<VersionTable
					versions={[
						{
							version: 2,
							at: "2026-11-21T10:00:00Z",
							actor: "ci",
							source: "git:abc",
							added: ["a"],
							updated: ["b", "c"],
							removed: [],
							tasks: 12,
							sha256: "ff",
						},
					]}
					t={t}
				/>,
			),
		);
		expect(html).toContain("v2");
		expect(html).toContain("+1 added, 2 changed");
		expect(html).not.toContain("-0");
		expect(html).toContain("git:abc");
	});

	it("names the missing secrets before an import", () => {
		expect(missingSecrets(langfuse, {}, ["LANGFUSE_PUBLIC_KEY"])).toEqual(["LANGFUSE_SECRET_KEY"]);
		expect(missingSecrets(langfuse, { secret_key: "LF_SK" }, ["LANGFUSE_PUBLIC_KEY", "LF_SK"])).toEqual([]);
		const html = render(
			<ImportForm suite="support-faq" platforms={[langfuse]} secrets={["LANGFUSE_PUBLIC_KEY"]} secretsLink="/settings?tab=secrets" t={t} />,
		);
		expect(text(html)).toContain("Add a secret named LANGFUSE_SECRET_KEY first.");
		expect(html).toMatch(/data-testid="suite-data-import-preview"[^>]*disabled/);
	});

	it("previews what an import reads, with personal data found", () => {
		const result: PlatformImportResult = {
			platform: "langfuse",
			kind: "dataset",
			target: "benchmark",
			read: 2,
			truncated: true,
			warnings: [],
			preview: [{ source_id: "it_1", input: "Where is my order?", expected: "Shipped" }],
			benchmark: {
				benchmark: "support-faq",
				dry_run: true,
				version: null,
				added: ["a", "b"],
				updated: [],
				unchanged: [],
				problems: [],
				pii: { mode: "redact", kinds: { email: 1 }, rows: 1, findings: [], summary: "1 email in 1 row, replaced" },
				next: "",
			},
			next: "",
		};
		const html = text(render(<ImportPreview result={result} t={t} />));
		expect(html).toContain("Read 2 records.");
		expect(html).toContain("More records exist");
		expect(html).toContain("1 email in 1 row, replaced");
		expect(html).toContain("Where is my order?");
	});
});