import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { Badge } from "./badge";
import { Card } from "./card";
import { CategoryIcon, categoryStyle, normalizeCategory } from "./categoryIcon";
import { DetailsDisclosure } from "./detailsDisclosure";
import { EmptyState } from "./emptyState";
import { KeyValueList } from "./keyValueList";
import { PageHeader, Section } from "./section";
import { StatCard } from "./statCard";
import { StatusPill } from "./statusPill";
import { FilterChips, Toolbar } from "./toolbar";

describe("StatusPill", () => {
	it("renders tone, dot and label; running pulses", () => {
		const html = renderToStaticMarkup(<StatusPill tone="running">Running</StatusPill>);
		expect(html).toContain('data-tone="running"');
		expect(html).toContain("animate-ping");
		expect(html).toContain("Running");
	});
	it("ok tone has no pulse and dot can be hidden", () => {
		expect(renderToStaticMarkup(<StatusPill tone="ok">Ready</StatusPill>)).not.toContain("animate-ping");
		expect(renderToStaticMarkup(<StatusPill dot={false}>x</StatusPill>)).not.toContain("status-dot");
	});
});

describe("CategoryIcon", () => {
	it("normalizes aliases and spellings", () => {
		expect(normalizeCategory("Computer Use")).toBe("computer_use");
		expect(normalizeCategory("computer-use")).toBe("computer_use");
		expect(normalizeCategory("mcp")).toBe("tool_use");
		expect(normalizeCategory("multi_agent")).toBe("team");
	});
	it("maps known categories to distinct icons and unknown to a fallback", () => {
		const keys = [
			"coding",
			"terminal",
			"memory",
			"computer_use",
			"browser",
			"voice",
			"support",
			"tool_use",
			"research",
			"safety",
			"team",
			"gpu",
		];
		const icons = new Set(keys.map((k) => categoryStyle(k).icon));
		expect(icons.size).toBe(keys.length);
		expect(icons.has(categoryStyle("weird").icon)).toBe(false);
	});
	it("is decorative unless labelled", () => {
		expect(renderToStaticMarkup(<CategoryIcon category="gpu" />)).toContain('aria-hidden="true"');
		expect(renderToStaticMarkup(<CategoryIcon category="gpu" label="GPU" />)).toContain('aria-label="GPU"');
	});
});

describe("layout primitives", () => {
	it("StatCard shows value, hint and trend direction", () => {
		const html = renderToStaticMarkup(<StatCard label="Runs" value={42} hint="last 7 days" trend={{ value: "+5%", direction: "up" }} />);
		expect(html).toContain("42");
		expect(html).toContain("last 7 days");
		expect(html).toContain('data-direction="up"');
		expect(html).toContain("text-chart-success-ink");
	});
	it("Section and PageHeader render title, description and actions", () => {
		const html = renderToStaticMarkup(
			<>
				<PageHeader title="Runs" subtitle="Every agent run" actions={<button type="button">New</button>} />
				<Section title="Recent" description="Last ten" testId="s">
					body
				</Section>
			</>,
		);
		expect(html).toContain("<h1");
		expect(html).toContain("Every agent run");
		expect(html).toContain('data-testid="s"');
		expect(html).toContain("Last ten");
	});
	it("DetailsDisclosure is collapsed with a default Details label", () => {
		const html = renderToStaticMarkup(<DetailsDisclosure>secret hash</DetailsDisclosure>);
		expect(html).toContain("<details");
		expect(html).not.toContain(" open");
		expect(html).toContain("Details");
	});
	it("EmptyState and KeyValueList render content", () => {
		expect(renderToStaticMarkup(<EmptyState title="No runs yet" description="Start one." />)).toContain("No runs yet");
		const kv = renderToStaticMarkup(<KeyValueList items={[{ label: "Digest", value: "sha256:abc", mono: true }]} />);
		expect(kv).toContain("<dt");
		expect(kv).toContain("font-mono");
	});
	it("FilterChips marks pressed chips and offers Clear", () => {
		const html = renderToStaticMarkup(
			<Toolbar>
				<FilterChips
					options={[
						{ value: "a", label: "A", count: 3 },
						{ value: "b", label: "B" },
					]}
					value={["a"]}
					onChange={() => undefined}
					testId="f"
				/>
			</Toolbar>,
		);
		expect(html).toContain('data-testid="f-a"');
		expect(html).toMatch(/aria-pressed="true"[^>]*data-testid="f-a"/);
		expect(html).toContain("f-clear");
	});
});

describe("restyled primitives keep their API", () => {
	it("Badge keeps variants and adds pill shape", () => {
		const html = renderToStaticMarkup(<Badge variant="success">ok</Badge>);
		expect(html).toContain("rounded-full");
		expect(html).toContain('data-slot="badge"');
	});
	it("Card lifts only when interactive", () => {
		expect(renderToStaticMarkup(<Card />)).not.toContain("hover:-translate-y-0.5");
		expect(renderToStaticMarkup(<Card interactive />)).toContain("hover:-translate-y-0.5");
	});
});