import { ConfirmButton } from "@/components/platform/badges";
import { useConfirm } from "@/components/platform/confirmDialog";
import { EmptyState } from "@/components/shell/emptyState";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import {
	useDecideImageMutation,
	useGetAdminUsageQuery,
	useGetOrgQuotaQuery,
	useListAdminContainersQuery,
	useListAdminImagesQuery,
	useListAdminOrgsQuery,
	useListAdminUsersQuery,
	useRemoveAdminRunnerMutation,
	useSetOrgQuotaMutation,
	useSetUserDisabledMutation,
} from "@/lib/store/apis/adminApi";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { errorStatus } from "@/lib/store/apis/session";
import type { AdminLimits, AdminOrg, AdminUsageRow } from "@/lib/types/admin";
import { useState } from "react";
import { useTranslation } from "react-i18next";

const GIB = 1 << 30;

function fmtDate(value?: string): string {
	if (!value) return "";
	const d = new Date(value);
	return Number.isNaN(d.getTime()) || d.getFullYear() < 1971 ? "" : d.toLocaleString();
}

function fmtBytes(n?: number): string {
	if (!n) return "";
	return n >= GIB ? `${(n / GIB).toFixed(1)} GiB` : `${Math.round(n / (1 << 20))} MiB`;
}

/** Loading, error (with retry) or nothing; panels render their data after it. */
function useGate(state: { isLoading: boolean; error?: unknown; refetch: () => unknown }, testId: string, unavailable?: string) {
	const { t } = useTranslation("admin");
	if (state.isLoading) {
		return (
			<div className="flex flex-col gap-2" data-testid={`${testId}-loading`}>
				{[0, 1, 2, 3].map((i) => (
					<div key={i} className="bg-muted h-10 animate-pulse rounded-lg" />
				))}
			</div>
		);
	}
	if (state.error) {
		const status = errorStatus(state.error);
		if (unavailable && (status === 404 || status === 405 || status === 503)) {
			return <EmptyState compact message={unavailable} action={{ label: t("hostingGuide"), to: "/docs", search: { doc: "hosting" } }} testId={`${testId}-unavailable`} />;
		}
		return (
			<EmptyState
				compact
				message={t("loadFailed", { message: getBenchErrorMessage(state.error) })}
				action={{ label: t("retry"), onClick: () => void state.refetch() }}
				testId={`${testId}-error`}
			/>
		);
	}
	return null;
}

export function AdminUsersPanel() {
	const { t } = useTranslation("admin");
	const confirm = useConfirm();
	const q = useListAdminUsersQuery();
	const [setDisabled, state] = useSetUserDisabledMutation();
	const [filter, setFilter] = useState("");
	const gate = useGate(q, "admin-users");
	if (gate) return gate;
	const users = (q.data?.users ?? []).filter((u) => {
		const f = filter.trim().toLowerCase();
		return !f || u.email.toLowerCase().includes(f) || u.name.toLowerCase().includes(f);
	});
	if ((q.data?.users ?? []).length === 0) return <EmptyState compact message={t("users.empty")} testId="admin-users-empty" />;
	const toggle = async (id: string, email: string, disabled: boolean) => {
		if (!disabled && !(await confirm(t("users.confirmDisable", { email })))) return;
		void setDisabled({ id, disabled: !disabled });
	};
	return (
		<div className="flex flex-col gap-3" data-testid="admin-users">
			<Input placeholder={t("users.search")} value={filter} onChange={(e) => setFilter(e.target.value)} className="w-full sm:max-w-sm" />
			{state.error && <p className="text-destructive text-sm">{getBenchErrorMessage(state.error)}</p>}
			<div className="bg-card min-w-0 overflow-x-auto rounded-xl border">
			<Table>
				<TableHeader>
					<TableRow>
						<TableHead>{t("users.email")}</TableHead>
						<TableHead>{t("users.name")}</TableHead>
						<TableHead className="text-right">{t("users.orgs")}</TableHead>
						<TableHead>{t("users.created")}</TableHead>
						<TableHead>{t("users.status")}</TableHead>
						<TableHead />
					</TableRow>
				</TableHeader>
				<TableBody>
					{users.map((u) => (
						<TableRow key={u.id} data-testid={`admin-user-${u.id}`}>
							<TableCell className="max-w-64">
								<div className="flex min-w-0 items-center gap-2">
									<span className="truncate font-medium" title={u.email}>
										{u.email}
									</span>
									{u.server_admin && <Badge variant="secondary">{t("users.serverAdmin")}</Badge>}
								</div>
							</TableCell>
							<TableCell className="text-muted-foreground">{u.name}</TableCell>
							<TableCell className="text-right tabular-nums">{u.orgs}</TableCell>
							<TableCell className="text-muted-foreground text-xs whitespace-nowrap">{fmtDate(u.created_at)}</TableCell>
							<TableCell>
								<Badge variant={u.disabled ? "destructive" : "success"}>{u.disabled ? t("users.disabled") : t("users.active")}</Badge>
							</TableCell>
							<TableCell className="text-right">
								<Button size="sm" variant="outline" disabled={state.isLoading} onClick={() => void toggle(u.id, u.email, u.disabled)}>
									{u.disabled ? t("users.enable") : t("users.disable")}
								</Button>
							</TableCell>
						</TableRow>
					))}
				</TableBody>
			</Table>
			</div>
		</div>
	);
}

