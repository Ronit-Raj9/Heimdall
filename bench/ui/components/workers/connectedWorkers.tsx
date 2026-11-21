import { CopyCodeBlock } from "@/components/suites/copyCodeBlock";
import { formatRelative } from "@/components/platform/platform.utils";
import { Button } from "@/components/ui/button";
import { StatusPill, type StatusTone } from "@/components/ui/statusPill";
import { getLocale } from "@/lib/i18n";
import {
	kubernetesManifestUrl,
	useCreateWorkerConnectionMutation,
	useListWorkerConnectionsQuery,
	useWorkerConnectionActionMutation,
	useWorkerInstallCommandMutation,
	type WorkerAction,
	type WorkerConnection,
	type WorkerConnectionCreated,
	type WorkerConnectionStatus,
	type WorkerPlatform,
} from "@/lib/store/apis/workerConnectionsApi";
import { cn } from "@/lib/utils";
import { Boxes, Cloud, Plus, Server, Ship } from "lucide-react";
import { useId, useState, type ReactNode } from "react";
import { useSetWorkerTargetMutation } from "@/lib/store/apis/workerTargetApi";
import { useTranslation } from "react-i18next";

import type byoworkerEn from "@/locales/en/byoworker.json";

declare module "@/lib/i18n/resources" {
	interface LaneResources {
		byoworker: typeof byoworkerEn;
	}
}

/**
 * Bring your own worker: the workers an organization connected (Runloop, Fly Machines, its own
 * Kubernetes cluster, its own VM), each with health, capacity, platform and last seen, the worker
 * new evaluations run on, and the form that connects one. Renders nothing on a server without the
 * feature (local mode answers 503).
 */

const STATUS_TONE: Record<WorkerConnectionStatus, StatusTone> = {
	ready: "ok",
	starting: "running",
	waiting: "neutral",
	offline: "warn",
	stopped: "neutral",
	error: "bad",
};

const PLATFORM_ICON: Record<string, typeof Cloud> = { runloop: Cloud, fly: Cloud, kubernetes: Ship, vm: Server };

function apiError(error: unknown, fallback: string): string {
	const data = (error as { data?: { error?: { message?: string } | string } } | undefined)?.data;
	if (typeof data?.error === "string") return data.error;
	return data?.error?.message ?? fallback;
}

function errorStatus(error: unknown): number | undefined {
	const s = (error as { status?: unknown } | undefined)?.status;
	return typeof s === "number" ? s : undefined;
}

export function ConnectedWorkers() {
	const { t } = useTranslation("byoworker");
	const query = useListWorkerConnectionsQuery(undefined, { pollingInterval: 15000 });
	const [connecting, setConnecting] = useState(false);
	const status = query.isError ? errorStatus(query.error) : undefined;
	if (status === 503 || status === 404) return null; // not a hosted server
	const data = query.data;
	return (
		<section className="flex min-w-0 flex-col gap-4" data-testid="byo-workers">
			<header className="flex min-w-0 flex-wrap items-end justify-between gap-3">
				<div className="min-w-0">
					<h2 className="text-foreground text-base font-semibold">{t("title")}</h2>
					<p className="text-muted-foreground max-w-2xl text-sm text-pretty">{t("subtitle")}</p>
				</div>
				{!connecting && (
					<Button size="sm" onClick={() => setConnecting(true)} data-testid="byo-connect">
						<Plus aria-hidden="true" />
						{t("connect")}
					</Button>
				)}
			</header>
			{connecting && data && <ConnectWorkerForm platforms={data.platforms} onClose={() => setConnecting(false)} />}
			{query.isError ? (
				<p className="text-chart-error-ink text-sm" role="alert" data-testid="byo-error">
					{t("loadError")}: {apiError(query.error, String(status ?? ""))}
				</p>
			) : !data ? null : (
				<>
					{data.connections.length === 0 ? (
						<p className="text-muted-foreground rounded-xl border border-dashed px-5 py-4 text-sm" data-testid="byo-empty">
							{t("empty")}
						</p>
					) : (
						<ul className="grid min-w-0 grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3" data-testid="byo-list">
							{data.connections.map((c) => (
								<li key={c.id} className="min-w-0">
									<ConnectionCard conn={c} />
								</li>
							))}
						</ul>
					)}
				</>
			)}
		</section>
	);
}

