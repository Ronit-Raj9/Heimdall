import "@/lib/types/modelSwitchI18n";
import { ParetoChart } from "@/components/migration/paretoChart";
import { costText, formatShare, observed } from "@/components/migration/pareto.utils";
import { BlockSkeleton, LoadError, Section } from "@/components/runs/screenStates";
import { Button } from "@/components/ui/button";
import { getLocale } from "@/lib/i18n";
import { useGetModelSwitchConfigQuery, useGetModelSwitchQuery, type ModelSwitchReport, type SwitchGroup } from "@/lib/store/apis/modelSwitchApi";
import type { Value } from "@/lib/types/bench";
import { useTranslation } from "react-i18next";

/**
 * Model switch report (lane Z21): one agent on several models, quality against cost with ranges,
 * each model against the best, per benchmark and per segment, a plain recommendation, and the
 * Bifrost routing config built from the same runs. Every number comes from the server report; a
 * value no run measured reads unknown and is never drawn at zero.
 */
export function ModelSwitchReportView({ evaluationId }: { evaluationId: string }) {
	const { t } = useTranslation("modelSwitch");
	const q = useGetModelSwitchQuery(evaluationId);
	if (q.isLoading) return <BlockSkeleton testId="model-switch-loading" />;
	if (q.error || !q.data) return <LoadError title={t("report.loadError")} error={q.error} onRetry={q.refetch} testId="model-switch-error" />;
	return (
		<div className="flex flex-col gap-6" data-testid="model-switch-report">
			<Report report={q.data} />
			<RoutingExport evaluationId={evaluationId} />
		</div>
	);
}

function Report({ report }: { report: ModelSwitchReport }) {
	const { t } = useTranslation("modelSwitch");
	const locale = getLocale();
	const unknown = t("report.unknown");
	const share = (v: Value) => formatShare(observed(v), locale) ?? unknown;
	const usd = (v: Value) => costText(v, locale) ?? unknown;
	const ms = (v: Value) => (observed(v) === null ? unknown : `${Math.round(observed(v) as number)} ms`);
	const rec = report.recommendation;
	const vsBest = new Map(report.comparisons.map((c) => [c.label, c]));
	return (
		<>
			<Section title={t("report.recommendation")} description={t("report.description")} testId="model-switch-recommendation">
				<p className="text-sm font-medium" data-testid="model-switch-action">
					{t(`report.action.${rec.action}`)}
					{rec.model ? `: ${rec.model}` : ""} ({t(`report.strength.${rec.strength}`)})
				</p>
				<p className="text-sm">{rec.summary}</p>
				{rec.reasons.length > 0 && <TextList title={t("report.reasons")} items={rec.reasons} testId="model-switch-reasons" />}
				{rec.caveats.length > 0 && <TextList title={t("report.caveats")} items={rec.caveats} testId="model-switch-caveats" />}
			</Section>
			<Section title={t("report.models")} testId="model-switch-models">
				<div className="overflow-x-auto">
					<table className="w-full text-sm">
						<thead className="text-muted-foreground text-left">
							<tr>
								<th className="py-1 pr-3">{t("report.col.model")}</th>
								<th className="py-1 pr-3">{t("report.col.quality")}</th>
								<th className="py-1 pr-3">{t("report.col.costRun")}</th>
								<th className="py-1 pr-3">{t("report.col.costSuccess")}</th>
								<th className="py-1 pr-3">{t("report.col.latency")}</th>
								<th className="py-1 pr-3">{t("report.col.availability")}</th>
								<th className="py-1 pr-3">{t("report.col.runs")}</th>
								<th className="py-1">{t("report.col.vsBest")}</th>
							</tr>
						</thead>
						<tbody>
							{report.models.map((m) => {
								const c = vsBest.get(m.label);
								const range = m.quality.bounds ? ` (${formatShare(m.quality.bounds[0], locale)} to ${formatShare(m.quality.bounds[1], locale)})` : "";
								const tags = [
									m.label === report.current && t("report.current"),
									m.label === report.best && t("report.best"),
									m.label === rec.model && t("report.recommended"),
									m.open && t("report.open"),
								].filter(Boolean);
								return (
									<tr key={m.label} className="border-t" data-testid={`model-switch-row-${m.label}`}>
										<td className="py-1 pr-3 font-medium">
											{m.provider ? `${m.provider}/${m.model}` : m.model || m.label}
											{tags.length > 0 && <span className="text-muted-foreground font-normal"> ({tags.join(", ")})</span>}
										</td>
										<td className="py-1 pr-3">
											{share(m.quality)}
											{range}
										</td>
										<td className="py-1 pr-3">{usd(m.cost_per_run)}</td>
										<td className="py-1 pr-3">{usd(m.cost_per_success)}</td>
										<td className="py-1 pr-3">
											{ms(m.latency_p50_ms)} / {ms(m.latency_p95_ms)}
										</td>
										<td className="py-1 pr-3">{share(m.availability)}</td>
										<td className="py-1 pr-3">{m.runs}</td>
										<td className="py-1">{m.label === report.best ? t("report.isBest") : (c?.reason ?? unknown)}</td>
									</tr>
								);
							})}
						</tbody>
					</table>
				</div>
			</Section>
			<Section title={t("report.costChart")} description={report.pareto.note} testId="model-switch-pareto">
				<ParetoChart pareto={report.pareto} testId="model-switch-pareto-chart" />
			</Section>
			<Groups title={t("report.perBenchmark")} groups={report.benchmarks} testId="model-switch-benchmarks" share={share} />
			<Groups title={t("report.perSegment")} description={t("report.segmentsDescription")} groups={report.segments} testId="model-switch-segments" share={share} empty={t("report.noSegments")} />
			{report.missing.length > 0 && <TextList title={t("report.missing")} items={report.missing} testId="model-switch-missing" />}
			{report.notes.length > 0 && <TextList title={t("report.notes")} items={report.notes} testId="model-switch-notes" />}
			{report.practice_runs ? <p className="text-muted-foreground text-sm">{t("report.practice", { count: report.practice_runs })}</p> : null}
		</>
	);
}