type Translate = (key: string, options?: Record<string, unknown>) => string;

/** "at most 4 at once, no daily run limit, no daily spend limit": 0 means no limit on the server (N9). */
export function limitsSummary(l: Pick<AdminLimits, "max_concurrent_runs" | "max_runs_per_day" | "max_spend_usd_per_day">, t: Translate): string {
	return [
		l.max_concurrent_runs > 0 ? t("orgs.atOnce", { n: l.max_concurrent_runs }) : t("orgs.atOnceNone"),
		l.max_runs_per_day > 0 ? t("orgs.perDay", { n: l.max_runs_per_day }) : t("orgs.perDayNone"),
		l.max_spend_usd_per_day > 0 ? t("orgs.spend", { n: l.max_spend_usd_per_day }) : t("orgs.spendNone"),
	].join(", ");
}

export function AdminOrgsPanel() {
	const { t } = useTranslation("admin");
	const q = useListAdminOrgsQuery();
	const [editing, setEditing] = useState<string | null>(null);
	const gate = useGate(q, "admin-orgs");
	if (gate) return gate;
	const orgs = q.data?.orgs ?? [];
	if (orgs.length === 0) return <EmptyState compact message={t("orgs.empty")} testId="admin-orgs-empty" />;
	const current = orgs.find((o) => o.id === editing);
	return (
		<div className="flex flex-col gap-4" data-testid="admin-orgs">
			{current && <QuotaEditor key={current.id} org={current} onClose={() => setEditing(null)} />}
			<div className="bg-card min-w-0 overflow-x-auto rounded-xl border">
			<Table>
				<TableHeader>
					<TableRow>
						<TableHead>{t("orgs.slug")}</TableHead>
						<TableHead>{t("orgs.name")}</TableHead>
						<TableHead className="text-right">{t("orgs.members")}</TableHead>
						<TableHead>{t("orgs.quota")}</TableHead>
						<TableHead>{t("orgs.runtime")}</TableHead>
						<TableHead />
					</TableRow>
				</TableHeader>
				<TableBody>
					{orgs.map((o) => (
						<TableRow key={o.id} data-testid={`admin-org-${o.slug}`}>
							<TableCell className="font-mono text-xs">
								{o.slug} {o.public_read && <Badge variant="outline">{t("orgs.public")}</Badge>}
							</TableCell>
							<TableCell className="font-medium">{o.name}</TableCell>
							<TableCell className="text-right tabular-nums">{o.members}</TableCell>
							<TableCell className="text-muted-foreground text-sm">
								{o.quota && limitsSummary(o.quota, t as Translate)}
							</TableCell>
							<TableCell>
								<RuntimeBadge runtime={o.quota?.runtime ?? "runsc"} testId={`admin-org-runtime-${o.slug}`} />
							</TableCell>
							<TableCell className="text-right">
								<Button size="sm" variant="outline" onClick={() => setEditing(o.id)} data-testid={`admin-org-edit-${o.slug}`}>
									{t("orgs.edit")}
								</Button>
							</TableCell>
						</TableRow>
					))}
				</TableBody>
			</Table>
			</div>
		</div>
	);
}

