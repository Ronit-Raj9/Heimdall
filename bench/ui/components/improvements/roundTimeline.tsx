import { StatusPill } from "@/components/ui/statusPill";
import type { CandidateView, RoundView } from "@/lib/types/improve";
import { useTranslation } from "react-i18next";
import { points, versionLabel } from "./improve.utils";
import "./w45impI18n";

/** p with two significant digits (A2.12). */
export function formatP(p: number): string {
	return Number.isFinite(p) ? String(Number(p.toPrecision(2))) : "-";
}

/** How many ideas a round tried and how many reached the gate; an older round had one idea. */
export function roundCounts(r: RoundView): { ideas: number; gate: number } {
	const ideas = r.candidates && r.candidates.length > 0 ? r.candidates.length : 1;
	const tested = (r.commit !== undefined && !r.commit.skipped) || r.audit !== undefined || r.decision !== "";
	return { ideas, gate: tested ? 1 : 0 };
}

/** The timeline (A4.5 item 4): one row per round, each idea under it when expanded. */
export function RoundTimeline({ rounds }: { rounds: readonly RoundView[] }) {
	const { t } = useTranslation("w45imp");
	return (
		<ol className="flex flex-col gap-2" data-testid="improvement-timeline">
			{rounds.map((r) => {
				const { ideas, gate } = roundCounts(r);
				const decision =
					r.decision === "committed" ? t("timeline.kept") : r.decision === "rejected" ? t("timeline.notKept") : t("timeline.inProgress");
				const p = r.commit && !r.commit.skipped ? formatP(r.commit.p) : undefined;
				const row = p ? t("timeline.rowP", { n: r.n, ideas, gate, decision, p }) : t("timeline.row", { n: r.n, ideas, gate, decision });
				return (
					<li key={r.n} className="rounded-md border p-2 text-xs" data-testid={`improvement-timeline-${r.n}`}>
						<details>
							<summary className="text-foreground cursor-pointer">{row}</summary>
							{r.why && <p className="text-muted-foreground mt-1">{r.why}</p>}
							<ul className="mt-2 flex flex-col gap-1">
								{(r.candidates ?? []).map((c) => (
									<IdeaRow key={c.id} idea={c} winner={c.id === r.winner} />
								))}
							</ul>
						</details>
					</li>
				);
			})}
		</ol>
	);
}

function IdeaRow({ idea: c, winner }: { idea: CandidateView; winner: boolean }) {
	const { t } = useTranslation("w45imp");
	const source = t(`timeline.source.${c.kind}`, { version: versionLabel(c.source), defaultValue: c.kind });
	return (
		<li className="text-muted-foreground flex flex-wrap items-center gap-x-3 gap-y-1">
			<span className="text-foreground">{source}</span>
			{c.dev_diff !== undefined && <span className="tabular-nums">{t("timeline.practice", { v: points(c.dev_diff) })}</span>}
			{c.dropped && <span>{t("timeline.stopped", { why: c.dropped })}</span>}
			{winner && <StatusPill tone="ok">{t("timeline.winner")}</StatusPill>}
		</li>
	);
}