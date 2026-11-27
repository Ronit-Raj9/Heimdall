import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { GradingBadge, JudgedNotice, OutputBadge, TrackBadge } from "./scenarioBadges";

describe("TrackBadge", () => {
	it("names the artifact track with what it means", () => {
		const html = renderToStaticMarkup(<TrackBadge track="artifact" />);
		expect(html).toContain(">Artifacts<");
		expect(html).toContain("graded on content");
	});

	it("names a known track with what it means", () => {
		const html = renderToStaticMarkup(<TrackBadge track="tool_use" testId="t" />);
		expect(html).toContain(">Tool use<");
		expect(html).toContain('title="Call tools on a service. Calls or state are checked."');
	});

	it("shows a track this UI does not know as sent", () => {
		const html = renderToStaticMarkup(<TrackBadge track="robotics" />);
		expect(html).toContain(">robotics<");
		expect(html).toContain("Unknown track.");
	});
});

describe("GradingBadge", () => {
	it("marks a judged suite in the warning tone and says a model decides", () => {
		const html = renderToStaticMarkup(<GradingBadge grading="judged" testId="g" />);
		expect(html).toMatch(/data-testid="g" data-tone="warn"/);
		expect(html).toContain(">Judged<");
		expect(html).toContain("A pinned judge model decides");
		expect(html).toContain("Never compared with verified");
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

	it("names the artifact kind with what the verifier reads", () => {
		const html = renderToStaticMarkup(<OutputBadge output="artifact" />);
		expect(html).toContain(">Artifacts<");
		expect(html).toContain("files the agent left");
	});
});

describe("JudgedNotice", () => {
	it("explains a judged suite in plain words as a note", () => {
		const html = renderToStaticMarkup(<JudgedNotice testId="n" />);
		expect(html).toContain('role="note"');
		expect(html).toContain("Judged");
		expect(html).toContain("Model, prompt and votes are pinned");
		expect(html).toContain("Never compared with verified benchmarks");
	});

	it("takes its own title and body", () => {
		const html = renderToStaticMarkup(<JudgedNotice testId="n" title="T" body="B" />);
		expect(html).toContain(">T<");
		expect(html).toContain(">B<");
	});
});