/** The sandbox an org's agents get on the shared worker: gVisor by default; runc only where a server admin chose it. */
export function RuntimeBadge({ runtime, testId }: { runtime: AdminLimits["runtime"]; testId?: string }) {
	const { t } = useTranslation("admin");
	const runc = runtime === "runc";
	return (
		<Badge variant={runc ? "destructive" : "secondary"} title={t(runc ? "orgs.runtimeRuncHint" : "orgs.runtimeRunscHint")} data-testid={testId}>
			{t(runc ? "orgs.runtimeRunc" : "orgs.runtimeRunsc")}
		</Badge>
	);
}

/** The form before an org without stored limits gets the server's answer: every number 0 (no limit), gVisor. */
const BLANK_LIMITS: AdminLimits = {
	max_concurrent_runs: 0,
	max_runs_per_day: 0,
	max_spend_usd_per_day: 0,
	max_image_bytes: 0,
	allow_custom_images: false,
	shared_pool: false,
	runtime: "runsc",
};

/**
 * The quota form: numbers, toggles and the runtime; saved through POST /admin/orgs/{id}/quota. 0 means
 * no limit; "Use defaults" loads the server's own defaults (GET /admin/orgs/{id}/quota, N9).
 */
export function QuotaEditor({ org, onClose }: { org: AdminOrg; onClose(): void }) {
	const { t } = useTranslation("admin");
	const quota = useGetOrgQuotaQuery(org.id);
	const [edited, setForm] = useState<AdminLimits | null>(org.quota ?? null);
	const form = edited ?? quota.data?.limits ?? BLANK_LIMITS;
	const defaults = quota.data?.defaults;
	const [save, state] = useSetOrgQuotaMutation();
	const [saved, setSaved] = useState(false);
	const num = (key: keyof AdminLimits, scale = 1) => ({
		type: "number",
		min: 0,
		step: scale === 1 && key !== "max_spend_usd_per_day" ? 1 : "any",
		value: String(Number(form[key]) / scale),
		onChange: (e: React.ChangeEvent<HTMLInputElement>) => {
			setSaved(false);
			setForm({ ...form, [key]: Math.max(0, Number(e.target.value) || 0) * scale });
		},
	});
	const noLimit = (key: keyof AdminLimits, testId: string) =>
		Number(form[key]) === 0 && (
			<span className="text-muted-foreground text-xs" data-testid={testId}>
				{t("quota.noLimit")}
			</span>
		);
	const submit = async () => {
		const res = await save({ orgId: org.id, limits: { ...form, max_image_bytes: Math.round(form.max_image_bytes) } });
		if (!("error" in res)) setSaved(true);
	};
	return (
		<Card data-testid="admin-quota-editor">
			<CardHeader>
				<CardTitle>{t("quota.title", { org: org.name || org.slug })}</CardTitle>
			</CardHeader>
			<CardContent className="flex flex-col gap-4">
				<div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
					<label className="flex flex-col gap-1 text-sm">
						{t("quota.maxConcurrent")}
						<Input {...num("max_concurrent_runs")} data-testid="quota-max-concurrent" />
						{noLimit("max_concurrent_runs", "quota-max-concurrent-none")}
					</label>
					<label className="flex flex-col gap-1 text-sm">
						{t("quota.maxPerDay")}
						<Input {...num("max_runs_per_day")} data-testid="quota-max-per-day" />
						{noLimit("max_runs_per_day", "quota-max-per-day-none")}
					</label>
					<label className="flex flex-col gap-1 text-sm">
						{t("quota.maxSpend")}
						<Input {...num("max_spend_usd_per_day")} data-testid="quota-max-spend" />
						{noLimit("max_spend_usd_per_day", "quota-max-spend-none")}
					</label>
					<label className="flex flex-col gap-1 text-sm">
						{t("quota.maxImage")}
						<Input {...num("max_image_bytes", GIB)} />
					</label>
					<label className="flex flex-col gap-1 text-sm">
						{t("quota.runtime")}
						<select
							className="border-input bg-background h-8 rounded-md border px-2 text-sm"
							value={form.runtime}
							onChange={(e) => setForm({ ...form, runtime: e.target.value === "runsc" ? "runsc" : "runc" })}
							data-testid="quota-runtime"
						>
							<option value="runsc">{t("quota.runsc")}</option>
							<option value="runc">{t("quota.runc")}</option>
						</select>
						{form.runtime === "runc" && (
							<span className="text-destructive text-xs" data-testid="quota-runc-warning">
								{t("quota.runcWarning")}
							</span>
						)}
					</label>
				</div>
				<label className="flex items-center gap-2 text-sm">
					<input type="checkbox" checked={form.allow_custom_images} onChange={(e) => setForm({ ...form, allow_custom_images: e.target.checked })} />
					{t("quota.allowCustom")}
				</label>
				<label className="flex items-center gap-2 text-sm">
					<input type="checkbox" checked={form.shared_pool} onChange={(e) => setForm({ ...form, shared_pool: e.target.checked })} />
					{t("quota.sharedPool")}
				</label>
				<p className="text-muted-foreground text-xs">{t("quota.zeroHint")}</p>
				{state.error && <p className="text-destructive text-sm">{getBenchErrorMessage(state.error)}</p>}
				{saved && <p className="text-sm" data-testid="quota-saved">{t("quota.saved")}</p>}
				<div className="flex gap-2">
					<Button onClick={() => void submit()} disabled={state.isLoading} data-testid="quota-save">
						{t("quota.save")}
					</Button>
					<Button
						variant="outline"
						disabled={!defaults}
						title={defaults ? undefined : t("quota.defaultsLoading")}
						onClick={() => {
							if (!defaults) return;
							setSaved(false);
							setForm(defaults);
						}}
						dataTestId="quota-reset"
					>
						{t("quota.reset")}
					</Button>
					<Button variant="ghost" onClick={onClose}>
						{t("quota.cancel")}
					</Button>
				</div>
			</CardContent>
		</Card>
	);
}