/** One worker: status, platform, capacity, last seen and its actions. */
export function ConnectionCard({ conn }: { conn: WorkerConnection }) {
	const { t } = useTranslation("byoworker");
	const [act, actState] = useWorkerConnectionActionMutation();
	const [reveal, revealState] = useWorkerInstallCommandMutation();
	const [setTarget] = useSetWorkerTargetMutation();
	const locale = getLocale();
	const Icon = PLATFORM_ICON[conn.platform] ?? Boxes;
	const runner = conn.runner;
	const seen = runner ? formatRelative(runner.last_seen, locale) : null;
	const starts = conn.platform !== "vm";
	const running = conn.instance && (conn.instance.state === "starting" || conn.instance.state === "running");
	const run = (action: WorkerAction) => {
		if (action === "remove" && !window.confirm(t("actions.confirmRemove", { name: conn.name }))) return;
		void act({ id: conn.id, action });
	};
	const capacity = runner ? t("card.slots", { used: runner.running_slots, total: runner.slots }) : (conn.health?.capacity ?? null);
	return (
		<article
			className="bg-card flex h-full min-w-0 flex-col gap-4 rounded-xl border p-5"
			data-testid={`byo-card-${conn.name}`}
			data-status={conn.status}
		>
			<header className="flex items-start justify-between gap-3">
				<div className="flex min-w-0 items-center gap-3">
					<span className="bg-muted flex size-9 shrink-0 items-center justify-center rounded-lg">
						<Icon className="size-4" aria-hidden="true" />
					</span>
					<div className="min-w-0">
						<p className="text-foreground flex items-center gap-2 truncate text-base font-semibold">
							<span className="truncate">{conn.name}</span>
							{conn.default && (
								<span className="bg-primary/10 text-primary rounded px-1.5 py-0.5 text-[11px] font-medium">{t("default.badge")}</span>
							)}
						</p>
						<p className="text-muted-foreground truncate text-xs">{conn.platform_title}</p>
					</div>
				</div>
				<StatusPill tone={STATUS_TONE[conn.status] ?? "neutral"} pulse={conn.status === "starting"} testId={`byo-card-${conn.name}-status`}>
					{t(`status.${conn.status}`, { defaultValue: conn.status })}
				</StatusPill>
			</header>
			<p className="text-muted-foreground text-sm text-pretty" data-testid={`byo-card-${conn.name}-detail`}>
				{conn.status_detail}
			</p>
			<dl className="grid grid-cols-2 gap-3 text-xs">
				<Fact label={t("card.capacity")}>{capacity ?? t("card.never")}</Fact>
				<Fact label={t("card.seen")}>
					{seen ? (
						<time dateTime={String(runner?.last_seen ?? "")} title={String(runner?.last_seen ?? "")}>
							{seen}
						</time>
					) : (
						t("card.never")
					)}
				</Fact>
			</dl>
			{conn.health && (
				<p
					className={cn("text-xs", conn.health.ok ? "text-muted-foreground" : "text-chart-error-ink")}
					data-testid={`byo-card-${conn.name}-health`}
				>
					{conn.health.detail} {t("card.checked", { when: formatRelative(conn.health.checked_at, locale) ?? "" })}
				</p>
			)}
			{runner?.version && (
				<p className="text-muted-foreground truncate font-mono text-[11px]">{t("card.version", { version: runner.version })}</p>
			)}
			{revealState.data?.install && (
				<CopyCodeBlock code={revealState.data.install} label={t("form.installTitle")} testId={`byo-card-${conn.name}-install`} />
			)}
			{(actState.isError || revealState.isError) && (
				<p className="text-chart-error-ink text-xs" role="alert" data-testid={`byo-card-${conn.name}-error`}>
					{apiError(actState.error ?? revealState.error, "")}
				</p>
			)}
			<div className="mt-auto flex flex-wrap gap-2">
				{starts && (
					<Button
						size="sm"
						variant="outline"
						disabled={actState.isLoading}
						onClick={() => run("check")}
						data-testid={`byo-card-${conn.name}-check`}
					>
						{t("actions.check")}
					</Button>
				)}
				{starts &&
					(running ? (
						<Button
							size="sm"
							variant="outline"
							disabled={actState.isLoading}
							onClick={() => run("stop")}
							data-testid={`byo-card-${conn.name}-stop`}
						>
							{t("actions.stop")}
						</Button>
					) : (
						<Button size="sm" disabled={actState.isLoading} onClick={() => run("start")} data-testid={`byo-card-${conn.name}-start`}>
							{t("actions.start")}
						</Button>
					))}
				{!starts && !revealState.data && (
					<Button
						size="sm"
						variant="outline"
						disabled={revealState.isLoading}
						onClick={() => void reveal(conn.id)}
						data-testid={`byo-card-${conn.name}-install-show`}
					>
						{t("actions.install")}
					</Button>
				)}
				{!conn.default && (
					<Button
						size="sm"
						variant="ghost"
						onClick={() => void setTarget(`worker:${conn.pool}`)}
						data-testid={`byo-card-${conn.name}-default`}
					>
						{t("actions.makeDefault")}
					</Button>
				)}
				<Button
					size="sm"
					variant="ghost"
					className="text-chart-error-ink"
					disabled={actState.isLoading}
					onClick={() => run("remove")}
					data-testid={`byo-card-${conn.name}-remove`}
				>
					{t("actions.remove")}
				</Button>
			</div>
		</article>
	);
}

