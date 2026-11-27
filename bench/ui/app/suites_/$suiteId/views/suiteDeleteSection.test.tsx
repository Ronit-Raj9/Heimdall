import "@/lib/i18n";

import { LOCAL_SESSION } from "@/lib/session/sessionContext";
import { errorQuery, mutation, render } from "@/lib/testing/render";
import { describe, expect, it, vi } from "vitest";
import { SuiteDeleteSection } from "./suiteDeleteSection";

const state = vi.hoisted(() => ({ error: undefined as unknown }));

vi.mock("@tanstack/react-router", () => ({ useNavigate: () => () => undefined }));
vi.mock("@/lib/store/apis/suiteAuthoringApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/suiteAuthoringApi")>()),
	useDeleteSuiteMutation: () => mutation({ error: state.error }),
}));
// The inline question shows only after a click; render it at once to check its words.
vi.mock("@/components/platform/badges", () => ({
	ConfirmButton: (p: { label: string; question: string; confirmLabel: string; testId: string }) => (
		<span data-testid={p.testId}>
			{p.label}|{p.question}|{p.confirmLabel}
		</span>
	),
}));

describe("SuiteDeleteSection (N288)", () => {
	it("is hidden for a built-in benchmark and for a viewer", () => {
		expect(render(<SuiteDeleteSection suiteId="coding-core" builtin />)).toBe("");
		expect(
			render(<SuiteDeleteSection suiteId="zz-test-del" builtin={false} />, {
				...LOCAL_SESSION,
				authMode: "hosted",
				signedIn: true,
				role: "viewer",
			}),
		).toBe("");
	});

	it("asks one plain question before deleting", () => {
		state.error = undefined;
		const html = render(<SuiteDeleteSection suiteId="zz-test-del" builtin={false} />);
		expect(html).toContain("Delete this benchmark");
		// M6-35: the same question and confirm label as the benchmarks list row Delete.
		expect(html).toContain("Delete the benchmark zz-test-del? Its tasks and files are removed. Past results stay. This cannot be undone.");
		expect(html).toContain("|Delete</span>");
	});

	it("shows the server's refusal", () => {
		state.error = errorQuery(409, "zz-test-del has comparisons or evaluations still running. Stop them first, then delete it.").error;
		const html = render(<SuiteDeleteSection suiteId="zz-test-del" builtin={false} />);
		expect(html).toContain("zz-test-del has comparisons or evaluations still running. Stop them first, then delete it.");
	});
});