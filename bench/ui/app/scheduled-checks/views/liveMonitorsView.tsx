import { EditLiveMonitor } from "./editLiveMonitor";
import { useConfirm } from "@/components/platform/confirmDialog";
import { NativeSelect } from "@/components/ui/select";
import { PageHeader } from "@/components/shell";
import { BlockSkeleton, EmptyNote, InlineError, LoadError, Section } from "@/components/runs/screenStates";
import { PaidButton } from "@/components/ui/paidButton";
import { notify } from "@/lib/notify";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { useGetAgentsQuery } from "@/lib/store/apis/benchApi";
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
import type { AgentSummary } from "@/lib/types/bench";
import "@/lib/types/liveMonitorsI18n";
import { Link } from "@tanstack/react-router";
import { parseAsString, useQueryState } from "nuqs";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { LineChart, samplePassRate } from "./liveMonitorChart";
import { WhyDisabled } from "@/components/ui/whyDisabled";
import { usd as usd2 } from "@/components/canaries/howOften";
import "@/components/canaries/uf7bI18n";

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
	const list = useGetLiveMonitorsQuery(undefined, { pollingInterval: 60_000, skipPollingIfUnfocused: true });
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
				!showForm && <LiveMonitorsEmpty />
			) : (
				<>
					<MonitorTable rows={data.monitors} selected={open} onSelect={(id) => void setMonitor(id)} />
					{open && <MonitorPanel key={open} id={open} />}
				</>
			)}
		</section>
	);
}

