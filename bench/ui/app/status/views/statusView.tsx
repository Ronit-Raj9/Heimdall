import { Button } from "@/components/ui/button";
import { StatusDot, StatusPill, type StatusTone } from "@/components/ui/statusPill";
import { type Incident, type StatusLevel, type StatusPage, useGetStatusPageQuery } from "@/lib/store/apis/statusApi";
import "@/lib/types/statusI18n";
import { usePageTitle } from "@/lib/routing/pageTitle";
import { useTranslation } from "react-i18next";

/** How each level reads at a glance. */
export const levelTone: Record<StatusLevel, StatusTone> = {
	operational: "ok",
	maintenance: "running",
	degraded: "warn",
	partial_outage: "bad",
	major_outage: "bad",
	unknown: "neutral",
};

function when(iso?: string): string {
	if (!iso) return "";
	const d = new Date(iso);
	return Number.isNaN(d.getTime()) ? iso : d.toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" });
}

/** /status: the public status page. Polls every 30 s; the server caches its checks for 15 s. */
export default function StatusView() {
	const { t } = useTranslation("status");
	const q = useGetStatusPageQuery(undefined, { pollingInterval: 30_000, skipPollingIfUnfocused: true });

	usePageTitle(t("page.title"));

	if (q.isLoading) {
		return (
			<p className="text-muted-foreground text-sm" data-testid="status-loading">
				{t("page.loading")}
			</p>
		);
	}
	if (!q.data) {
		return (
			<div role="alert" className="flex flex-col items-start gap-3" data-testid="status-error">
				<p className="text-sm">{t("page.error")}</p>
				<Button size="sm" variant="outline" onClick={() => q.refetch()}>
					{t("page.retry")}
				</Button>
			</div>
		);
	}
	return <StatusPageView page={q.data} />;
}

/** The page body for one answer (tests render it directly). */
export function StatusPageView({ page }: { page: StatusPage }) {
	const { t } = useTranslation("status");
	const names = new Map(page.components.map((c) => [c.id, c.name]));
	const scheduled = page.incidents.filter((i) => i.status === "scheduled");
	const open = page.incidents.filter((i) => i.status !== "scheduled");
	return (
		<div className="flex flex-col gap-8" data-testid="status-page">
			<header className="flex flex-col gap-2">
				<h1 className="text-xl font-semibold">{t("page.heading")}</h1>
				<div className="bg-card flex items-center gap-3 rounded-xl border px-4 py-4" data-testid="status-overall" data-level={page.level}>
					<StatusDot tone={levelTone[page.level]} className="size-2.5" pulse={page.level !== "operational"} />
					<span className="text-base font-medium">{t(`overall.${page.level}`)}</span>
				</div>
				<p className="text-muted-foreground text-xs">
					{t("page.checkedAt", { time: when(page.checked_at) })} · {t("page.refreshNote")}
				</p>
			</header>

			{open.length > 0 && (
				<section className="flex flex-col gap-3" aria-labelledby="status-incidents">
					<h2 id="status-incidents" className="text-sm font-semibold">
						{t("sections.incidents")}
					</h2>
					{open.map((i) => (
						<IncidentCard key={i.id} incident={i} names={names} />
					))}
				</section>
			)}

			<section className="flex flex-col gap-3" aria-labelledby="status-components">
				<h2 id="status-components" className="text-sm font-semibold">
					{t("sections.components")}
				</h2>
				<ul className="bg-card divide-y rounded-xl border" data-testid="status-components">
					{page.components.map((c) => (
						<li key={c.id} className="flex flex-wrap items-center justify-between gap-2 px-4 py-3" data-testid={`status-component-${c.id}`}>
							<span className="flex flex-col">
								<span className="text-sm font-medium">{c.name}</span>
								{c.detail && <span className="text-muted-foreground text-xs">{c.detail}</span>}
							</span>
							<StatusPill tone={levelTone[c.level]}>{t(`level.${c.level}`)}</StatusPill>
						</li>
					))}
				</ul>
				{open.length === 0 && <p className="text-muted-foreground text-xs">{t("sections.noIncidents")}</p>}
			</section>

			{scheduled.length > 0 && (
				<section className="flex flex-col gap-3" aria-labelledby="status-scheduled">
					<h2 id="status-scheduled" className="text-sm font-semibold">
						{t("sections.scheduled")}
					</h2>
					{scheduled.map((i) => (
						<IncidentCard key={i.id} incident={i} names={names} />
					))}
				</section>
			)}

			<section className="flex flex-col gap-3" aria-labelledby="status-recent">
				<h2 id="status-recent" className="text-sm font-semibold">
					{t("sections.recent")}
				</h2>
				{page.recent.length === 0 ? (
					<p className="text-muted-foreground text-xs">{t("sections.noRecent")}</p>
				) : (
					page.recent.map((i) => <IncidentCard key={i.id} incident={i} names={names} />)
				)}
			</section>

			{page.version && <p className="text-muted-foreground text-xs">{t("page.version", { version: page.version })}</p>}
		</div>
	);
}

/** An incident a platform check opened on its own (fix wave 2, N148); `auto` is not in the shared type yet. */
type MaybeAutoIncident = Incident & { auto?: boolean };

function IncidentCard({ incident, names }: { incident: MaybeAutoIncident; names: Map<string, string> }) {
	const { t } = useTranslation("status");
	const affected = (incident.components ?? []).map((id) => names.get(id) ?? id);
	const tone: StatusTone =
		incident.status === "resolved"
			? "ok"
			: incident.status === "scheduled" || incident.impact === "maintenance"
				? "running"
				: incident.impact === "minor"
					? "warn"
					: "bad";
	const updates = [...incident.updates].reverse(); // newest first
	return (
		<article className="bg-card flex flex-col gap-3 rounded-xl border px-4 py-4" data-testid={`status-incident-${incident.id}`}>
			<div className="flex flex-wrap items-center justify-between gap-2">
				<h3 className="text-sm font-semibold">{incident.title}</h3>
				<span className="flex items-center gap-2">
					{incident.auto && (
						<span
							className="text-muted-foreground rounded-md border px-1.5 py-0.5 text-xs"
							title={t("incident.autoHint")}
							data-testid={`status-incident-auto-${incident.id}`}
						>
							{t("incident.auto")}
						</span>
					)}
					<StatusPill tone={tone}>{t(`incident.status.${incident.status}`)}</StatusPill>
				</span>
			</div>
			<p className="text-muted-foreground text-xs">
				{affected.length > 0 ? t("incident.affects", { components: affected.join(", ") }) : t("incident.wholePlatform")}
				{" · "}
				{incident.status === "scheduled" && incident.scheduled_for
					? t("incident.scheduledFor", { time: when(incident.scheduled_for) })
					: t("incident.started", { time: when(incident.started_at) })}
				{incident.resolved_at && <> · {t("incident.resolved", { time: when(incident.resolved_at) })}</>}
			</p>
			<ol className="flex flex-col gap-2 border-l pl-3">
				{updates.map((u, n) => (
					<li key={`${u.at}-${n}`} className="flex flex-col gap-0.5">
						<span className="text-xs font-medium">
							{t(`incident.status.${u.status}`)} <span className="text-muted-foreground font-normal">{when(u.at)}</span>
						</span>
						<span className="text-sm whitespace-pre-line">{u.message}</span>
					</li>
				))}
			</ol>
		</article>
	);
}