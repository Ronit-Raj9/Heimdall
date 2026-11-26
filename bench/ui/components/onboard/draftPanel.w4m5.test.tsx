import i18n from "@/lib/i18n";

import type { OnboardDraftSet } from "@/lib/types/onboard";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";

vi.mock("@tanstack/react-router", () => ({
	Link: ({ to, children, ...rest }: { to: string; children: React.ReactNode }) => (
		<a href={to} {...rest}>
			{children}
		</a>
	),
}));
vi.mock("@/lib/store/apis/onboardApi", () => {
	const idle = () => [() => undefined, { isLoading: false, error: undefined }];
	return { useEditOnboardTaskMutation: idle, useStartOnboardReadinessMutation: idle, usePublishOnboardDraftMutation: idle };
});

const { emptyDraftText, plainReadinessError, publishQuestion, ReadinessPanel } = await import("./draftPanel");

const tx = (k: string, o?: Record<string, unknown>) => i18n.t(k as never, { ns: "onboard", ...o } as never) as unknown as string;

const task = (id: string, held: boolean) => ({
	id,
	origin: { source: "faq.csv", ref: "row 2" },
	title: id,
	instruction: id,
	checks: [],
	controls: [],
	gates: [],
	held_out: held,
	review: "accepted" as const,
	readiness: { admitted: true, checks: [] },
});

const set: OnboardDraftSet = {
	id: "d1",
	suite: "faq",
	new_suite: true,
	title: "faq",
	kind: "rows",
	source: "rows",
	input: { upload: "onboard/x/faq.csv" },
	plan: { source: "rows", suite: "faq", held_out: { pct: 0, seed: 1 } },
	grader: "answer_match",
	public: false,
	problems: [],
	tasks: [task("a", true), task("b", false)],
};

describe("draft panel wave 4 minors", () => {
	it("says why a draft is empty (p10-5)", () => {
		const none = { ...set, tasks: [] };
		expect(emptyDraftText(tx, none)).toBe("No task is left in this draft. Draft again from step 2 to start over.");
		expect(emptyDraftText(tx, { ...none, problems: [{ ref: "row 2", message: "x" }] })).toBe(
			"No task was drafted. The problems below say why.",
		);
	});

	it("uses the singular for one held-out task (p10-9a)", () => {
		expect(publishQuestion(set, false, tx)).toBe("Add 2 tasks to faq? 1 of them is held out (private, never in shared results).");
		const two = { ...set, tasks: [task("a", true), task("b", true)] };
		expect(publishQuestion(two, false, tx)).toBe("Add 2 tasks to faq? 2 of them are held out (private, never in shared results).");
	});

	it("asks for consent on a judge-graded draft (p10-6)", () => {
		const judged = {
			...set,
			grader: "judge",
			plan: { ...set.plan, grader: "judge", judge: { provider: "openai", model: "gpt-4.1-mini" } },
		};
		const html = renderToStaticMarkup(<ReadinessPanel set={judged} />);
		expect(html).toContain('data-testid="onboard-ready-consent"');
		expect(html).toContain("I agree: the readiness checks call my judge model openai/gpt-4.1-mini on my own model key.");
		expect(html).toContain("Tick the box above to agree to the judge calls.");
		expect(renderToStaticMarkup(<ReadinessPanel set={set} />)).not.toContain("onboard-ready-consent");
	});

	it("strips the job parameter prefix from the readiness error (p10-6)", () => {
		expect(plainReadinessError("invalid job parameters: The readiness checks call x")).toBe("The readiness checks call x");
	});
});