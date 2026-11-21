import { EmptyState } from "@/components/shell/emptyState";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import {
	useDecideImageMutation,
	useGetAdminUsageQuery,
	useListAdminContainersQuery,
	useListAdminImagesQuery,
	useListAdminOrgsQuery,
	useListAdminUsersQuery,
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
	if (state.isLoading) return <p className="text-muted-foreground text-sm">...</p>;
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
	const toggle = (id: string, email: string, disabled: boolean) => {
		if (!disabled && !window.confirm(t("users.confirmDisable", { email }))) return;
		void setDisabled({ id, disabled: !disabled });
	};
	return (
		<div className="flex flex-col gap-3" data-testid="admin-users">
			<Input placeholder={t("users.search")} value={filter} onChange={(e) => setFilter(e.target.value)} className="max-w-sm" />
			{state.error && <p className="text-destructive text-sm">{getBenchErrorMessage(state.error)}</p>}
			<Table>
				<TableHeader>
					<TableRow>
						<TableHead>{t("users.email")}</TableHead>
						<TableHead>{t("users.name")}</TableHead>
						<TableHead>{t("users.orgs")}</TableHead>
						<TableHead>{t("users.created")}</TableHead>
						<TableHead>{t("users.status")}</TableHead>
						<TableHead />
					</TableRow>
				</TableHeader>
				<TableBody>
					{users.map((u) => (
						<TableRow key={u.id} data-testid={`admin-user-${u.id}`}>
							<TableCell>
								{u.email} {u.server_admin && <Badge variant="secondary">{t("users.serverAdmin")}</Badge>}
							</TableCell>
							<TableCell>{u.name}</TableCell>
							<TableCell className="tabular-nums">{u.orgs}</TableCell>
							<TableCell>{fmtDate(u.created_at)}</TableCell>
							<TableCell>
								<Badge variant={u.disabled ? "destructive" : "success"}>{u.disabled ? t("users.disabled") : t("users.active")}</Badge>
							</TableCell>
							<TableCell className="text-right">
								<Button size="sm" variant="outline" disabled={state.isLoading} onClick={() => toggle(u.id, u.email, u.disabled)}>
									{u.disabled ? t("users.enable") : t("users.disable")}
								</Button>
							</TableCell>
						</TableRow>
					))}
				</TableBody>
			</Table>
		</div>
	);
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
			<Table>
				<TableHeader>
					<TableRow>
						<TableHead>{t("orgs.slug")}</TableHead>
						<TableHead>{t("orgs.name")}</TableHead>
						<TableHead>{t("orgs.members")}</TableHead>
						<TableHead>{t("orgs.quota")}</TableHead>
						<TableHead />
					</TableRow>
				</TableHeader>
				<TableBody>
					{orgs.map((o) => (
						<TableRow key={o.id} data-testid={`admin-org-${o.slug}`}>
							<TableCell className="font-mono text-xs">
								{o.slug} {o.public_read && <Badge variant="outline">{t("orgs.public")}</Badge>}
							</TableCell>
							<TableCell>{o.name}</TableCell>
							<TableCell className="tabular-nums">{o.members}</TableCell>
							<TableCell className="text-sm">
								{o.quota &&
									t("orgs.quotaSummary", {
										concurrent: o.quota.max_concurrent_runs,
										perDay: o.quota.max_runs_per_day,
										spend: o.quota.max_spend_usd_per_day,
									})}
								{o.quota?.runtime === "runsc" && <Badge variant="secondary" className="ml-2">gVisor</Badge>}
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
	);
}

const DEFAULT_LIMITS: AdminLimits = {
	max_concurrent_runs: 2,
	max_runs_per_day: 50,
	max_spend_usd_per_day: 10,
	max_image_bytes: 2 * GIB,
	allow_custom_images: false,
	shared_pool: false,
	runtime: "runc",
};

/** The quota form: numbers, toggles and the runtime; saved through POST /admin/orgs/{id}/quota. */
export function QuotaEditor({ org, onClose }: { org: AdminOrg; onClose(): void }) {
	const { t } = useTranslation("admin");
	const [form, setForm] = useState<AdminLimits>(org.quota ?? DEFAULT_LIMITS);
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
					</label>
					<label className="flex flex-col gap-1 text-sm">
						{t("quota.maxPerDay")}
						<Input {...num("max_runs_per_day")} />
					</label>
					<label className="flex flex-col gap-1 text-sm">
						{t("quota.maxSpend")}
						<Input {...num("max_spend_usd_per_day")} />
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
							<option value="runc">{t("quota.runc")}</option>
							<option value="runsc">{t("quota.runsc")}</option>
						</select>
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
					<Button variant="outline" onClick={() => setForm(DEFAULT_LIMITS)}>
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

export function AdminImagesPanel() {
	const { t } = useTranslation("admin");
	const q = useListAdminImagesQuery();
	const [decide, state] = useDecideImageMutation();
	const gate = useGate(q, "admin-images", t("images.unavailable"));
	if (gate) return gate;
	const images = q.data?.images ?? [];
	if (images.length === 0) return <EmptyState compact message={t("images.empty")} testId="admin-images-empty" />;
	const reject = (id: string) => {
		const reason = window.prompt(t("images.rejectReason"));
		if (reason === null) return;
		void decide({ id, decision: "reject", reason: reason.trim() || undefined });
	};
	const statusLabel = (s: string) => (s === "approved" ? t("images.approved") : s === "rejected" ? t("images.rejected") : t("images.pending"));
	return (
		<div className="flex flex-col gap-3" data-testid="admin-images">
			{state.error && <p className="text-destructive text-sm">{getBenchErrorMessage(state.error)}</p>}
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
							<TableCell className="max-w-md truncate font-mono text-xs" title={img.ref}>
								{img.ref}
							</TableCell>
							<TableCell>{img.org_slug ?? img.org_id}</TableCell>
							<TableCell>{fmtBytes(img.size_bytes)}</TableCell>
							<TableCell>
								<Badge variant={img.status === "approved" ? "success" : img.status === "rejected" ? "destructive" : "warning"}>
									{statusLabel(img.status)}
								</Badge>
							</TableCell>
							<TableCell>
								{img.requested_by} {fmtDate(img.requested_at)}
							</TableCell>
							<TableCell className="flex justify-end gap-2">
								{img.status !== "approved" && (
									<Button size="sm" disabled={state.isLoading} onClick={() => void decide({ id: img.id, decision: "approve" })}>
										{t("images.approve")}
									</Button>
								)}
								{img.status !== "rejected" && (
									<Button size="sm" variant="outline" disabled={state.isLoading} onClick={() => reject(img.id)}>
										{t("images.reject")}
									</Button>
								)}
							</TableCell>
						</TableRow>
					))}
				</TableBody>
			</Table>
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
	const rows: AdminUsageRow[] = q.data?.orgs ?? q.data?.rows ?? [];
	const total = q.data?.total_cost_usd ?? rows.reduce((sum, r) => sum + (r.cost_usd ?? 0), 0);
	const runs = q.data?.total_runs ?? rows.reduce((sum, r) => sum + (r.runs ?? 0), 0);
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
							{t("usage.runs")}: <strong className="tabular-nums">{runs}</strong>
						</span>
					</div>
					{rows.length === 0 ? (
						<EmptyState compact message={t("usage.empty")} testId="admin-usage-empty" />
					) : (
						<Table>
							<TableHeader>
								<TableRow>
									<TableHead>{t("usage.org")}</TableHead>
									<TableHead>{t("usage.runs")}</TableHead>
									<TableHead>{t("usage.cost")}</TableHead>
								</TableRow>
							</TableHeader>
							<TableBody>
								{rows.map((r, i) => (
									<TableRow key={r.org_id ?? r.key ?? i}>
										<TableCell>{r.org_slug ?? r.org_id ?? r.key}</TableCell>
										<TableCell className="tabular-nums">{r.runs ?? 0}</TableCell>
										<TableCell className="tabular-nums">{(r.cost_usd ?? 0).toFixed(2)}</TableCell>
									</TableRow>
								))}
							</TableBody>
						</Table>
					)}
				</>
			)}
		</div>
	);
}

export function AdminContainersPanel() {
	const { t } = useTranslation("admin");
	const q = useListAdminContainersQuery(undefined, { pollingInterval: 10000 });
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
					</TableRow>
				</TableHeader>
				<TableBody>
					{runners.map((r) => (
						<TableRow key={r.runner_id} data-testid={`admin-runner-${r.runner_id}`}>
							<TableCell>{r.org_slug}</TableCell>
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
						</TableRow>
					))}
				</TableBody>
			</Table>
		</div>
	);
}
