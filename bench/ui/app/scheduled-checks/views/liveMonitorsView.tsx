import { useConfirm } from "@/components/platform/confirmDialog";
import { PageHeader } from "@/components/shell";
import { BlockSkeleton, EmptyNote, LoadError, Section } from "@/components/runs/screenStates";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
	useCreateLiveMonitorMutation,
	useGetLiveMonitorQuery,
	useGetLiveMonitorsQuery,
	useMonitorActionMutation,
	useSetMonitorBadgeMutation,
	type MonitorDetailResponse,
	type MonitorIncident,
	type MonitorRow,
} from "@/lib/store/apis/liveMonitorsApi";
import "@/lib/types/liveMonitorsI18n";
import { Link } from "@tanstack/react-router";
import { parseAsString, useQueryState } from "nuqs";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { LineChart, samplePassRate } from "./liveMonitorChart";

const pct = (v: number | null | undefined) => (v === null || v === undefined ? "-" : `${Math.round(v * 100)}%`);
const ms = (v: number | null | undefined) => (v === null || v === undefined ? "-" : `${Math.round(v)}`);
const usd = (v: number) => `$${v.toFixed(v < 1 ? 3 : 2)}`;
const when = (s?: string) => (s ? new Date(s).toLocaleString() : "-");

/** Live monitors: /scheduled-checks?tab=live&monitor=. ?agent= opens the new-monitor form for that agent. */
export default function LiveMonitorsView() {
	const { t } = useTranslation("liveMonitors");
	const [monitor, setMonitor] = useQueryState("monitor", parseAsString);
	const [agent, setAgent] = useQueryState("agent", parseAsString);
	const [formOpen, setFormOpen] = useState(false);
	const list = useGetLiveMonitorsQuery(undefined, { pollingInterval: 60_000 });
	const data = list.data;
	const open = monitor ?? data?.monitors[0]?.id ?? null;
	const showForm = formOpen || Boolean(agent);
	const close = () => {
		setFormOpen(false);
		void setAgent(null);
	};
	return (
		<section className="flex min-w-0 flex-col gap-6" data-testid="live-monitors-screen">
			<PageHeader
				title={t("title")}
				subtitle={t("subtitle")}
				actions={
					!showForm ? (
						<Button size="sm" onClick={() => setFormOpen(true)} data-testid="live-monitor-new">
							{t("new")}
						</Button>
					) : undefined
				}
			/>
			{data && !data.ticker && <EmptyNote testId="live-monitors-no-ticker">{t("noTicker")}</EmptyNote>}
			{showForm && (
				<NewMonitorForm
					agent={agent ?? ""}
					onClose={close}
					onCreated={(id) => {
						close();
						void setMonitor(id);
					}}
				/>
			)}
			{list.isLoading ? (
				<BlockSkeleton rows={4} testId="live-monitors-loading" />
			) : list.error || !data ? (
				<LoadError title={t("loadError")} error={list.error} onRetry={() => void list.refetch()} testId="live-monitors-error" />
			) : data.monitors.length === 0 ? (
				!showForm && (
					<EmptyNote testId="live-monitors-empty">
						<strong>{t("emptyTitle")}</strong> {t("emptyMessage")}
					</EmptyNote>
				)
			) : (
				<>
					<MonitorTable rows={data.monitors} selected={open} onSelect={(id) => void setMonitor(id)} />
					{open && <MonitorPanel key={open} id={open} />}
				</>
			)}
		</section>
	);
}