export function MonitorTable({
	rows,
	selected,
	onSelect,
}: {
	rows: MonitorRow[];
	selected: string | null;
	onSelect: (id: string) => void;
}) {
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
								className={`hover:bg-muted/40 focus-visible:ring-(--focus) cursor-pointer border-t outline-none focus-visible:ring-2 ${r.id === selected ? "bg-muted" : ""}`}
								onClick={() => onSelect(r.id)}
								// M7-24: the row opens on Enter or Space too, so keyboard users can reach every monitor.
								tabIndex={0}
								role="link"
								aria-current={r.id === selected || undefined}
								onKeyDown={(e) => {
									if (e.key === "Enter" || e.key === " ") {
										e.preventDefault();
										onSelect(r.id);
									}
								}}
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

/** The empty list: what a monitor does, and where an endpoint agent comes from. */
export function LiveMonitorsEmpty() {
	const { t } = useTranslation("liveMonitors");
	return (
		<EmptyNote testId="live-monitors-empty">
			<strong>{t("emptyTitle")}</strong> {t("emptyMessage")}{" "}
			<Link to="/connect" className="text-primary font-medium underline-offset-4 hover:underline" data-testid="live-monitors-connect">
				{t("connectAgent")}
			</Link>
		</EmptyNote>
	);
}

/** Agents a monitor can watch: those with a remote endpoint (owns_model), by id. */
export function endpointAgents(agents: Pick<AgentSummary, "id" | "owns_model">[]): string[] {
	return agents
		.filter((a) => a.owns_model === true)
		.map((a) => a.id)
		.sort();
}

/** The agent field: a list of endpoint agents, or a link to Connect when there is none. */
export function MonitorAgentPicker({ value, agents, onChange }: { value: string; agents: string[]; onChange: (id: string) => void }) {
	const { t } = useTranslation("liveMonitors");
	const { t: m } = useTranslation("uf7b");
	// M7-54: an agent with no remote endpoint is never shown as some other agent picked; say why and point to drift monitors.
	const notEndpoint = value !== "" && !agents.includes(value);
	const notEndpointNote = notEndpoint ? (
		<span className="text-chart-warning-ink" data-testid="live-monitor-form-not-endpoint">
			{m("live.notEndpoint", { agent: value })}{" "}
			<Link
				to="/scheduled-checks"
				search={{ tab: "drift", agent: value } as never}
				className="text-primary font-medium underline-offset-4 hover:underline"
				data-testid="live-monitor-form-use-drift"
			>
				{m("live.useDrift")}
			</Link>
		</span>
	) : null;
	if (agents.length === 0) {
		return (
			<div className="flex flex-col gap-1 text-xs">
				{t("form.agent")}
				<span className="text-muted-foreground">
					{notEndpointNote ?? (
						<>
							{t("form.noEndpointAgents")}{" "}
						</>
					)}
					<Link
						to="/connect"
						className="text-primary font-medium underline-offset-4 hover:underline"
						data-testid="live-monitor-form-connect"
					>
						{t("connectAgent")}
					</Link>
				</span>
			</div>
		);
	}
	return (
		<label className="flex flex-col gap-1 text-xs">
			{t("form.agent")}
			<NativeSelect
				value={notEndpoint ? "" : value}
				onChange={(e) => onChange(e.target.value)}
				data-testid="live-monitor-form-agent"
			>
				<option value="">{t("form.pickAgent")}</option>
				{agents.map((id) => (
					<option key={id} value={id}>
						{id}
					</option>
				))}
			</NativeSelect>
			{notEndpointNote}
		</label>
	);
}

/** The live monitor form's fields, as typed. */
export interface LiveMonitorFields {
	name: string;
	agent: string;
	benchmark: string;
	tasks: string;
	interval: string;
	budget: string;
}

/** The uf7b keys of what the form still needs, by field (M7-53). Exported for tests. */
export function liveMonitorProblems(
	f: LiveMonitorFields,
	agents: string[],
	minInterval = 15,
): { field: keyof LiveMonitorFields; key: "live.problem.name" | "live.problem.agent" | "live.problem.tasks" | "live.problem.interval" | "live.problem.budget" }[] {
	const out: ReturnType<typeof liveMonitorProblems> = [];
	const whole = (v: string) => /^\d+$/.test(v.trim()) ? Number(v) : NaN;
	if (!f.name.trim()) out.push({ field: "name", key: "live.problem.name" });
	if (!f.agent || !agents.includes(f.agent)) out.push({ field: "agent", key: "live.problem.agent" });
	if (!(whole(f.tasks) >= 1)) out.push({ field: "tasks", key: "live.problem.tasks" });
	if (!(whole(f.interval) >= minInterval)) out.push({ field: "interval", key: "live.problem.interval" });
	if (!(Number(f.budget) > 0)) out.push({ field: "budget", key: "live.problem.budget" });
	return out;
}

function NewMonitorForm({ agent, onClose, onCreated }: { agent: string; onClose: () => void; onCreated: (id: string) => void }) {
	const { t } = useTranslation("liveMonitors");
	const { t: m } = useTranslation("uf7b");
	const [create, state] = useCreateLiveMonitorMutation();
	const agentList = useGetAgentsQuery();
	const list = useGetLiveMonitorsQuery(undefined);
	const minInterval = list.data?.min_interval_min || 15;
	const agents = endpointAgents(agentList.data?.agents ?? []);
	const [f, setF] = useState<LiveMonitorFields>({ name: "", agent, benchmark: "", tasks: "3", interval: "60", budget: "2" });
	const [err, setErr] = useState("");
	const [touched, setTouched] = useState<Partial<Record<keyof LiveMonitorFields, boolean>>>({});
	const problems = liveMonitorProblems(f, agents, minInterval);
	const problemOf = (k: keyof LiveMonitorFields) => problems.find((p) => p.field === k);
	const field = (k: keyof LiveMonitorFields, label: string, type = "text") => {
		const p = touched[k] ? problemOf(k) : undefined;
		return (
			<label className="flex flex-col gap-1 text-xs">
				{label}
				<Input
					type={type}
					value={f[k]}
					min={type === "number" ? (k === "interval" ? minInterval : k === "tasks" ? 1 : 0) : undefined}
					aria-invalid={p ? true : undefined}
					onChange={(e) => setF({ ...f, [k]: e.target.value })}
					onBlur={() => setTouched((x) => ({ ...x, [k]: true }))}
					data-testid={`live-monitor-form-${k}`}
				/>
				{p && (
					<span className="text-chart-error-ink" data-testid={`live-monitor-form-problem-${k}`}>
						{m(p.key, { min: minInterval })}
					</span>
				)}
			</label>
		);
	};
	const why = problems.length ? m("live.toCreate", { list: problems.map((p) => m(p.key, { min: minInterval })).join(" ") }) : null;
	const interval = Number(f.interval);
	const budget = Number(f.budget);
	const submit = async () => {
		setErr("");
		const res = await create({
			name: f.name.trim(),
			agent: f.agent,
			benchmark: f.benchmark || undefined,
			tasks: Number(f.tasks),
			interval_min: Number(f.interval),
			budget_usd_per_day: Number(f.budget),
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
				<MonitorAgentPicker value={f.agent} agents={agents} onChange={(id) => setF({ ...f, agent: id })} />
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
			{interval >= minInterval && budget > 0 && (
				<p className="text-muted-foreground text-xs" data-testid="live-monitor-form-cost">
					{m("live.cost", { count: Math.floor(1440 / interval), day: usd2(budget), month: usd2(budget * 30) })}
				</p>
			)}
			<div className="flex flex-wrap items-center gap-2">
				<Button
					size="sm"
					disabled={problems.length > 0 || state.isLoading}
					isLoading={state.isLoading}
					aria-describedby={why ? "live-monitor-form-why" : undefined}
					onClick={() => void submit()}
					data-testid="live-monitor-form-create"
				>
					{budget > 0 ? m("live.createUpTo", { day: usd2(budget) }) : t("form.create")}
				</Button>
				<Button size="sm" variant="ghost" onClick={onClose}>
					{t("form.cancel")}
				</Button>
				<WhyDisabled reason={state.isLoading ? null : why} testId="live-monitor-form-why" />
			</div>
		</Section>
	);
}

function MonitorPanel({ id }: { id: string }) {
	const { t } = useTranslation("liveMonitors");
	const detail = useGetLiveMonitorQuery(id, { pollingInterval: 60_000, skipPollingIfUnfocused: true });
	if (detail.isLoading) return <BlockSkeleton rows={6} testId="live-monitor-loading" />;
	if (detail.error || !detail.data) {
		return <LoadError title={t("detailError")} error={detail.error} onRetry={() => void detail.refetch()} testId="live-monitor-error" />;
	}
	return <MonitorDetailBody d={detail.data} />;
}

export function MonitorDetailBody({ d }: { d: MonitorDetailResponse }) {
	const { t } = useTranslation("liveMonitors");
	const s = d.samples;
	const [editing, setEditing] = useState(false);
	return (
		<div className="flex min-w-0 flex-col gap-6" data-testid="live-monitor-detail">
			<Section
				title={d.name}
				description={d.problem || `${d.agent} · ${d.benchmark} · ${d.schedule}`}
				actions={<MonitorActions d={d} onEdit={() => setEditing(true)} />}
				testId="live-monitor-summary"
			>
				{editing && <EditLiveMonitor d={d} minInterval={15} onClose={() => setEditing(false)} />}
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
						<LineChart
							title={t("detail.passRate")}
							series={[{ label: "", values: s.map(samplePassRate) }]}
							max={1}
							format={pct}
							testId="live-monitor-chart-pass"
						/>
						<LineChart
							title={t("detail.latency")}
							series={[
								{ label: "p50", values: s.map((x) => x.p50_ms ?? null) },
								{ label: "p95", values: s.map((x) => x.p95_ms ?? null), dashed: true },
							]}
							format={ms}
							testId="live-monitor-chart-latency"
						/>
						<LineChart
							title={t("detail.cost")}
							series={[{ label: "", values: s.map((x) => x.cost_usd) }]}
							format={usd}
							testId="live-monitor-chart-cost"
						/>
						<LineChart
							title={t("detail.errors")}
							series={[{ label: "", values: s.map((x) => x.errors) }]}
							format={(v) => String(v)}
							testId="live-monitor-chart-errors"
						/>
					</div>
				)}
			</Section>
			<Section title={t("detail.incidents")} testId="live-monitor-incidents">
				{d.incidents.length === 0 ? (
					<EmptyNote testId="live-monitor-no-incidents">{t("detail.noIncidents")}</EmptyNote>
				) : (
					d.incidents.map((i) => <IncidentCard key={i.id} i={i} />)
				)}
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

function MonitorActions({ d, onEdit }: { d: MonitorDetailResponse; onEdit: () => void }) {
	const { t } = useTranslation("liveMonitors");
	const { t: l8 } = useTranslation("ufl8");
	const { t: m } = useTranslation("uf7b");
	const confirm = useConfirm();
	const [act, state] = useMonitorActionMutation();
	const [badge, badgeState] = useSetMonitorBadgeMutation();
	const [err, setErr] = useState<unknown>(null);
	// M1-44: every action waits for the server and shows its error beside the buttons.
	const run = async (fn: () => Promise<unknown>, done?: string) => {
		setErr(null);
		try {
			await fn();
			if (done) notify({ text: done });
		} catch (e) {
			setErr(e);
		}
	};
	const known = d.check_budget_usd > 0;
	return (
		<span className="flex flex-wrap items-start gap-2" data-testid="live-monitor-actions">
			{/* M7-53: Run now spends money, so it always asks first and says how much one check can cost. */}
			<PaidButton
				size="sm"
				variant="outline"
				verb={m("live.runConfirm")}
				capUsd={known ? d.check_budget_usd : null}
				estimateAt="title"
				confirm="auto"
				kind="runNow"
				confirmText={
					known
						? m("live.runAsk", { name: d.name, usd: usd2(d.check_budget_usd), left: usd2(Math.max(0, d.budget_usd_per_day - d.spent_today_usd)) })
						: m("live.runAskUnknown", { name: d.name })
				}
				disabled={state.isLoading}
				onConfirm={() => run(() => act({ id: d.id, action: "run" }).unwrap(), t("started", { ns: "toast", name: d.name }))}
				testId="live-monitor-run"
			/>
			<Button
				size="sm"
				variant="outline"
				disabled={state.isLoading}
				onClick={() => void run(() => act({ id: d.id, action: d.paused ? "resume" : "pause" }).unwrap())}
				data-testid="live-monitor-pause"
			>
				{d.paused ? t("actions.resume") : t("actions.pause")}
			</Button>
			<Button size="sm" variant="outline" onClick={onEdit} data-testid="live-monitor-edit-open">
				{l8("liveEdit.edit")}
			</Button>
			<Button
				size="sm"
				variant="ghost"
				disabled={badgeState.isLoading}
				onClick={() => void run(() => badge({ id: d.id, enabled: !d.badge }).unwrap())}
				data-testid="live-monitor-badge"
			>
				{d.badge ? t("detail.badgeOff") : t("detail.badgeOn")}
			</Button>
			<Button
				size="sm"
				variant="ghost"
				disabled={state.isLoading}
				onClick={async () => {
					if (await confirm(t("actions.deleteConfirm", { name: d.name })))
						void run(() => act({ id: d.id, action: "delete" }).unwrap(), t("deleted", { ns: "toast", name: d.name }));
				}}
				data-testid="live-monitor-delete"
			>
				{t("actions.delete")}
			</Button>
			<InlineError error={err} testId="live-monitor-action-error" className="basis-full" />
		</span>
	);
}
