import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { GradingBadge, JudgedNotice, OutputBadge, TrackBadge } from "./scenarioBadges";

describe("TrackBadge", () => {
	it("names a known track with what it means", () => {
		const html = renderToStaticMarkup(<TrackBadge track="tool_use" testId="t" />);
		expect(html).toContain(">Tool use<");
		expect(html).toContain('title="Call tools against a service; the final state is checked."');
	});

	it("shows a track this UI does not know as sent", () => {
		const html = renderToStaticMarkup(<TrackBadge track="robotics" />);
		expect(html).toContain(">robotics<");
		expect(html).toContain("A track this version of the UI does not know.");
	});
});

describe("GradingBadge", () => {
	it("marks a judged suite in the warning tone and says a model decides", () => {
		const html = renderToStaticMarkup(<GradingBadge grading="judged" testId="g" />);
		expect(html).toMatch(/data-testid="g" data-tone="warn"/);
		expect(html).toContain(">Judged<");
		expect(html).toContain("A pinned judge model decides each outcome");
		expect(html).toContain("never compared with verified ones");
	});

	it("marks a verified suite as a deterministic check", () => {
		const html = renderToStaticMarkup(<GradingBadge grading="verified" testId="g" />);
		expect(html).toMatch(/data-tone="good"/);
		expect(html).toContain("No model grades.");
	});
});

describe("OutputBadge", () => {
	it("names diff, answer and state, and shows an unknown kind as sent", () => {
		expect(renderToStaticMarkup(<OutputBadge output="state" />)).toContain("files exported from the services");
		expect(renderToStaticMarkup(<OutputBadge output="answer" />)).toContain(">Answer<");
		expect(renderToStaticMarkup(<OutputBadge output="video" />)).toContain(">video<");
	});
});

describe("JudgedNotice", () => {
	it("explains a judged suite in plain words as a note", () => {
		const html = renderToStaticMarkup(<JudgedNotice testId="n" />);
		expect(html).toContain('role="note"');
		expect(html).toContain("Judged, not verified");
		expect(html).toContain("pinned in suite.yaml");
		expect(html).toContain("never ranked against, or compared with, a verified suite");
	});

	it("takes its own title and body", () => {
		const html = renderToStaticMarkup(<JudgedNotice testId="n" title="T" body="B" />);
		expect(html).toContain(">T<");
		expect(html).toContain(">B<");
	});
});