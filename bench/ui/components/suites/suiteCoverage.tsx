import { Section } from "@/components/runs/screenStates";
import type { SuiteProbe, SuiteTask } from "@/lib/types/bench";
import { AlertTriangle } from "lucide-react";
import { useTranslation } from "react-i18next";
import { ToneBadge } from "./suiteBadges";
import { capabilityCoverage, licenseSummary, probesByFamily } from "./suites.utils";

/** Tasks per capability as simple bars, plus how many tasks name none. */
export function CapabilityCoverage({ tasks }: { tasks: readonly SuiteTask[] }) {
	const { t } = useTranslation("bench");
	const { rows, missing } = capabilityCoverage(tasks);
	const max = Math.max(1, ...rows.map((row) => row.count));
	return (
		<Section title={t("suites.coverage.title")} description={t("suites.coverage.description")} testId="suites-coverage">
			{rows.length === 0 ? (
				<p className="text-muted-foreground text-xs" data-testid="suites-coverage-none">
					{t("suites.coverage.none")}
				</p>
			) : (
				<ul className="flex min-w-0 flex-col gap-1.5">
					{rows.map((row) => (
						<li
							key={row.name}
							className="grid min-w-0 grid-cols-[minmax(0,9rem)_1fr_2rem] items-center gap-2 text-xs"
							data-testid={`suites-coverage-${row.name}`}
						>
							<span className="text-foreground truncate font-mono" title={row.name}>
								{row.name}
							</span>
							<span
								role="img"
								aria-label={t("suites.coverage.label", { capability: row.name, count: row.count })}
								className="bg-chart-track h-2 min-w-0 rounded-full"
							>
								<span className="bg-chart-seq-3 block h-full rounded-full" style={{ width: `${(row.count / max) * 100}%` }} />
							</span>
							<span className="text-muted-foreground text-right tabular-nums">{row.count}</span>
						</li>
					))}
				</ul>
			)}
			{missing > 0 && rows.length > 0 && (
				<p className="text-muted-foreground text-xs" data-testid="suites-coverage-missing">
					{t("suites.coverage.missing", { count: missing })}
				</p>
			)}
		</Section>
	);
}

/** Tasks per license; tasks with none are flagged by id. */
export function LicenseSummary({ tasks }: { tasks: readonly SuiteTask[] }) {
	const { t } = useTranslation("bench");
	const { rows, missing } = licenseSummary(tasks);
	return (
		<Section title={t("suites.licenses.title")} description={t("suites.licenses.description")} testId="suites-licenses">
			{rows.length > 0 && (
				<ul className="flex flex-wrap gap-2">
					{rows.map((row) => (
						<li key={row.name} data-testid={`suites-license-${row.name}`}>
							<ToneBadge tone="neutral">
								<span className="font-mono">{row.name}</span>
								<span className="text-muted-foreground font-normal">{t("suites.licenses.count", { count: row.count })}</span>
							</ToneBadge>
						</li>
					))}
				</ul>
			)}
			{missing.length > 0 && (
				<p
					role="note"
					className="border-warning bg-warning-soft text-foreground flex items-start gap-2 rounded-sm border px-3 py-2 text-xs"
					data-testid="suites-license-missing"
				>
					<AlertTriangle className="text-warning-ink mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
					<span className="min-w-0 break-words">
						{t("suites.licenses.missingWarning", { count: missing.length, tasks: missing.join(", ") })}
					</span>
				</p>
			)}
		</Section>
	);
}

/** Probes grouped by the PS2 failure family they cover. */
/** M6-38: a probe id in plain words: "unavailable-model" reads "Unavailable model". Exported for tests. */
export function probeLabel(id: string): string {
	const words = id.replace(/[-_.]+/g, " ").trim();
	return words ? words[0].toUpperCase() + words.slice(1) : id;
}

export function ProbeList({ probes }: { probes: readonly SuiteProbe[] }) {
	const { t } = useTranslation("bench");
	const groups = probesByFamily(probes);
	return (
		<Section title={t("suites.probes.title")} description={t("suites.probes.description")} testId="suites-probes">
			{groups.length === 0 ? (
				<p className="text-muted-foreground text-xs">{t("suites.probes.none")}</p>
			) : (
				<div className="grid min-w-0 grid-cols-1 gap-3 lg:grid-cols-3">
					{groups.map((group) => (
						<section
							key={group.family}
							className="flex min-w-0 flex-col gap-2 rounded-sm border p-3"
							data-testid={`suites-probe-family-${group.family}`}
						>
							<h3 className="text-foreground flex items-center justify-between gap-2 text-xs font-semibold">
								<span>{familyLabel(group.family, t)}</span>
								<span className="text-muted-foreground font-normal tabular-nums">{group.probes.length}</span>
							</h3>
							<ul className="flex min-w-0 flex-col gap-2">
								{group.probes.map((probe) => (
									<li key={probe.id} className="flex min-w-0 flex-col gap-1" data-testid={`suites-probe-${probe.id}`}>
										{/* M6-38: a plain label; the probe id and axis are in the tooltip. */}
										<span className="flex flex-wrap items-center gap-1.5" title={`${probe.id} · ${t("suites.probes.axis", { axis: probe.axis })}`}>
											<span className="text-foreground text-xs font-medium">{probeLabel(probe.id)}</span>
											<ToneBadge tone="neutral" className="px-1.5 py-0 text-xs">
												{layerLabel(probe.layer, t)}
											</ToneBadge>
											{probe.goal_preserving && (
												<ToneBadge tone="good" className="px-1.5 py-0 text-xs" hint={t("suites.probes.goalPreservingHint")}>
													{t("suites.probes.goalPreserving")}
												</ToneBadge>
											)}
										</span>
										{probe.safe_behavior && <span className="text-muted-foreground text-xs">{probe.safe_behavior}</span>}
									</li>
								))}
							</ul>
						</section>
					))}
				</div>
			)}
		</Section>
	);
}

type T = ReturnType<typeof useTranslation<"bench">>["t"];

function familyLabel(family: string, t: T): string {
	switch (family) {
		case "unavailable_model":
		case "broken_tool":
		case "misleading_instruction":
		case "hostile_tool":
			return t(`suites.probes.family.${family}`);
		default:
			return family;
	}
}

function layerLabel(layer: string, t: T): string {
	switch (layer) {
		case "fixture":
		case "gateway":
		case "tool_shim":
		case "tool":
		case "service":
			return t(`suites.probes.layer.${layer}`);
		default:
			return layer;
	}
}