const IMAGE_FILTERS = ["pending", "approved", "rejected", "all"] as const;
type ImageFilter = (typeof IMAGE_FILTERS)[number];

/** Image requests from orgs (J6): pending first, approve or reject with a note the org sees. */
export function AdminImagesPanel() {
	const { t } = useTranslation("admin");
	const [filter, setFilter] = useState<ImageFilter>("pending");
	const q = useListAdminImagesQuery(filter === "all" ? undefined : filter);
	const [decide, state] = useDecideImageMutation();
	const [rejecting, setRejecting] = useState<string | null>(null);
	const [note, setNote] = useState("");
	const gate = useGate(q, "admin-images", t("images.unavailable"));
	const statusLabel = (s: string) => (s === "approved" ? t("images.approved") : s === "rejected" ? t("images.rejected") : t("images.pending"));
	const filters = (
		<div className="flex gap-1" role="tablist" aria-label={t("images.status")} data-testid="admin-images-filter">
			{IMAGE_FILTERS.map((f) => (
				<Button key={f} size="sm" variant={filter === f ? "secondary" : "ghost"} role="tab" aria-selected={filter === f} onClick={() => setFilter(f)} data-testid={`admin-images-filter-${f}`}>
					{f === "all" ? t("images.all", { defaultValue: "All" }) : statusLabel(f)}
				</Button>
			))}
		</div>
	);
	if (gate) return gate;
	const images = q.data?.images ?? [];
	const confirmReject = (id: string) => {
		void decide({ id, decision: "reject", reason: note.trim() || undefined });
		setRejecting(null);
		setNote("");
	};
	return (
		<div className="flex flex-col gap-3" data-testid="admin-images">
			{filters}
			{state.error && <p className="text-destructive text-sm">{getBenchErrorMessage(state.error)}</p>}
			{images.length === 0 ? (
				<EmptyState compact message={t("images.empty")} testId="admin-images-empty" />
			) : (
				<div className="bg-card min-w-0 overflow-x-auto rounded-xl border">
					<Table>
						<TableHeader>
							<TableRow>
								<TableHead>{t("images.ref")}</TableHead>
								<TableHead>{t("images.org")}</TableHead>
								<TableHead>{t("images.size")}</TableHead>
								<TableHead>{t("images.status")}</TableHead>
								<TableHead>{t("images.requested")}</TableHead>
								<TableHead />
							</TableRow>
						</TableHeader>
						<TableBody>
							{images.map((img) => (
								<TableRow key={img.id} data-testid={`admin-image-${img.id}`}>
									<TableCell className="max-w-md">
										<span className="block truncate font-mono text-xs" title={img.image}>
											{img.image}
										</span>
										{img.note && <span className="text-muted-foreground block text-xs">{img.note}</span>}
										{img.decision && <span className="text-muted-foreground block text-xs">{img.decision}</span>}
									</TableCell>
									<TableCell>{img.org_id}</TableCell>
									<TableCell>{fmtBytes(img.size_bytes)}</TableCell>
									<TableCell>
										<Badge variant={img.status === "approved" ? "success" : img.status === "rejected" ? "destructive" : "warning"}>{statusLabel(img.status)}</Badge>
									</TableCell>
									<TableCell>
										{img.requested_by} {fmtDate(img.created_at)}
									</TableCell>
									<TableCell>
										{img.status === "pending" &&
											(rejecting === img.id ? (
												<div className="flex items-center justify-end gap-2">
													<Input
														className="h-8 w-48"
														value={note}
														placeholder={t("images.rejectReason")}
														onChange={(e) => setNote(e.target.value)}
														data-testid={`admin-image-note-${img.id}`}
													/>
													<Button size="sm" variant="outline" disabled={state.isLoading} onClick={() => confirmReject(img.id)}>
														{t("images.reject")}
													</Button>
													<Button size="sm" variant="ghost" onClick={() => setRejecting(null)}>
														{t("images.cancel", { defaultValue: "Cancel" })}
													</Button>
												</div>
											) : (
												<div className="flex justify-end gap-2">
													<Button size="sm" disabled={state.isLoading} onClick={() => void decide({ id: img.id, decision: "approve" })} data-testid={`admin-image-approve-${img.id}`}>
														{t("images.approve")}
													</Button>
													<Button size="sm" variant="outline" disabled={state.isLoading} onClick={() => setRejecting(img.id)} data-testid={`admin-image-reject-${img.id}`}>
														{t("images.reject")}
													</Button>
												</div>
											))}
									</TableCell>
								</TableRow>
							))}
						</TableBody>
					</Table>
				</div>
			)}
		</div>
	);
}