function Fact({ label, children }: { label: string; children: ReactNode }) {
	return (
		<div className="min-w-0">
			<dt className="text-muted-foreground">{label}</dt>
			<dd className="text-foreground truncate text-sm font-medium">{children}</dd>
		</div>
	);
}

/** A form field: label, control, hint. */
function Field({ label, hint, children, htmlFor }: { label: string; hint?: string; children: ReactNode; htmlFor: string }) {
	return (
		<div className="flex min-w-0 flex-col gap-1.5">
			<label htmlFor={htmlFor} className="text-sm font-medium">
				{label}
			</label>
			{children}
			{hint && <span className="text-muted-foreground text-xs">{hint}</span>}
		</div>
	);
}

const inputClass = "border-input bg-background h-8 w-full min-w-0 rounded-md border px-2 text-sm";
const areaClass = "border-input bg-background min-h-28 w-full min-w-0 rounded-md border px-2 py-1.5 font-mono text-xs";

/** What the form collects, before it becomes a create request. */
export interface ConnectDraft {
	platform: string;
	name: string;
	fields: Record<string, string>;
	kubeMode: "kubeconfig" | "token";
}

/** The POST /workers/connections body of a draft: settings, and the credential for the platform. */
export function draftToCreate(d: ConnectDraft) {
	const f = d.fields;
	const pick = (...keys: string[]) => Object.fromEntries(keys.filter((k) => (f[k] ?? "").trim() !== "").map((k) => [k, f[k].trim()]));
	switch (d.platform) {
		case "runloop":
			return { name: d.name.trim(), platform: d.platform, settings: pick("size", "blueprint", "hours"), secret: (f.secret ?? "").trim() };
		case "fly":
			return {
				name: d.name.trim(),
				platform: d.platform,
				settings: pick("app", "region", "cpus", "memory_mb", "cpu_kind", "image"),
				secret: (f.secret ?? "").trim(),
			};
		case "kubernetes": {
			const secret =
				d.kubeMode === "kubeconfig"
					? (f.kubeconfig ?? "").trim()
					: JSON.stringify({ server: (f.server ?? "").trim(), certificate_authority: (f.ca ?? "").trim(), token: (f.token ?? "").trim() });
			return { name: d.name.trim(), platform: d.platform, settings: pick("namespace", "image"), secret };
		}
		case "vm":
			return { name: d.name.trim(), platform: d.platform };
		default:
			return { name: d.name.trim(), platform: d.platform, settings: {}, secret: (f.secret ?? "").trim() };
	}
}