function Groups({
	title,
	description,
	groups,
	testId,
	share,
	empty,
}: {
	title: string;
	description?: string;
	groups: SwitchGroup[];
	testId: string;
	share: (v: Value) => string;
	empty?: string;
}) {
	const { t } = useTranslation("modelSwitch");
	if (groups.length === 0 && !empty) return null;
	return (
		<Section title={title} description={description} testId={testId}>
			{groups.length === 0 ? (
				<p className="text-muted-foreground text-sm">{empty}</p>
			) : (
				<ul className="flex flex-col gap-2 text-sm">
					{groups.map((g) => (
						<li key={`${g.key}=${g.value}`} data-testid={`${testId}-${g.key}-${g.value}`}>
							<span className="font-medium">{g.key === "benchmark" ? g.value : `${g.key}: ${g.value}`}</span>{" "}
							<span className="text-muted-foreground">
								({t("report.tasks", { count: g.tasks })}
								{g.small ? `, ${t("report.small")}` : ""})
							</span>
							<div className="text-muted-foreground">
								{Object.entries(g.models)
									.map(([label, m]) => `${label} ${share(m.quality)}`)
									.join(", ")}
								{g.best ? `. ${t("report.best")}: ${g.best}` : ""}
								{g.vs_default ? `. ${g.vs_default.reason}` : ""}
								{g.note ? `. ${g.note}` : ""}
							</div>
						</li>
					))}
				</ul>
			)}
		</Section>
	);
}

function RoutingExport({ evaluationId }: { evaluationId: string }) {
	const { t } = useTranslation("modelSwitch");
	const q = useGetModelSwitchConfigQuery({ id: evaluationId, options: {} });
	if (q.isLoading) return <BlockSkeleton testId="model-switch-config-loading" />;
	if (q.error || !q.data) return <LoadError title={t("report.loadError")} error={q.error} onRetry={q.refetch} testId="model-switch-config-error" />;
	const ex = q.data;
	const json = JSON.stringify(ex.config, null, 2);
	const download = () => {
		const url = URL.createObjectURL(new Blob([json], { type: "application/json" }));
		const a = document.createElement("a");
		a.href = url;
		a.download = `bifrost-routing-${evaluationId}.json`;
		a.click();
		URL.revokeObjectURL(url);
	};
	return (
		<Section
			title={t("export.title")}
			description={t("export.description")}
			testId="model-switch-config"
			actions={
				ex.ready ? (
					<Button size="sm" variant="outline" onClick={download} dataTestId="model-switch-download">
						{t("export.download")}
					</Button>
				) : undefined
			}
		>
			{!ex.ready ? (
				<p className="text-muted-foreground text-sm">{t("export.notReady")}</p>
			) : (
				<>
					<ul className="flex flex-col gap-1 text-sm" data-testid="model-switch-rules">
						{ex.rules.map((r) => (
							<li key={r.name}>
								<span className="font-medium">{t(`export.kind.${r.kind}`)}</span> {r.name} ({r.enabled ? t("export.enabled") : t("export.disabled")},{" "}
								{t(`export.evidence.${r.evidence}`, { defaultValue: r.evidence })}): <span className="text-muted-foreground">{r.why}</span>
							</li>
						))}
					</ul>
					{ex.headers.length > 0 && (
						<TextList title={t("export.headers")} items={ex.headers.map((h) => `${h.header}: ${h.values.join(", ")}`)} testId="model-switch-headers" />
					)}
					{ex.budget_hints.length > 0 && <TextList title={t("export.budget")} items={ex.budget_hints.map((b) => b.text)} testId="model-switch-budget" />}
					<pre className="bg-muted max-h-80 overflow-auto rounded p-3 text-xs" data-testid="model-switch-json">
						{json}
					</pre>
				</>
			)}
			{ex.notes.length > 0 && <TextList title={t("report.notes")} items={ex.notes} testId="model-switch-config-notes" />}
		</Section>
	);
}

function TextList({ title, items, testId }: { title: string; items: string[]; testId: string }) {
	return (
		<div className="flex flex-col gap-1 text-sm" data-testid={testId}>
			<span className="font-medium">{title}</span>
			<ul className="text-muted-foreground list-disc pl-5">
				{items.map((s) => (
					<li key={s}>{s}</li>
				))}
			</ul>
		</div>
	);
}