export function MonitorTable({ rows, selected, onSelect }: { rows: MonitorRow[]; selected: string | null; onSelect: (id: string) => void }) {
	const { t } = useTranslation("liveMonitors");
	return (
		<Section title={t("list.title")} testId="live-monitors-list">
			<div className="overflow-x-auto">
				<table className="w-full text-sm">
					<thead className="text-muted-foreground text-left text-xs">
						<tr>
							<th className="py-1 pr-3">{t("form.name")}</th>
							<th className="py-1 pr-3">{t("list.agent")}</th>
							<th className="py-1 pr-3">{t("list.status")}</th>
							<th className="py-1 pr-3">{t("list.pass")}</th>
							<th className="py-1 pr-3">{t("list.latency")}</th>
							<th className="py-1 pr-3">{t("list.spent")}</th>
							<th className="py-1 pr-3">{t("list.next")}</th>
						</tr>
					</thead>
					<tbody>
						{rows.map((r) => (
							<tr
								key={r.id}
								className={`cursor-pointer border-t ${r.id === selected ? "bg-muted" : ""}`}
								onClick={() => onSelect(r.id)}
								data-testid={`live-monitor-row-${r.id}`}
							>
								<td className="py-1 pr-3 font-medium">{r.name}</td>
								<td className="py-1 pr-3">{r.agent}</td>
								<td className="py-1 pr-3" data-testid={`live-monitor-status-${r.id}`}>
									{t(`status.${r.status}` as "status.healthy", { defaultValue: r.status })}
								</td>
								<td className="py-1 pr-3">{pct(r.day.pass_rate)}</td>
								<td className="py-1 pr-3">
									{ms(r.day.p50_ms)} / {ms(r.day.p95_ms)}
								</td>
								<td className="py-1 pr-3">
									{usd(r.spent_today_usd)} / {usd(r.budget_usd_per_day)}
								</td>
								<td className="py-1 pr-3">{r.paused ? "-" : when(r.next)}</td>
							</tr>
						))}
					</tbody>
				</table>
			</div>
		</Section>
	);
}

function NewMonitorForm({ agent, onClose, onCreated }: { agent: string; onClose: () => void; onCreated: (id: string) => void }) {
	const { t } = useTranslation("liveMonitors");
	const [create, state] = useCreateLiveMonitorMutation();
	const [f, setF] = useState({ name: "", agent, benchmark: "", tasks: "3", interval: "60", budget: "" });
	const [err, setErr] = useState("");
	const field = (k: keyof typeof f, label: string, type = "text") => (
		<label className="flex flex-col gap-1 text-xs">
			{label}
			<Input type={type} value={f[k]} onChange={(e) => setF({ ...f, [k]: e.target.value })} data-testid={`live-monitor-form-${k}`} />
		</label>
	);
	const submit = async () => {
		setErr("");
		const res = await create({
			name: f.name || undefined,
			agent: f.agent,
			benchmark: f.benchmark || undefined,
			tasks: Number(f.tasks) || undefined,
			interval_min: Number(f.interval) || undefined,
			budget_usd_per_day: f.budget ? Number(f.budget) : undefined,
		});
		if ("error" in res) {
			const e = res.error as { data?: { error?: string; message?: string } };
			setErr(e?.data?.error ?? e?.data?.message ?? t("form.error"));
			return;
		}
		onCreated(res.data.id);
	};
	return (
		<Section title={t("new")} testId="live-monitor-form">
			<div className="grid gap-3 sm:grid-cols-3">
				{field("name", t("form.name"))}
				{field("agent", t("form.agent"))}
				{field("benchmark", t("form.benchmark"))}
				{field("tasks", t("form.tasks"), "number")}
				{field("interval", t("form.interval"), "number")}
				{field("budget", t("form.budget"), "number")}
			</div>
			{err && (
				<p className="text-chart-error-ink text-xs" data-testid="live-monitor-form-error">
					{err}
				</p>
			)}
			<div className="flex gap-2">
				<Button size="sm" disabled={!f.agent || state.isLoading} onClick={() => void submit()} data-testid="live-monitor-form-create">
					{t("form.create")}
				</Button>
				<Button size="sm" variant="ghost" onClick={onClose}>
					{t("form.cancel")}
				</Button>
			</div>
		</Section>
	);
}

function MonitorPanel({ id }: { id: string }) {
	const { t } = useTranslation("liveMonitors");
	const detail = useGetLiveMonitorQuery(id, { pollingInterval: 60_000 });
	if (detail.isLoading) return <BlockSkeleton rows={6} testId="live-monitor-loading" />;
	if (detail.error || !detail.data) {
		return <LoadError title={t("detailError")} error={detail.error} onRetry={() => void detail.refetch()} testId="live-monitor-error" />;
	}
	return <MonitorDetailBody d={detail.data} />;
}

