import i18n from "@/lib/i18n";
import type { Ratio, Verdict } from "@/lib/types/bench";
import { headline } from "./verdictSummary";
import "@/components/experiments/ufrcI18n";

export interface ComparisonSummaryInput {
	title: string;
	expId: string;
	suite?: string;
	verdict: Verdict;
	arms: readonly string[];
	naive: Record<string, Ratio>;
	names?: Record<string, string>;
	/** arm -> cost per run in USD, already formatted the way the page shows it. */
	costs?: Record<string, string | undefined>;
	/** The page's address, so the reader can open the full result. */
	url?: string;
	date: Date;
}

/**
 * A readable comparison report (Q-70) in Markdown, from what the page already has: the headline the
 * Result tab shows, each setup's solved count and cost, the benchmark, the date and the link. Pure
 * apart from the i18n lookups.
 */
export function comparisonSummaryMarkdown(i: ComparisonSummaryInput): string {
	const t = i18n.getFixedT("en", "ufrc");
	const tp = i18n.getFixedT("en", "experimentPage");
	const lines = [`# ${i.title}`, ""];
	lines.push(headline(i.verdict, i.arms, { names: i.names, naive: i.naive }, "en", tp as never), "");
	if (i.suite) lines.push(t("report.suite", { suite: i.suite }));
	lines.push(t("report.date", { date: i.date.toISOString().slice(0, 10) }), "");
	lines.push(`| ${t("report.setup")} | ${t("report.solved")} | ${t("report.cost")} |`, "| --- | --- | --- |");
	for (const arm of i.arms) {
		const r = i.naive[arm];
		const name = i.names?.[arm] && i.names[arm] !== arm ? `${arm} (${i.names[arm]})` : arm;
		lines.push(`| ${name} | ${r ? `${r.num} / ${r.den}` : "-"} | ${i.costs?.[arm] ?? "-"} |`);
	}
	lines.push("");
	if (i.url) lines.push(t("report.link", { url: i.url }));
	lines.push(t("report.id", { id: i.expId }), "");
	return lines.join("\n");
}