function isoDay(d: Date): string {
	return d.toISOString().slice(0, 10);
}

export function AdminUsagePanel() {
	const { t } = useTranslation("admin");
	const [from, setFrom] = useState(() => isoDay(new Date(Date.now() - 30 * 86400000)));
	const [to, setTo] = useState(() => isoDay(new Date()));
	const q = useGetAdminUsageQuery({ from, to });
	const gate = useGate(q, "admin-usage", t("usage.unavailable"));
	const rows: AdminUsageRow[] = q.data?.orgs ?? [];
	const total = q.data?.total_cost_usd ?? rows.reduce((sum, r) => sum + (r.cost_usd ?? 0), 0);
	const runs = q.data?.runs ?? rows.reduce((sum, r) => sum + (r.runs ?? 0), 0);
	const tokens = q.data?.total_tokens ?? 0;
	const anyUnknown = rows.some((r) => (r.unknown_cost_runs ?? 0) > 0);
	return (
		<div className="flex flex-col gap-4" data-testid="admin-usage">
			<div className="flex flex-wrap items-end gap-3">
				<label className="flex flex-col gap-1 text-sm">
					{t("usage.from")}
					<Input type="date" value={from} onChange={(e) => setFrom(e.target.value)} />
				</label>
				<label className="flex flex-col gap-1 text-sm">
					{t("usage.to")}
					<Input type="date" value={to} onChange={(e) => setTo(e.target.value)} />
				</label>
			</div>
			{gate ?? (
				<>
					<div className="flex gap-6 text-sm">
						<span>
							{t("usage.total")}: <strong className="tabular-nums">${total.toFixed(2)}</strong>
						</span>
						<span>
							{t("usage.runs")}:{" "}
							<strong className="tabular-nums" data-testid="admin-usage-runs">
								{runs}
							</strong>
						</span>
						{tokens > 0 && (
							<span>
								{t("usage.tokens")}: <strong className="tabular-nums">{Math.round(tokens).toLocaleString()}</strong>
							</span>
						)}
					</div>
					{rows.length === 0 ? (
						<EmptyState compact message={t("usage.empty")} testId="admin-usage-empty" />
					) : (
						<div className="bg-card min-w-0 overflow-x-auto rounded-xl border">
						<Table>
							<TableHeader>
								<TableRow>
									<TableHead>{t("usage.org")}</TableHead>
									<TableHead>{t("usage.runs")}</TableHead>
									<TableHead>{t("usage.cost")}</TableHead>
									{anyUnknown && <TableHead>{t("usage.unknownCost")}</TableHead>}
								</TableRow>
							</TableHeader>
							<TableBody>
								{rows.map((r) => (
									<TableRow key={r.org_id} data-testid={`admin-usage-row-${r.org_id}`}>
										<TableCell title={r.org_id}>
											<div className="flex min-w-0 flex-col">
												<span className="font-medium">{r.name || r.slug || r.org_id}</span>
												{r.slug && r.name && <span className="text-muted-foreground font-mono text-xs">{r.slug}</span>}
												{r.error && (
													<span className="text-destructive text-xs" role="alert">
														{t("usage.orgError", { error: r.error })}
													</span>
												)}
											</div>
										</TableCell>
										<TableCell className="tabular-nums">{r.runs ?? 0}</TableCell>
										<TableCell className="tabular-nums">${(r.cost_usd ?? 0).toFixed(2)}</TableCell>
										{anyUnknown && <TableCell className="tabular-nums">{r.unknown_cost_runs ?? 0}</TableCell>}
									</TableRow>
								))}
							</TableBody>
						</Table>
						</div>
					)}
				</>
			)}
		</div>
	);
}

