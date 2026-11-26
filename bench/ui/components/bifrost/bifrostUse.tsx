import { BlockSkeleton, LoadError, Section } from "@/components/runs/screenStates";
import { useGetBifrostUseQuery, useGetEvaluationBifrostCacheQuery, useGetEvaluationBifrostUseQuery } from "@/lib/store/apis/bifrostUseApi";
import type { BifrostFeature, BifrostFeatureState, BifrostUse, EvaluationCacheAB } from "@/lib/types/bifrostUse";
import { cn } from "@/lib/utils";
import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";

declare module "@/lib/i18n/resources" {
	interface LaneResources {
		bifrostUse: typeof import("@/locales/en/bifrostUse.json");
	}
}

const STATE_TONE: Record<BifrostFeatureState, string> = {
	on: "bg-emerald-500/10 text-emerald-700 dark:text-emerald-400",
	available: "bg-sky-500/10 text-sky-700 dark:text-sky-400",
	off: "bg-muted text-muted-foreground",
	unknown: "bg-amber-500/10 text-amber-700 dark:text-amber-400",
};

/** Short tab status: how many features are in use. */
export function bifrostUseStatus(u: BifrostUse | undefined): { tone: "ok" | "warn" | "idle"; count: number } {
	if (!u || !u.gateway_configured) return { tone: "idle", count: 0 };
	const count = u.features.filter((f) => f.state === "on").length;
	return { tone: u.reachable ? "ok" : "warn", count };
}

/** One row per feature: name, state, what Heimdall does with it, docs link. Pure. */
export function BifrostFeatureList({ features, testId }: { features: BifrostFeature[]; testId: string }) {
	const { t } = useTranslation("bifrostUse");
	return (
		<ul className="flex flex-col divide-y rounded-xl border" data-testid={testId}>
			{features.map((f) => (
				<li key={f.id} className="flex flex-col gap-1 p-3 text-sm" data-testid={`bifrost-feature-${f.id}`} data-state={f.state}>
					<div className="flex flex-wrap items-center gap-2">
						<span className="font-medium">{f.name}</span>
						<span className={cn("rounded-full px-2 py-0.5 text-xs", STATE_TONE[f.state] ?? STATE_TONE.off)}>{t(`state.${f.state}`)}</span>
						<span className="ml-auto flex gap-3 text-xs">
							{f.code && <code className="text-muted-foreground">{f.code}</code>}
							<a className="text-primary underline-offset-2 hover:underline" href={f.docs} target="_blank" rel="noreferrer">
								{t("docs")}
							</a>
						</span>
					</div>
					<p className="text-muted-foreground">{f.detail}</p>
				</li>
			))}
		</ul>
	);
}

/** The gateway line: host, reachability and loaded plugins. Pure. */
export function BifrostGatewayLine({ use }: { use: BifrostUse }) {
	const { t } = useTranslation("bifrostUse");
	if (!use.gateway_configured) {
		return (
			<p className="text-muted-foreground text-sm" data-testid="bifrost-no-gateway">
				{t("noGateway")}
			</p>
		);
	}
	return (
		<div className="flex flex-col gap-1 text-sm" data-testid="bifrost-gateway">
			<span>
				<span className="font-medium">{t("gateway")}:</span> {use.gateway_host}
			</span>
			{use.reachable ? (
				<span className="text-muted-foreground" data-testid="bifrost-plugins">
					{t("plugins")}: {use.loaded_plugins.length ? use.loaded_plugins.join(", ") : t("noPlugins")}
				</span>
			) : (
				<span className="text-amber-700 dark:text-amber-400" data-testid="bifrost-unreachable">
					{t("notReachable", { error: use.check_error ?? "" })}
				</span>
			)}
		</div>
	);
}

