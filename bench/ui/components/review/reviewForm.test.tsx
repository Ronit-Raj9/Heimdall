import "@/lib/i18n";

import { demoSession, hostedSession } from "@/lib/testing/platformFixtures";
import { render } from "@/lib/testing/render";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { ReviewForm, type ReviewFormProps } from "./reviewForm";
import { passedRunReviews } from "./testFixtures";

const base: ReviewFormProps = { runId: "r_fmw1z7ouim", expId: "e_synthetic-codewins", mode: "live", onSubmit: () => {} };

/** The opening tag of the element with the test id. */
function tag(html: string, testId: string): string {
	const match = html.match(new RegExp(`<[a-z]+[^>]*data-testid="${testId}"[^>]*>`));
	expect(match, testId).not.toBeNull();
	return match![0];
}

describe("ReviewForm", () => {
	it("offers the four decisions and says the reviewer name is self-declared", () => {
		const html = renderToStaticMarkup(<ReviewForm {...base} />);
		for (const d of ["pass", "fail", "invalid", "flag_only"]) expect(html).toContain(`data-testid="review-decision-${d}"`);
		expect(html).toContain("There is no login");
		expect(tag(html, "review-submit")).not.toContain('disabled=""');
		expect(html).not.toContain('data-testid="review-form-read-only"');
	});

	it("shows every missing field after a submit attempt", () => {
		const html = renderToStaticMarkup(<ReviewForm {...base} showErrors />);
		expect(html).toContain('data-testid="review-error-decision"');
		expect(html).toContain('data-testid="review-error-reason"');
		expect(html).toContain('data-testid="review-error-reviewer"');
		expect(tag(html, "review-reason-input")).toContain('aria-invalid="true"');
	});

	it("shows no error once the required fields are filled", () => {
		const html = renderToStaticMarkup(
			<ReviewForm {...base} showErrors initialValues={{ decision: "pass", reason: "verifier too strict", reviewer: "maya" }} />,
		);
		expect(html).not.toContain("review-error-");
		expect(tag(html, "review-reason-input")).not.toContain('aria-invalid="');
	});

	for (const mode of ["recorded", "synthetic"] as const) {
		it(`is read-only in ${mode} mode`, () => {
			const html = renderToStaticMarkup(<ReviewForm {...base} mode={mode} />);
			expect(html).toContain('data-testid="review-form-read-only"');
			expect(html).toContain(`runs in ${mode} mode`);
			expect(tag(html, "review-submit")).toContain('disabled=""');
			expect(tag(html, "review-reason-input")).toContain('disabled=""');
		});
	}

	it("shows the server error and the created review", () => {
		const html = renderToStaticMarkup(<ReviewForm {...base} submitError="run r_x is not sealed" created={passedRunReviews.reviews[0]} />);
		expect(html).toContain("run r_x is not sealed");
		expect(html).toContain("Review rv_synth0002 recorded.");
	});
});
describe("ReviewForm in hosted mode", () => {
	it("has no write-token box and starts with the signed-in user as reviewer", () => {
		const html = render(<ReviewForm {...base} />, hostedSession("member"));
		expect(html).toContain('data-testid="review-form"');
		expect(html).not.toContain("review-token-input");
		expect(html).not.toContain("There is no login");
		expect(tag(html, "review-reviewer-input")).toContain('value="Ada Lovelace"');
	});
	it("keeps the token box in local mode", () => {
		expect(renderToStaticMarkup(<ReviewForm {...base} />)).toContain("review-token-input");
	});
	it("tells a viewer the role needed and shows a demo visitor no form", () => {
		expect(render(<ReviewForm {...base} />, hostedSession("viewer"))).toContain('data-testid="review-form-role"');
		expect(render(<ReviewForm {...base} />, demoSession())).toBe("");
	});
});