export function AdminContainersPanel() {
	const { t } = useTranslation("admin");
	const q = useListAdminContainersQuery(undefined, { pollingInterval: 10000 });
	const [remove, removeState] = useRemoveAdminRunnerMutation();
	const gate = useGate(q, "admin-containers");
	if (gate) return gate;
	const runners = q.data?.runners ?? [];
	if (runners.length === 0) return <EmptyState compact message={t("containers.empty")} testId="admin-containers-empty" />;
	return (
		<div className="flex flex-col gap-3" data-testid="admin-containers">
			<div>
				<Button size="sm" variant="outline" onClick={() => void q.refetch()}>
					{t("containers.refresh")}
				</Button>
			</div>
			{removeState.error && (
				<p className="text-destructive text-sm" role="alert" data-testid="admin-runner-remove-error">
					{t("containers.removeError", { message: getBenchErrorMessage(removeState.error) })}
				</p>
			)}
			<div className="bg-card min-w-0 overflow-x-auto rounded-xl border">
			<Table>
				<TableHeader>
					<TableRow>
						<TableHead>{t("containers.org")}</TableHead>
						<TableHead>{t("containers.runner")}</TableHead>
						<TableHead>{t("containers.pool")}</TableHead>
						<TableHead>{t("containers.host")}</TableHead>
						<TableHead>{t("containers.state")}</TableHead>
						<TableHead>{t("containers.slots")}</TableHead>
						<TableHead>{t("containers.seen")}</TableHead>
						<TableHead />
					</TableRow>
				</TableHeader>
				<TableBody>
					{runners.map((r) => (
						<TableRow key={r.runner_id} data-testid={`admin-runner-${r.runner_id}`}>
							<TableCell title={r.org_id}>{r.org_name || r.org_slug}</TableCell>
							<TableCell className="font-mono text-xs">{r.runner}</TableCell>
							<TableCell>{r.pool}</TableCell>
							<TableCell>{r.host}</TableCell>
							<TableCell>
								<Badge variant={r.live ? "success" : "secondary"}>{r.live ? t("containers.live") : t("containers.offline")}</Badge> {r.state}
							</TableCell>
							<TableCell className="tabular-nums" title={r.running.join(", ")}>
								{r.running.length}/{r.slots}
							</TableCell>
							<TableCell>{fmtDate(r.last_seen)}</TableCell>
							<TableCell className="text-right">
								{!r.live && (
									<ConfirmButton
										label={t("containers.remove")}
										question={t("containers.confirmRemove", { runner: r.runner, org: r.org_name || r.org_slug })}
										confirmLabel={t("containers.remove")}
										cancelLabel={t("containers.removeCancel")}
										onConfirm={() => void remove({ orgId: r.org_id, runnerId: r.runner_id })}
										isLoading={removeState.isLoading && removeState.originalArgs?.runnerId === r.runner_id}
										testId={`admin-runner-remove-${r.runner_id}`}
									/>
								)}
							</TableCell>
						</TableRow>
					))}
				</TableBody>
			</Table>
			</div>
		</div>
	);
}