/** Settings > Integrations: the org's Bifrost features. */
export function BifrostUsePanel() {
	const { t } = useTranslation("bifrostUse");
	const { t: tg } = useTranslation("w4p6");
	const q = useGetBifrostUseQuery();
	if (q.isLoading) return <BlockSkeleton rows={4} testId="bifrost-use-loading" />;
	if (q.error || !q.data) return <LoadError title={t("loadError")} error={q.error} onRetry={() => void q.refetch()} testId="bifrost-use-error" />;
	return (
		<Section title={t("title")} description={t("orgDesc")} testId="bifrost-use-panel">
			<div className="flex flex-col gap-4">
				<BifrostGatewayLine use={q.data} />
				<BifrostFeatureList features={q.data.features} testId="bifrost-use-features" />
				<p className="text-muted-foreground text-xs">{t("routing")}</p>
				<Link to="/gateway" className="text-primary text-sm underline-offset-2 hover:underline" data-testid="bifrost-gateway-link">
					{tg("link")}
				</Link>
			</div>
		</Section>
	);
}

/** Evaluation page: what this evaluation uses. Hidden while loading or on error. */
export function EvaluationBifrostSection({ evaluationId }: { evaluationId: string }) {
	const { t } = useTranslation("bifrostUse");
	const q = useGetEvaluationBifrostUseQuery(evaluationId);
	if (!q.data) return null;
	return (
		<Section title={t("evalTitle")} description={t("evalDesc")} testId="evaluation-bifrost">
			<div className="flex flex-col gap-4">
				<BifrostGatewayLine use={q.data} />
				{q.data.uses.length ? (
					<BifrostFeatureList features={q.data.uses} testId="evaluation-bifrost-features" />
				) : (
					<p className="text-muted-foreground text-sm">{t("nothing")}</p>
				)}
				{q.data.uses.some((f) => f.id === "semantic_cache") && <EvaluationCacheABBlock evaluationId={evaluationId} />}
			</div>
		</Section>
	);
}

function EvaluationCacheABBlock({ evaluationId }: { evaluationId: string }) {
	const q = useGetEvaluationBifrostCacheQuery(evaluationId);
	if (!q.data || !q.data.enabled) return null;
	return <CacheABTable report={q.data} />;
}

const pct = (v: number) => `${Math.round(v * 100)}%`;
const signed = (v: number, unit: string, digits = 0) => `${v > 0 ? "+" : ""}${v.toFixed(digits)}${unit}`;

/** The cache on vs off report, one row per benchmark (bifrostfeatures.CacheAB). Pure. */
export function CacheABTable({ report }: { report: EvaluationCacheAB }) {
	const { t } = useTranslation("bifrostUse");
	return (
		<div className="flex flex-col gap-2" data-testid="evaluation-bifrost-cache">
			<h4 className="text-sm font-medium">{t("cache.title")}</h4>
			{report.children.length === 0 ? (
				<p className="text-muted-foreground text-sm">{t("cache.none")}</p>
			) : (
				<div className="overflow-x-auto">
					<table className="w-full text-sm">
						<thead className="text-muted-foreground text-left text-xs">
							<tr>
								<th className="py-1 pr-3 font-normal">{t("cache.benchmark")}</th>
								<th className="py-1 pr-3 font-normal">{t("cache.tasks")}</th>
								<th className="py-1 pr-3 font-normal">{t("cache.hitRate")}</th>
								<th className="py-1 pr-3 font-normal">{t("cache.latency")}</th>
								<th className="py-1 pr-3 font-normal">{t("cache.costSaved")}</th>
								<th className="py-1 font-normal">{t("cache.success")}</th>
							</tr>
						</thead>
						<tbody>
							{report.children.map((c) => (
								<tr key={c.experiment_id} className="border-t" data-testid={`evaluation-bifrost-cache-${c.benchmark}`} title={c.report.summary}>
									<td className="py-1 pr-3">{c.benchmark}</td>
									<td className="py-1 pr-3">{c.report.tasks}</td>
									<td className="py-1 pr-3">{pct(c.report.on.hit_rate)}</td>
									<td className="py-1 pr-3">{signed(c.report.latency_delta_ms, " ms")}</td>
									<td className="py-1 pr-3">${c.report.cost_saved_usd.toFixed(4)}</td>
									<td className="py-1">{signed(c.report.success_delta * 100, " pts")}</td>
								</tr>
							))}
						</tbody>
					</table>
				</div>
			)}
		</div>
	);
}
