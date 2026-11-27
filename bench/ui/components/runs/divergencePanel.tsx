import "./w441I18n";

import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { Card, CardContent } from "@/components/ui/card";
import { useGetPairDivergenceQuery, type PairDivergenceResponse, type SideAfter } from "@/lib/store/apis/behaviourApi";
import { formatUsd } from "@/lib/money";
import { useTranslation } from "react-i18next";

/** Dollars in the one money format (formatUsd), or the "not known" words. */
function costText(v: number | undefined, unknown: string): string {
	if (v === undefined || v === null) return unknown;
	return formatUsd(v);
}

/** The first point where the two runs of a pair did something different (lane I28). */
export function DivergenceView({ data }: { data: PairDivergenceResponse }) {
	const { t } = useTranslation("w441");
	const [armA, armB] = data.arms;
	const d = data.divergence;
	const result = (outcome: string) => (outcome === "pass" ? t("divergence.passed") : t("divergence.failed"));
	const rows: { label: string; value: (s: SideAfter) => string }[] = [
		{ label: t("divergence.row.turns"), value: (s) => String(s.turns) },
		{ label: t("divergence.row.retries"), value: (s) => String(s.retries) },
		{ label: t("divergence.row.testRuns"), value: (s) => String(s.test_runs) },
		{ label: t("divergence.row.edits"), value: (s) => String(s.edits) },
		{ label: t("divergence.row.errors"), value: (s) => String(s.errors) },
		{ label: t("divergence.row.cost"), value: (s) => costText(s.cost_usd, t("divergence.costUnknown")) },
	];
	const side = (arm: string, seq: number | undefined, text: string | undefined) =>
		seq ? t("divergence.side", { arm, seq, text: text ?? "" }) : t("divergence.sideEnded", { arm });
	return (
		<Card className="py-0 shadow-none" data-testid="divergence-panel">
			<CardContent className="flex flex-col gap-3 px-4 py-4 text-sm">
				<h3 className="font-medium">{t("divergence.title")}</h3>
				{!data.available || !d ? (
					<p className="text-muted-foreground" data-testid="divergence-unavailable">
						{data.reason}
					</p>
				) : !d.found ? (
					<p className="text-muted-foreground" data-testid="divergence-same">
						{t("divergence.same")}
					</p>
				) : (
					<>
						<p data-testid="divergence-category">{d.category ? t(`divergence.category.${d.category}`) : null}</p>
						<ul className="flex flex-col gap-1 font-mono text-xs">
							<li data-testid="divergence-side-a">{side(armA, d.seq_a, d.text_a)}</li>
							<li data-testid="divergence-side-b">{side(armB, d.seq_b, d.text_b)}</li>
						</ul>
						<table className="w-full max-w-md text-xs" data-testid="divergence-after">
							<caption className="text-muted-foreground mb-1 text-left">{t("divergence.after")}</caption>
							<thead>
								<tr>
									<th />
									<th className="text-left font-medium">{armA}</th>
									<th className="text-left font-medium">{armB}</th>
								</tr>
							</thead>
							<tbody>
								{rows.map((r) => (
									<tr key={r.label}>
										<td className="text-muted-foreground pr-4">{r.label}</td>
										<td>{r.value(d.after[0])}</td>
										<td>{r.value(d.after[1])}</td>
									</tr>
								))}
							</tbody>
						</table>
					</>
				)}
				<p data-testid="divergence-final">
					{t("divergence.final", { armA, armB, resultA: result(data.outcomes[0]), resultB: result(data.outcomes[1]) })}
				</p>
				<p className="text-muted-foreground text-xs">{t("divergence.note")}</p>
			</CardContent>
		</Card>
	);
}

/** Loads GET /pairs/{id}/divergence and shows it above the aligned lanes. */
export function DivergencePanel({ pairId }: { pairId: string }) {
	const { t } = useTranslation("w441");
	const q = useGetPairDivergenceQuery(pairId);
	if (q.isLoading) return <BlockSkeleton rows={3} testId="divergence-loading" />;
	if (q.error || !q.data)
		return <LoadError title={t("divergence.error")} error={q.error} onRetry={() => void q.refetch()} testId="divergence-error" />;
	return <DivergenceView data={q.data} />;
}