export function MonitorDetailBody({ d }: { d: MonitorDetailResponse }) {
	const { t } = useTranslation("liveMonitors");
	const s = d.samples;
	return (
		<div className="flex min-w-0 flex-col gap-6" data-testid="live-monitor-detail">
			<Section title={d.name} description={d.problem || `${d.agent} · ${d.benchmark} · ${d.schedule}`} actions={<MonitorActions d={d} />} testId="live-monitor-summary">
				<p className="text-muted-foreground text-xs">
					{t("detail.tasks")}: {d.tasks.join(", ") || "-"}
					{d.tasks_note ? ` (${d.tasks_note})` : ""}
				</p>
				{d.badge && d.badge_path && (
					<p className="text-xs">
						{t("detail.badge")}: <img src={d.badge_path} alt={d.status} className="inline h-5" />
					</p>
				)}
			</Section>
			<Section title={t("detail.charts")} testId="live-monitor-charts">
				{s.length === 0 ? (
					<EmptyNote testId="live-monitor-no-samples">{t("detail.noSamples")}</EmptyNote>
				) : (
					<div className="grid gap-3 md:grid-cols-2">
						<LineChart title={t("detail.passRate")} series={[{ label: "", values: s.map(samplePassRate) }]} max={1} format={pct} testId="live-monitor-chart-pass" />
						<LineChart
							title={t("detail.latency")}
							series={[
								{ label: "p50", values: s.map((x) => x.p50_ms ?? null) },
								{ label: "p95", values: s.map((x) => x.p95_ms ?? null), dashed: true },
							]}
							format={ms}
							testId="live-monitor-chart-latency"
						/>
						<LineChart title={t("detail.cost")} series={[{ label: "", values: s.map((x) => x.cost_usd) }]} format={usd} testId="live-monitor-chart-cost" />
						<LineChart title={t("detail.errors")} series={[{ label: "", values: s.map((x) => x.errors) }]} format={(v) => String(v)} testId="live-monitor-chart-errors" />
					</div>
				)}
			</Section>
			<Section title={t("detail.incidents")} testId="live-monitor-incidents">
				{d.incidents.length === 0 ? <EmptyNote testId="live-monitor-no-incidents">{t("detail.noIncidents")}</EmptyNote> : d.incidents.map((i) => <IncidentCard key={i.id} i={i} />)}
			</Section>
		</div>
	);
}

function IncidentCard({ i }: { i: MonitorIncident }) {
	const { t } = useTranslation("liveMonitors");
	return (
		<article className="flex flex-col gap-1 rounded-md border p-3 text-sm" data-testid={`live-monitor-incident-${i.id}`}>
			<div className="flex flex-wrap items-baseline gap-2">
				<span className="font-medium">{i.title}</span>
				<span className="text-muted-foreground text-xs">
					{i.kind} · {i.state} · {t("detail.opened", { at: when(i.opened_at) })}
					{i.resolved_at ? ` · ${t("detail.resolved", { at: when(i.resolved_at) })}` : ""}
				</span>
			</div>
			<p className="text-muted-foreground text-xs">{i.detail}</p>
			{i.failing_runs.length > 0 && (
				<p className="flex flex-wrap gap-2 text-xs">
					{t("detail.failingRuns")}:
					{i.failing_runs.slice(0, 10).map((r) => (
						<Link key={r.run_id} to="/runs/$runId" params={{ runId: r.run_id }} className="text-primary underline-offset-4 hover:underline">
							{r.task}
						</Link>
					))}
				</p>
			)}
		</article>
	);
}

function MonitorActions({ d }: { d: MonitorDetailResponse }) {
	const { t } = useTranslation("liveMonitors");
	const confirm = useConfirm();
	const [act, state] = useMonitorActionMutation();
	const [badge] = useSetMonitorBadgeMutation();
	return (
		<span className="flex flex-wrap items-center gap-2" data-testid="live-monitor-actions">
			<Button size="sm" variant="outline" disabled={state.isLoading} onClick={() => void act({ id: d.id, action: "run" })} data-testid="live-monitor-run">
				{t("actions.run")}
			</Button>
			<Button size="sm" variant="outline" disabled={state.isLoading} onClick={() => void act({ id: d.id, action: d.paused ? "resume" : "pause" })} data-testid="live-monitor-pause">
				{d.paused ? t("actions.resume") : t("actions.pause")}
			</Button>
			<Button size="sm" variant="ghost" onClick={() => void badge({ id: d.id, enabled: !d.badge })} data-testid="live-monitor-badge">
				{d.badge ? t("detail.badgeOff") : t("detail.badgeOn")}
			</Button>
			<Button
				size="sm"
				variant="ghost"
				disabled={state.isLoading}
				onClick={async () => {
					if (await confirm(t("actions.deleteConfirm", { name: d.name }))) void act({ id: d.id, action: "delete" });
				}}
				data-testid="live-monitor-delete"
			>
				{t("actions.delete")}
			</Button>
		</span>
	);
}