export function ConnectWorkerForm({ platforms, onClose }: { platforms: WorkerPlatform[]; onClose(): void }) {
	const { t } = useTranslation("byoworker");
	const [draft, setDraft] = useState<ConnectDraft>({
		platform: platforms[0]?.id ?? "vm",
		name: "",
		fields: { namespace: "heimdall" },
		kubeMode: "kubeconfig",
	});
	const [create, state] = useCreateWorkerConnectionMutation();
	const [act, actState] = useWorkerConnectionActionMutation();
	const [created, setCreated] = useState<WorkerConnectionCreated | null>(null);
	const base = useId();
	const set = (key: string, value: string) => setDraft((d) => ({ ...d, fields: { ...d.fields, [key]: value } }));
	const text = (key: string, label: string, hint?: string, type = "text") => (
		<Field label={label} hint={hint} htmlFor={`${base}-${key}`}>
			<input
				id={`${base}-${key}`}
				type={type}
				autoComplete="off"
				className={inputClass}
				value={draft.fields[key] ?? ""}
				onChange={(e) => set(key, e.target.value)}
				data-testid={`byo-form-${key}`}
			/>
		</Field>
	);
	const area = (key: string, label: string, hint?: string) => (
		<Field label={label} hint={hint} htmlFor={`${base}-${key}`}>
			<textarea
				id={`${base}-${key}`}
				spellCheck={false}
				className={areaClass}
				value={draft.fields[key] ?? ""}
				onChange={(e) => set(key, e.target.value)}
				data-testid={`byo-form-${key}`}
			/>
		</Field>
	);
	const submit = async () => {
		const res = await create(draftToCreate(draft));
		if ("data" in res && res.data) setCreated(res.data);
	};

	if (created) {
		const c = created.connection;
		return (
			<div className="bg-card flex min-w-0 flex-col gap-3 rounded-xl border p-5" data-testid="byo-created">
				<p className="text-foreground text-sm font-medium">{t("form.connected", { name: c.name })}</p>
				{created.install ? (
					<>
						<p className="text-sm font-medium">{t("form.installTitle")}</p>
						<CopyCodeBlock code={created.install} label={t("form.installTitle")} testId="byo-created-install" />
						<p className="text-muted-foreground text-xs">{t("form.installHint")}</p>
					</>
				) : (
					<p className="text-muted-foreground text-sm">{t("form.startHint")}</p>
				)}
				{actState.isError && (
					<p className="text-chart-error-ink text-sm" role="alert">
						{apiError(actState.error, "")}
					</p>
				)}
				<div className="flex gap-2">
					{!created.install && !actState.isSuccess && (
						<Button
							size="sm"
							disabled={actState.isLoading}
							onClick={() => void act({ id: c.id, action: "start" })}
							data-testid="byo-created-start"
						>
							{actState.isLoading ? t("actions.working") : t("form.startNow")}
						</Button>
					)}
					<Button size="sm" variant="outline" onClick={onClose} data-testid="byo-created-done">
						{t("form.done")}
					</Button>
				</div>
			</div>
		);
	}

	const p = draft.platform;
	return (
		<form
			className="bg-card flex min-w-0 flex-col gap-4 rounded-xl border p-5"
			data-testid="byo-form"
			onSubmit={(e) => {
				e.preventDefault();
				void submit();
			}}
		>
			<h3 className="text-base font-semibold">{t("form.title")}</h3>
			<fieldset className="flex min-w-0 flex-col gap-1.5">
				<legend className="mb-1.5 text-sm font-medium">{t("form.platform")}</legend>
				<div className="flex flex-wrap gap-2" role="radiogroup">
					{platforms.map((pl) => (
						<button
							key={pl.id}
							type="button"
							role="radio"
							aria-checked={p === pl.id}
							onClick={() => setDraft((d) => ({ ...d, platform: pl.id }))}
							className={cn(
								"rounded-lg border px-3 py-1.5 text-sm transition-colors",
								p === pl.id ? "border-primary bg-primary/5 text-foreground font-medium" : "text-muted-foreground hover:border-ring/50",
							)}
							data-testid={`byo-form-platform-${pl.id}`}
						>
							{pl.title}
						</button>
					))}
				</div>
			</fieldset>
			<div className="grid min-w-0 grid-cols-1 gap-4 md:grid-cols-2">
				<Field label={t("form.name")} hint={t("form.nameHint")} htmlFor={`${base}-name`}>
					<input
						id={`${base}-name`}
						className={inputClass}
						value={draft.name}
						onChange={(e) => setDraft((d) => ({ ...d, name: e.target.value }))}
						data-testid="byo-form-name"
						required
					/>
				</Field>
				{p === "runloop" && (
					<>
						{text("secret", t("runloop.secret"), t("form.secretHint"), "password")}
						<Field label={t("runloop.size")} htmlFor={`${base}-size`}>
							<select
								id={`${base}-size`}
								className={inputClass}
								value={draft.fields.size ?? "X_LARGE"}
								onChange={(e) => set("size", e.target.value)}
								data-testid="byo-form-size"
							>
								{["MEDIUM", "LARGE", "X_LARGE", "XX_LARGE"].map((s) => (
									<option key={s}>{s}</option>
								))}
							</select>
						</Field>
						{text("blueprint", t("runloop.blueprint"), t("runloop.blueprintHint"))}
						{text("hours", t("runloop.hours"), undefined, "number")}
					</>
				)}
				{p === "fly" && (
					<>
						{text("app", t("fly.app"), t("fly.appHint"))}
						{text("secret", t("fly.secret"), t("fly.secretHint"), "password")}
						{text("region", t("fly.region"))}
						{text("cpus", t("fly.cpus"), undefined, "number")}
						{text("memory_mb", t("fly.memory"), undefined, "number")}
						<Field label={t("fly.cpuKind")} htmlFor={`${base}-cpu_kind`}>
							<select
								id={`${base}-cpu_kind`}
								className={inputClass}
								value={draft.fields.cpu_kind ?? "performance"}
								onChange={(e) => set("cpu_kind", e.target.value)}
							>
								<option>performance</option>
								<option>shared</option>
							</select>
						</Field>
						{text("image", t("fly.image"))}
					</>
				)}
				{p === "kubernetes" && (
					<>
						{text("namespace", t("kubernetes.namespace"))}
						{text("image", t("kubernetes.image"), t("kubernetes.imageHint"))}
					</>
				)}
			</div>
			{p === "kubernetes" && (
				<div className="flex min-w-0 flex-col gap-4">
					<div className="bg-muted/40 rounded-lg border px-4 py-3 text-sm" data-testid="byo-form-manifest">
						<p className="font-medium">{t("kubernetes.manifest")}</p>
						<p className="text-muted-foreground text-xs">{t("kubernetes.manifestHint")}</p>
						<a
							className="text-primary text-sm font-medium underline-offset-4 hover:underline"
							href={kubernetesManifestUrl(draft.fields.namespace || "heimdall")}
							download={`heimdall-${draft.fields.namespace || "heimdall"}.yaml`}
						>
							{t("kubernetes.manifestLink")}
						</a>
					</div>
					<div className="flex gap-2" role="radiogroup" aria-label={t("kubernetes.mode")}>
						{(["kubeconfig", "token"] as const).map((m) => (
							<button
								key={m}
								type="button"
								role="radio"
								aria-checked={draft.kubeMode === m}
								onClick={() => setDraft((d) => ({ ...d, kubeMode: m }))}
								className={cn(
									"rounded-lg border px-3 py-1 text-xs",
									draft.kubeMode === m ? "border-primary bg-primary/5 font-medium" : "text-muted-foreground",
								)}
								data-testid={`byo-form-kube-${m}`}
							>
								{t(`kubernetes.${m}`)}
							</button>
						))}
					</div>
					{draft.kubeMode === "kubeconfig" ? (
						area("kubeconfig", t("kubernetes.kubeconfig"), t("kubernetes.kubeconfigHint"))
					) : (
						<div className="grid min-w-0 grid-cols-1 gap-4 md:grid-cols-2">
							{text("server", t("kubernetes.server"))}
							{text("token", t("kubernetes.saToken"), t("form.secretHint"), "password")}
							<div className="md:col-span-2">{area("ca", t("kubernetes.ca"))}</div>
						</div>
					)}
				</div>
			)}
			{p === "vm" && <p className="text-muted-foreground text-sm">{t("form.vmHint")}</p>}
			{state.isError && (
				<p className="text-chart-error-ink text-sm" role="alert" data-testid="byo-form-error">
					{apiError(state.error, "")}
				</p>
			)}
			<div className="flex gap-2">
				<Button type="submit" size="sm" disabled={state.isLoading || draft.name.trim() === ""} data-testid="byo-form-submit">
					{state.isLoading ? t("form.submitting") : t("form.submit")}
				</Button>
				<Button type="button" size="sm" variant="outline" onClick={onClose}>
					{t("cancel")}
				</Button>
			</div>
		</form>
	);
}