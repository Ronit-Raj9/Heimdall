import "@/components/platform/fw23I18n";
import { useConfirm } from "@/components/platform/confirmDialog";
import { JobRunButton } from "@/components/jobs/jobRunButton";
import { EmptyState } from "@/components/shell/emptyState";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { useGetAgentsQuery, useGetSuitesQuery, useListExperimentsQuery } from "@/lib/store/apis/benchApi";
import {
	apiErrorMessage,
	exportDownloadUrl,
	useCreateWebhookMutation,
	useDeleteExportMutation,
	useDeleteWebhookMutation,
	useGetCIWorkflowQuery,
	useListExportsQuery,
	useListWebhooksQuery,
	useTestWebhookMutation,
	useUpdateWebhookMutation,
} from "@/lib/store/apis/integrationsApi";
import { useGetGithubStatusQuery } from "@/lib/store/apis/githubApi";
import { DELIVERY_POLL_MS, useListDeliveriesQuery, useRetryDeliveryMutation } from "@/lib/store/apis/dataOpsApi";
import { canUse, useSession } from "@/lib/session/sessionContext";
import type { GithubStatus } from "@/lib/types/github";
import type {
	IntegrationExport,
	IntegrationExportFormat,
	IntegrationsExportParams,
	Webhook,
	WebhookDelivery,
	WebhookEventType,
	WebhookFormat,
} from "@/lib/types/integrations";
import { PageHeader } from "@/components/shell/pageHeader";
import { Skeleton } from "@/components/ui/skeleton";
import { cn } from "@/lib/utils";
import { Activity, Code2, Download, Network, FileDown, GitPullRequest, Webhook as WebhookIcon, type LucideIcon } from "lucide-react";
import { useEffect, useId, useState } from "react";
import { EmailPanel } from "./emailPanel";
import { MaximPanel } from "./maximPanel";
import { McpInstallPanel } from "@/components/connect/mcpInstall"; // Z02: Add to Cursor / Claude Code
import { BifrostUsePanel, bifrostUseStatus } from "@/components/bifrost/bifrostUse";
import { useGetBifrostUseQuery } from "@/lib/store/apis/bifrostUseApi";
import { useGetMaximQuery } from "@/lib/store/apis/traceToolsApi";
import { useTranslation } from "react-i18next";
import "@/app/experiments/views/experimentsI18n";
import { exportPickerGroups, type ExperimentPickerOption } from "@/components/experiments/experiments.utils";
import { useDriftMonitorNames } from "@/components/experiments/useDriftMonitorNames";
import { useEvaluationNames } from "@/components/experiments/useEvaluationNames";

declare module "@/lib/i18n/resources" {
	interface LaneResources {
		integrations: typeof import("@/locales/en/integrations.json");
	}
}

const FORMATS: IntegrationExportFormat[] = ["otlp", "eee", "csv", "jsonl", "harbor"];
const EVENTS: WebhookEventType[] = [
	"evaluation.finished",
	"evaluation.regressed",
	"release.blocked",
	"schedule.failed",
	"worker.offline",
	"experiment.finished",
	"job.failed",
	"monitor.incident",
];
const HOOK_FORMATS: WebhookFormat[] = ["slack", "teams", "pagerduty", "json"];

/** Events a hook of format f can take (webhooks/format.go EventsFor): PagerDuty pages only on problems. */
export function eventsFor(f: WebhookFormat): WebhookEventType[] {
	if (f === "pagerduty")
		return ["evaluation.regressed", "release.blocked", "schedule.failed", "worker.offline", "job.failed", "monitor.incident"];
	return EVENTS;
}

/** What a new hook of format f starts subscribed to (webhooks/format.go DefaultEvents). */
export function defaultEventsFor(f: WebhookFormat): WebhookEventType[] {
	if (f === "pagerduty") return ["evaluation.regressed"];
	if (f === "slack" || f === "teams") return ["evaluation.finished", "evaluation.regressed", "release.blocked"];
	return ["evaluation.finished"];
}

/**
 * What is wrong with a web address before it is sent (a fw23 locale key under webhooks), or null.
 * A hosted server delivers only to https (webhooks.ValidateURL); a local one also takes http.
 */
export function webhookUrlProblem(url: string, hosted: boolean): "httpsOnly" | null {
	const trimmed = url.trim();
	if (!hosted || trimmed === "") return null;
	return /^https:\/\//i.test(trimmed) ? null : "httpsOnly";
}

/** Whether the add form has what a hook of format f needs. */
export function webhookFormReady(
	f: WebhookFormat,
	url: string,
	routingKey: string,
	events: readonly WebhookEventType[],
	hosted = false,
): boolean {
	if (events.length === 0) return false;
	if (f === "pagerduty") return /^[A-Za-z0-9]{20,64}$/.test(routingKey.trim());
	return url.trim() !== "" && webhookUrlProblem(url, hosted) === null;
}

/** The web address field of the add form, with the address check right under it. */
export function WebhookUrlField({
	format,
	url,
	hosted,
	onChange,
}: {
	format: WebhookFormat;
	url: string;
	hosted: boolean;
	onChange(url: string): void;
}) {
	const { t } = useTranslation("integrations");
	const { t: tf } = useTranslation("fw23");
	const problem = webhookUrlProblem(url, hosted);
	const hintId = useId();
	return (
		<div className="flex flex-col gap-1">
			<Input
				aria-label={t(`webhooks.urlLabels.${format}`)}
				placeholder={t(`webhooks.urlPlaceholders.${format}`)}
				value={url}
				onChange={(e) => onChange(e.target.value)}
				aria-invalid={problem !== null}
				aria-describedby={problem ? hintId : undefined}
				data-testid="webhook-url"
			/>
			{problem && (
				<p id={hintId} className="text-chart-error-ink text-xs" data-testid="webhook-url-problem">
					{tf(`webhooks.${problem}`)}
				</p>
			)}
		</div>
	);
}
const selectClass = "border-input bg-background h-9 w-full rounded-md border px-2 text-sm";

/** Integrations (web parity L19): CI workflow (install PR via Settings > GitHub), exports with download, experiment-finished webhooks. */
type IntegrationTab = "webhooks" | "exports" | "ci" | "maxim" | "ide" | "bifrost";

const TAB_ICONS: Record<IntegrationTab, LucideIcon> = {
	webhooks: WebhookIcon,
	exports: FileDown,
	ci: GitPullRequest,
	maxim: Activity,
	ide: Code2,
	bifrost: Network,
};

type Tone = "ok" | "warn" | "bad" | "idle";

const TONE_PILL: Record<Tone, string> = {
	ok: "bg-emerald-500/10 text-emerald-700 dark:text-emerald-400",
	warn: "bg-amber-500/10 text-amber-700 dark:text-amber-400",
	bad: "bg-red-500/10 text-red-700 dark:text-red-400",
	idle: "bg-muted text-muted-foreground",
};

const TONE_DOT: Record<Tone, string> = { ok: "bg-emerald-500", warn: "bg-amber-500", bad: "bg-red-500", idle: "bg-muted-foreground/50" };

/** A small status pill: colored dot plus a few words. */
export function StatusDot({ tone, label }: { tone: Tone; label: string }) {
	return (
		<span className={cn("inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 text-xs", TONE_PILL[tone])}>
			<span className={cn("size-1.5 rounded-full", TONE_DOT[tone])} aria-hidden="true" />
			{label}
		</span>
	);
}

/** Integrations (web parity L19): CI workflow (install PR via Settings > GitHub), exports with download, experiment-finished webhooks. */
export default function IntegrationsView() {
	const { t } = useTranslation("integrations");
	const [tab, setTab] = useState<IntegrationTab>("webhooks");
	const hooks = useListWebhooksQuery();
	const exportsList = useListExportsQuery();
	const gh = useGetGithubStatusQuery();
	const maxim = useGetMaximQuery();
	const bifrost = useGetBifrostUseQuery();
	const { t: tt } = useTranslation("traceTools");
	const { t: tc } = useTranslation("cliConnect");
	const { t: tb } = useTranslation("bifrostUse");

	const status = (k: IntegrationTab): { tone: Tone; label: string } | null => {
		if (k === "webhooks") {
			if (!hooks.data) return null;
			const live = hooks.data.webhooks.filter((h) => h.enabled);
			if (live.some((h) => h.last_delivery && !h.last_delivery.ok)) return { tone: "bad", label: t("cards.webhooks.failing") };
			return live.length
				? { tone: "ok", label: t("cards.webhooks.count", { count: live.length }) }
				: { tone: "idle", label: t("cards.webhooks.none") };
		}
		if (k === "exports") {
			if (!exportsList.data) return null;
			const n = exportsList.data.exports.length;
			return n ? { tone: "ok", label: t("cards.exports.count", { count: n }) } : { tone: "idle", label: t("cards.exports.none") };
		}
		if (k === "ide") return { tone: "idle", label: tc("ide.status") };
		if (k === "bifrost") {
			if (bifrost.isLoading) return null;
			const b = bifrostUseStatus(bifrost.data);
			return { tone: b.tone, label: b.count ? `${b.count} ${tb("state.on").toLowerCase()}` : tb("state.off") };
		}
		if (k === "maxim") {
			if (maxim.isLoading) return null;
			if (!maxim.data?.connected) return { tone: "idle", label: tt("maxim.notConnected") };
			return maxim.data.last_error ? { tone: "warn", label: tt("maxim.connected") } : { tone: "ok", label: tt("maxim.connected") };
		}
		if (gh.isLoading) return null;
		const step = ciNextStep(gh.data, gh.isError);
		if (step === "install") return { tone: "ok", label: t("cards.ci.connected") };
		if (step === "connect") return { tone: "warn", label: t("cards.ci.notConnected") };
		return { tone: "idle", label: t("cards.ci.manual") };
	};

	return (
		<div className="mx-auto flex w-full max-w-[1200px] flex-col gap-6 p-6" data-testid="integrations-view">
			<PageHeader title={t("title")} subtitle={t("subtitle")} />
			<div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-6" role="tablist" aria-label={t("title")}>
				{(["webhooks", "exports", "ci", "maxim", "ide", "bifrost"] as const).map((k) => {
					const Icon = TAB_ICONS[k];
					const active = k === tab;
					const st = status(k);
					return (
						<button
							key={k}
							type="button"
							role="tab"
							aria-selected={active}
							onClick={() => setTab(k)}
							className={cn(
								"bg-card focus-visible:ring-ring/60 flex flex-col gap-3 rounded-xl border p-5 text-left transition-all hover:shadow-md focus-visible:ring-2 focus-visible:outline-none",
								active ? "border-primary ring-primary/30 ring-1" : "hover:border-foreground/20",
							)}
							data-testid={`integrations-tab-${k}`}
						>
							<div className="flex items-center justify-between gap-2">
								<span
									className={cn(
										"flex size-9 items-center justify-center rounded-lg",
										active ? "bg-primary text-primary-foreground" : "bg-muted text-muted-foreground",
									)}
								>
									<Icon className="size-4.5" aria-hidden="true" />
								</span>
								{st ? <StatusDot tone={st.tone} label={st.label} /> : <Skeleton className="h-5 w-20 rounded-full" />}
							</div>
							<div className="flex flex-col gap-0.5">
								<span className="text-base font-semibold">
									{k === "ide" ? tc("ide.tab") : k === "maxim" ? tt("maxim.tab") : k === "bifrost" ? tb("tab") : t(`tabs.${k}`)}
								</span>
								<span className="text-muted-foreground line-clamp-2 text-sm">
									{k === "ide" ? tc("ide.desc") : k === "maxim" ? tt("maxim.desc") : k === "bifrost" ? tb("desc") : t(`cards.${k}.desc`)}
								</span>
							</div>
						</button>
					);
				})}
			</div>
			<div role="tabpanel">
				{tab === "webhooks" && (
					<div className="flex flex-col gap-6">
						<WebhooksPanel />
						<EmailPanel />
						<DeliveryLogPanel />
					</div>
				)}
				{tab === "exports" && <ExportsPanel />}
				{tab === "ci" && <CIPanel />}
				{tab === "maxim" && <MaximPanel />}
				{tab === "ide" && <McpInstallPanel />}
				{tab === "bifrost" && <BifrostUsePanel />}
			</div>
		</div>
	);
}

function Section({ title, intro, children }: { title: string; intro?: string; children: React.ReactNode }) {
	return (
		<section className="border-border bg-card flex flex-col gap-4 rounded-xl border p-5">
			<div className="flex flex-col gap-0.5">
				<h2 className="text-base font-semibold">{title}</h2>
				{intro && <p className="text-muted-foreground text-sm">{intro}</p>}
			</div>
			{children}
		</section>
	);
}

function ErrorNote({ message, testId }: { message: string; testId?: string }) {
	return (
		<p className="text-chart-error-ink text-sm" role="alert" data-testid={testId ?? "integrations-error"}>
			{message}
		</p>
	);
}

// ----------------------------------------------------------------------------------------------
// Webhooks

function WebhooksPanel() {
	const { t } = useTranslation("integrations");
	const confirm = useConfirm();
	const { data, error, isLoading } = useListWebhooksQuery();
	const [createHook, create] = useCreateWebhookMutation();
	const [updateHook] = useUpdateWebhookMutation();
	const [deleteHook] = useDeleteWebhookMutation();
	const [testHook] = useTestWebhookMutation();
	const [url, setUrl] = useState("");
	const [desc, setDesc] = useState("");
	const [format, setFormat] = useState<WebhookFormat>("slack");
	const [routingKey, setRoutingKey] = useState("");
	const [events, setEvents] = useState<WebhookEventType[]>(defaultEventsFor("slack"));
	const [secret, setSecret] = useState<string | null>(null);
	const [message, setMessage] = useState<string | null>(null);
	const [tests, setTests] = useState<Record<string, WebhookDelivery>>({});

	const fail = (err: unknown) =>
		setMessage((err as { status?: number })?.status === 403 ? t("errors.adminOnly") : apiErrorMessage(err, t("errors.generic")));

	const add = async () => {
		setMessage(null);
		const res = await createHook({
			url: url.trim() || undefined,
			events,
			description: desc.trim() || undefined,
			format,
			routing_key: format === "pagerduty" ? routingKey.trim() : undefined,
		});
		if ("error" in res) return fail(res.error);
		if (format === "json") setSecret(res.data.secret);
		setUrl("");
		setDesc("");
		setRoutingKey("");
	};

	return (
		<Section title={t("webhooks.heading")} intro={t("webhooks.intro")}>
			{data?.hosted && <p className="text-muted-foreground text-xs">{t("webhooks.hostedNote")}</p>}
			<details className="text-muted-foreground text-xs">
				<summary className="hover:text-foreground cursor-pointer">{t("webhooks.signingTitle")}</summary>
				<p className="mt-1 font-mono break-words">{t("webhooks.signing")}</p>
			</details>
			<form
				className="grid gap-2 sm:grid-cols-[2fr_1fr_auto]"
				onSubmit={(e) => {
					e.preventDefault();
					void add();
				}}
			>
				<label className="flex flex-col gap-1 text-sm sm:col-span-3">
					<span className="text-muted-foreground text-xs font-medium">{t("webhooks.format")}</span>
					<select
						className={cn(selectClass, "sm:max-w-xs")}
						value={format}
						onChange={(e) => {
							const f = e.target.value as WebhookFormat;
							setFormat(f);
							setEvents(defaultEventsFor(f));
						}}
						data-testid="webhook-format"
					>
						{HOOK_FORMATS.map((f) => (
							<option key={f} value={f}>
								{t(`webhooks.formats.${f}`)}
							</option>
						))}
					</select>
					<span className="text-muted-foreground text-xs" data-testid="webhook-format-help">
						{t(`webhooks.formatHelp.${format}`)}
					</span>
				</label>
				{format === "pagerduty" && (
					<Input
						className="sm:col-span-3"
						aria-label={t("webhooks.routingKey")}
						placeholder={t("webhooks.routingKeyPlaceholder")}
						value={routingKey}
						onChange={(e) => setRoutingKey(e.target.value)}
						autoComplete="off"
						data-testid="webhook-routing-key"
					/>
				)}
				<WebhookUrlField format={format} url={url} hosted={data?.hosted === true} onChange={setUrl} />
				<Input
					aria-label={t("webhooks.description")}
					placeholder={t("webhooks.description")}
					value={desc}
					onChange={(e) => setDesc(e.target.value)}
				/>
				<Button
					type="submit"
					disabled={!webhookFormReady(format, url, routingKey, events, data?.hosted === true) || create.isLoading}
					data-testid="webhook-add"
				>
					{create.isLoading ? t("webhooks.adding") : t("webhooks.add")}
				</Button>
				<fieldset className="flex flex-wrap gap-4 text-sm sm:col-span-3">
					<legend className="sr-only">{t("webhooks.events")}</legend>
					{eventsFor(format).map((ev) => (
						<label key={ev} className="flex items-center gap-1.5">
							<input
								type="checkbox"
								checked={events.includes(ev)}
								onChange={(e) => setEvents((cur) => (e.target.checked ? [...cur, ev] : cur.filter((x) => x !== ev)))}
							/>
							{t(`webhooks.eventNames.${ev}`)}
						</label>
					))}
				</fieldset>
			</form>
			{message && <ErrorNote message={message} testId="webhook-error" />}
			{secret && (
				<div
					className="border-chart-warning/60 bg-chart-warning/10 flex flex-wrap items-center gap-2 rounded-md border p-3 text-sm"
					data-testid="webhook-secret"
				>
					<span>{t("webhooks.secretOnce")}</span>
					<code className="bg-background rounded px-1.5 py-0.5 font-mono text-xs select-all">{secret}</code>
					<Button size="sm" variant="outline" onClick={() => setSecret(null)}>
						{t("webhooks.dismiss")}
					</Button>
				</div>
			)}
			{error ? (
				<ErrorNote message={apiErrorMessage(error, t("errors.load"))} />
			) : isLoading ? null : (
				<WebhookList
					hooks={data?.webhooks ?? []}
					tests={tests}
					onTest={async (id) => {
						const res = await testHook(id);
						if ("data" in res && res.data) setTests((cur) => ({ ...cur, [id]: res.data }));
						else if ("error" in res) fail(res.error);
					}}
					onToggle={async (h) => {
						const res = await updateHook({ id: h.id, body: { enabled: !h.enabled } });
						if ("error" in res) fail(res.error);
					}}
					onDelete={async (h) => {
						if (!(await confirm(t("webhooks.deleteConfirm", { url: h.url })))) return;
						const res = await deleteHook(h.id);
						if ("error" in res) fail(res.error);
					}}
				/>
			)}
		</Section>
	);
}

export interface WebhookListProps {
	hooks: Webhook[];
	tests?: Record<string, WebhookDelivery>;
	onTest(id: string): void;
	onToggle(h: Webhook): void;
	onDelete(h: Webhook): void;
}

/** The org's webhooks with their last delivery and actions. */
export function WebhookList({ hooks, tests = {}, onTest, onToggle, onDelete }: WebhookListProps) {
	const { t } = useTranslation("integrations");
	if (hooks.length === 0) return <EmptyState compact message={t("webhooks.empty")} testId="webhooks-empty" />;
	return (
		<ul className="divide-border flex flex-col divide-y rounded-lg border" data-testid="webhook-list">
			{hooks.map((h) => {
				const last = h.last_delivery;
				const test = tests[h.id];
				return (
					<li key={h.id} className="hover:bg-muted/30 flex flex-col gap-1.5 px-4 py-3 transition-colors" data-testid={`webhook-${h.id}`}>
						<div className="flex flex-wrap items-center gap-2">
							<WebhookIcon className="text-muted-foreground size-4 shrink-0" aria-hidden="true" />
							<Badge variant="outline" data-testid={`webhook-format-${h.id}`}>
								{t(`webhooks.formats.${h.format ?? "json"}`)}
							</Badge>
							<span className="min-w-0 truncate font-mono text-sm" title={h.url}>
								{h.url}
							</span>
							<Badge variant={h.enabled ? "success" : "secondary"}>{h.enabled ? t("webhooks.enabled") : t("webhooks.disabled")}</Badge>
							{last ? (
								<Badge variant={last.ok ? "success" : "destructive"} title={last.error}>
									{last.ok ? t("webhooks.lastOk") : t("webhooks.lastFail")}
								</Badge>
							) : (
								<Badge variant="outline">{t("webhooks.never")}</Badge>
							)}
						</div>
						<div className="text-muted-foreground text-xs">
							{h.events.map((e) => t(`webhooks.eventNames.${e}`)).join(", ")}
							{(h.format ?? "json") === "json" ? ` · whsec_...${h.secret_last4}` : ""}
							{h.routing_key_last4 ? ` · ${t("webhooks.keyEnding", { last4: h.routing_key_last4 })}` : ""}
							{h.description ? ` · ${h.description}` : ""}
						</div>
						<div className="flex flex-wrap gap-2">
							<Button size="sm" variant="outline" onClick={() => onTest(h.id)} data-testid={`webhook-test-${h.id}`}>
								{t("webhooks.test")}
							</Button>
							<Button size="sm" variant="outline" onClick={() => onToggle(h)}>
								{h.enabled ? t("webhooks.disable") : t("webhooks.enable")}
							</Button>
							<Button size="sm" variant="ghost" onClick={() => onDelete(h)}>
								{t("webhooks.delete")}
							</Button>
						</div>
						{test && (
							<p
								className={test.ok ? "text-chart-success-ink text-xs" : "text-chart-error-ink text-xs"}
								data-testid={`webhook-test-result-${h.id}`}
							>
								{test.ok
									? t("webhooks.testOk", { code: test.status_code })
									: t("webhooks.testFail", { error: test.error ?? `HTTP ${test.status_code}` })}
							</p>
						)}
					</li>
				);
			})}
		</ul>
	);
}

// ----------------------------------------------------------------------------------------------
// Delivery log

type DeliveryState = NonNullable<WebhookDelivery["state"]>;

/** A row's state: the server's, or derived from ok for a server that does not send one. */
export function deliveryState(d: WebhookDelivery): DeliveryState {
	return d.state ?? (d.ok ? "delivered" : "failed");
}

/** True while any delivery waits for another attempt, so the log keeps refreshing. */
export function anyRetrying(deliveries: readonly WebhookDelivery[]): boolean {
	return deliveries.some((d) => deliveryState(d) === "retrying");
}

const STATE_BADGE: Record<DeliveryState, "success" | "warning" | "destructive"> = {
	delivered: "success",
	retrying: "warning",
	failed: "destructive",
};

function DeliveryLogPanel() {
	const { t } = useTranslation("integrations");
	const session = useSession();
	const [polling, setPolling] = useState(false);
	const { data, error, isLoading } = useListDeliveriesQuery(100, { pollingInterval: polling ? DELIVERY_POLL_MS : 0 });
	const hooks = useListWebhooksQuery();
	const [retry] = useRetryDeliveryMutation();
	const [message, setMessage] = useState<string | null>(null);
	const [queued, setQueued] = useState<Record<string, boolean>>({});
	const deliveries = data?.deliveries ?? [];
	const retrying = anyRetrying(deliveries);
	useEffect(() => setPolling(retrying), [retrying]);
	const hookUrls = Object.fromEntries((hooks.data?.webhooks ?? []).map((h) => [h.id, h.url]));
	return (
		<Section title={t("deliveryLog.heading")} intro={t("deliveryLog.intro")}>
			{message && <ErrorNote message={message} testId="delivery-error" />}
			{error ? (
				<ErrorNote message={apiErrorMessage(error, t("errors.load"))} />
			) : isLoading ? (
				<Skeleton className="h-24 w-full" />
			) : (
				<DeliveryTable
					deliveries={deliveries}
					hookUrls={hookUrls}
					canRetry={canUse(session, "member")}
					queued={queued}
					onRetry={async (id) => {
						setMessage(null);
						setQueued((cur) => ({ ...cur, [id]: true }));
						const res = await retry(id);
						if ("error" in res) {
							setQueued((cur) => ({ ...cur, [id]: false }));
							setMessage(apiErrorMessage(res.error, t("errors.generic")));
						}
					}}
				/>
			)}
		</Section>
	);
}

export interface DeliveryTableProps {
	deliveries: WebhookDelivery[];
	hookUrls?: Record<string, string>;
	canRetry: boolean;
	queued?: Record<string, boolean>;
	onRetry(id: string): void;
}

/** Webhook deliveries, newest first, with Retry on failed rows. */
export function DeliveryTable({ deliveries, hookUrls = {}, canRetry, queued = {}, onRetry }: DeliveryTableProps) {
	const { t } = useTranslation("integrations");
	if (deliveries.length === 0) return <EmptyState compact message={t("deliveryLog.empty")} testId="deliveries-empty" />;
	const when = (at?: string) => (at ? new Date(at).toLocaleString() : "");
	return (
		<div className="overflow-x-auto rounded-lg border">
			<table className="w-full text-sm" data-testid="delivery-table">
				<thead className="bg-muted/50 text-muted-foreground text-left text-xs">
					<tr>
						<th className="px-3 py-2 font-medium">{t("deliveryLog.time")}</th>
						<th className="px-3 font-medium">{t("deliveryLog.event")}</th>
						<th className="px-3 font-medium">{t("deliveryLog.webhook")}</th>
						<th className="px-3 font-medium">{t("deliveryLog.state")}</th>
						<th className="px-3 text-right font-medium">{t("deliveryLog.status")}</th>
						<th className="px-3 text-right font-medium">{t("deliveryLog.attempts")}</th>
						<th className="px-3 font-medium">{t("deliveryLog.next")}</th>
						<th className="px-3 font-medium">{t("deliveryLog.error")}</th>
						<th />
					</tr>
				</thead>
				<tbody>
					{deliveries.map((d) => {
						const state = deliveryState(d);
						const url = hookUrls[d.hook_id];
						return (
							<tr
								key={d.id}
								className="border-border hover:bg-muted/30 border-t transition-colors"
								data-testid={`delivery-${d.id}`}
								data-state={state}
							>
								<td className="text-muted-foreground px-3 py-2 text-xs whitespace-nowrap">{when(d.at)}</td>
								<td className="px-3 text-xs whitespace-nowrap">{d.event}</td>
								<td className="max-w-[14rem] truncate px-3 font-mono text-xs" title={url ?? d.hook_id}>
									{url ?? t("deliveryLog.unknownHook")}
								</td>
								<td className="px-3">
									<Badge variant={STATE_BADGE[state]}>{t(`deliveryLog.states.${state}`)}</Badge>
								</td>
								<td className="px-3 text-right tabular-nums">{d.status_code ?? ""}</td>
								<td className="px-3 text-right tabular-nums">{d.attempts}</td>
								<td className="text-muted-foreground px-3 text-xs whitespace-nowrap">
									{state === "retrying" ? when(d.next_attempt_at) : ""}
								</td>
								<td className="text-chart-error-ink max-w-[16rem] truncate px-3 text-xs" title={d.error}>
									{d.error ?? ""}
								</td>
								<td className="px-3 py-1.5 text-right">
									{state === "failed" && canRetry && (
										<Button
											size="sm"
											variant="outline"
											disabled={queued[d.id]}
											onClick={() => onRetry(d.id)}
											data-testid={`delivery-retry-${d.id}`}
										>
											{queued[d.id] ? t("deliveryLog.retrying") : t("deliveryLog.retry")}
										</Button>
									)}
								</td>
							</tr>
						);
					})}
				</tbody>
			</table>
		</div>
	);
}

// ----------------------------------------------------------------------------------------------
// Exports

/** The job params of an export form, or null while it is incomplete. */
export function exportParams(
	format: IntegrationExportFormat,
	expId: string,
	suite: string,
	payloads: boolean,
	allowSynthetic: boolean,
): IntegrationsExportParams | null {
	if (format === "harbor") return suite ? { format, suite } : null;
	if (!expId) return null;
	const p: IntegrationsExportParams = { format, exp_id: expId };
	if (format === "otlp" && payloads) p.payloads = true;
	if (format === "eee" && allowSynthetic) p.allow_synthetic = true;
	return p;
}

/**
 * The Download results panel. The comparison picker groups its choices (Evaluations, Comparisons,
 * and checks, practice and debug runs behind a box) with readable names; the raw id is the
 * option's tooltip. showHiddenAtStart ticks the box on first render.
 */
export function ExportsPanel({ showHiddenAtStart = false }: { showHiddenAtStart?: boolean } = {}) {
	const { t } = useTranslation("integrations");
	const { t: tx } = useTranslation("experimentsUx");
	const evalNames = useEvaluationNames();
	const monitors = useDriftMonitorNames();
	const [showHidden, setShowHidden] = useState(showHiddenAtStart);
	const confirm = useConfirm();
	const exps = useListExperimentsQuery();
	const suites = useGetSuitesQuery();
	const files = useListExportsQuery(undefined, { pollingInterval: 5000 });
	const [deleteExport] = useDeleteExportMutation();
	const [format, setFormat] = useState<IntegrationExportFormat>("otlp");
	const [expId, setExpId] = useState("");
	const [suite, setSuite] = useState("");
	const [payloads, setPayloads] = useState(false);
	const [allowSynthetic, setAllowSynthetic] = useState(false);
	const [message, setMessage] = useState<string | null>(null);
	const params = exportParams(format, expId, suite, payloads, allowSynthetic);
	const experiments = exps.data?.experiments ?? [];
	const groups = exportPickerGroups(experiments, (k, o) => String(tx(k as never, (o ?? {}) as never)), evalNames, showHidden, monitors);
	const optionText = (o: ExperimentPickerOption) => `${o.label} · ${o.state ? tx(`status.${o.state}`, { defaultValue: o.state }) : ""}`;

	return (
		<div className="flex flex-col gap-4">
			<Section title={t("exports.heading")} intro={t("exports.intro")}>
				<div className="grid gap-3 sm:grid-cols-2">
					<label className="flex flex-col gap-1 text-sm">
						{t("exports.format")}
						<select
							className={selectClass}
							value={format}
							onChange={(e) => setFormat(e.target.value as IntegrationExportFormat)}
							data-testid="export-format"
						>
							{FORMATS.map((f) => (
								<option key={f} value={f}>
									{t(`exports.formats.${f}`)}
								</option>
							))}
						</select>
					</label>
					{format === "harbor" ? (
						<label className="flex flex-col gap-1 text-sm">
							{t("exports.suite")}
							<select className={selectClass} value={suite} onChange={(e) => setSuite(e.target.value)} data-testid="export-suite">
								<option value="">{t("exports.pickSuite")}</option>
								{(suites.data?.suites ?? []).map((s) => (
									<option key={s.id} value={s.id}>
										{s.id}
									</option>
								))}
							</select>
						</label>
					) : (
						<div className="flex flex-col gap-1 text-sm">
							<label className="flex flex-col gap-1">
								{t("exports.experiment")}
								<select className={selectClass} value={expId} onChange={(e) => setExpId(e.target.value)} data-testid="export-experiment">
									<option value="">{t("exports.pickExperiment")}</option>
									{(
										[
											["evaluations", groups.evaluations],
											["comparisons", groups.comparisons],
											["other", groups.other],
										] as const
									).map(
										([key, options]) =>
											options.length > 0 && (
												<optgroup key={key} label={t(`exports.groups.${key}`)}>
													{options.map((o) => (
														<option key={o.id} value={o.id} title={o.id}>
															{optionText(o)}
														</option>
													))}
												</optgroup>
											),
									)}
								</select>
							</label>
							{groups.hiddenCount > 0 && (
								<label className="text-muted-foreground inline-flex items-center gap-1.5 text-xs">
									<input
										type="checkbox"
										checked={showHidden}
										onChange={(e) => {
											setShowHidden(e.target.checked);
											// A hidden choice cannot stay selected once its group is hidden again.
											if (!e.target.checked && groups.other.some((o) => o.id === expId)) setExpId("");
										}}
										className="accent-primary size-3.5"
										data-testid="export-show-hidden"
									/>
									{t("exports.showHidden", { count: groups.hiddenCount })}
								</label>
							)}
						</div>
					)}
				</div>
				{format === "otlp" && (
					<label className="flex items-center gap-1.5 text-sm">
						<input type="checkbox" checked={payloads} onChange={(e) => setPayloads(e.target.checked)} />
						{t("exports.payloads")}
					</label>
				)}
				{format === "eee" && (
					<label className="flex items-center gap-1.5 text-sm">
						<input type="checkbox" checked={allowSynthetic} onChange={(e) => setAllowSynthetic(e.target.checked)} />
						{t("exports.allowSynthetic")}
					</label>
				)}
				{format !== "harbor" && !exps.isLoading && experiments.length === 0 && (
					<EmptyState
						compact
						message={t("exports.noExperiments")}
						action={{ label: t("exports.experiment"), to: "/experiments" }}
						testId="exports-no-experiments"
					/>
				)}
				<div>
					{params ? (
						<JobRunButton kind="integrations.export" params={params} label={t("exports.run")} onDone={() => void files.refetch()} />
					) : (
						<Button disabled data-testid="export-run-disabled">
							{t("exports.run")}
						</Button>
					)}
				</div>
			</Section>
			<Section title={t("exports.files")}>
				{message && <ErrorNote message={message} />}
				{files.error ? (
					<ErrorNote message={apiErrorMessage(files.error, t("errors.load"))} />
				) : (
					<ExportTable
						exports={files.data?.exports ?? []}
						onDelete={async (name) => {
							if (!(await confirm(t("exports.deleteConfirm", { name })))) return;
							const res = await deleteExport(name);
							setMessage("error" in res ? apiErrorMessage(res.error, t("errors.generic")) : null);
						}}
					/>
				)}
			</Section>
		</div>
	);
}

function formatBytes(n: number): string {
	if (n < 1024) return `${n} B`;
	if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KiB`;
	return `${(n / 1024 / 1024).toFixed(1)} MiB`;
}

/** The export files with download links. */
export function ExportTable({ exports, onDelete }: { exports: IntegrationExport[]; onDelete(name: string): void }) {
	const { t } = useTranslation("integrations");
	if (exports.length === 0) return <EmptyState compact message={t("exports.empty")} testId="exports-empty" />;
	return (
		<div className="overflow-x-auto rounded-lg border">
			<table className="w-full text-sm" data-testid="export-table">
				<thead className="bg-muted/50 text-muted-foreground text-left text-xs">
					<tr>
						<th className="px-3 py-2 font-medium">{t("exports.name")}</th>
						<th className="px-3 font-medium">{t("exports.format")}</th>
						<th className="px-3 text-right font-medium">{t("exports.size")}</th>
						<th className="px-3 font-medium">{t("exports.created")}</th>
						<th />
					</tr>
				</thead>
				<tbody>
					{exports.map((x) => (
						<tr key={x.name} className="border-border hover:bg-muted/30 border-t transition-colors">
							<td className="max-w-[16rem] truncate px-3 py-2 font-mono text-xs" title={x.name}>
								{x.name}
							</td>
							<td className="px-3">
								<Badge variant="outline">{x.format}</Badge>
							</td>
							<td className="px-3 text-right tabular-nums">{formatBytes(x.size)}</td>
							<td className="text-muted-foreground px-3 text-xs whitespace-nowrap">{new Date(x.created_at).toLocaleString()}</td>
							<td className="flex justify-end gap-1 px-3 py-1.5">
								<Button asChild size="sm" variant="outline">
									<a href={exportDownloadUrl(x.name)} download={x.name} data-testid={`export-download-${x.name}`}>
										<Download className="size-4" aria-hidden="true" />
										{t("exports.download")}
									</a>
								</Button>
								<Button size="sm" variant="ghost" onClick={() => onDelete(x.name)}>
									{t("exports.delete")}
								</Button>
							</td>
						</tr>
					))}
				</tbody>
			</table>
		</div>
	);
}

// ----------------------------------------------------------------------------------------------
// CI

/** Where the one CI-install flow lives: Settings > GitHub (POST /github/ci-install, lane L07). */
export const GITHUB_SETTINGS_SEARCH = { tab: "github" } as const;

/** The CI tab's next step from the GitHub connection status: install from Settings, connect first, or copy by hand. */
export function ciNextStep(st: GithubStatus | undefined, failed: boolean): "install" | "connect" | "manual" {
	if (failed || !st || !st.configured) return "manual";
	return st.connected ? "install" : "connect";
}

export function CIInstallLink({ step }: { step: "install" | "connect" | "manual" }) {
	const { t } = useTranslation("integrations");
	if (step === "manual") {
		return (
			<p className="text-muted-foreground text-sm" data-testid="ci-manual">
				{t("ci.manual")}
			</p>
		);
	}
	return (
		<EmptyState
			compact
			message={step === "install" ? t("ci.installInSettings") : t("ci.notConnected")}
			action={{ label: step === "install" ? t("ci.openSettings") : t("ci.connect"), to: "/settings", search: GITHUB_SETTINGS_SEARCH }}
			testId={step === "install" ? "ci-install-link" : "ci-not-connected"}
		/>
	);
}

function CIPanel() {
	const { t } = useTranslation("integrations");
	const status = useGetGithubStatusQuery();
	const agents = useGetAgentsQuery();
	const [mode, setMode] = useState<"recorded" | "live">("recorded");
	const [agent, setAgent] = useState("");
	const [copied, setCopied] = useState(false);
	const preview = useGetCIWorkflowQuery(
		{ mode, agent: mode === "live" ? agent || undefined : undefined },
		{ skip: mode === "live" && !agent },
	);

	return (
		<Section title={t("ci.heading")} intro={t("ci.intro")}>
			{!status.isLoading && <CIInstallLink step={ciNextStep(status.data, status.isError)} />}
			<div className="grid gap-3 sm:grid-cols-3">
				<label className="flex flex-col gap-1 text-sm">
					{t("ci.mode")}
					<select
						className={selectClass}
						value={mode}
						onChange={(e) => setMode(e.target.value as "recorded" | "live")}
						data-testid="ci-mode"
					>
						<option value="recorded">{t("ci.modeRecorded")}</option>
						<option value="live">{t("ci.modeLive")}</option>
					</select>
				</label>
				{mode === "live" && (
					<label className="flex flex-col gap-1 text-sm">
						{t("ci.agent")}
						<select className={selectClass} value={agent} onChange={(e) => setAgent(e.target.value)} data-testid="ci-agent">
							<option value="">{t("ci.pickAgent")}</option>
							{(agents.data?.agents ?? []).map((a) => (
								<option key={a.id} value={a.id}>
									{a.id}
								</option>
							))}
						</select>
					</label>
				)}
			</div>
			{preview.data && (
				<div className="flex flex-col gap-1">
					<div className="flex items-center justify-between text-sm">
						<span>
							{t("ci.preview")}: <code className="font-mono text-xs">{preview.data.path}</code>
						</span>
						<Button
							size="sm"
							variant="ghost"
							onClick={() => {
								void navigator.clipboard?.writeText(preview.data?.workflow ?? "").then(() => setCopied(true));
							}}
						>
							{copied ? t("ci.copied") : t("ci.copy")}
						</Button>
					</div>
					<pre className="bg-muted/50 max-h-96 overflow-auto rounded-lg border p-4 font-mono text-xs leading-5" data-testid="ci-workflow">
						{preview.data.workflow}
					</pre>
				</div>
			)}
			{preview.error && <ErrorNote message={apiErrorMessage(preview.error, t("errors.load"))} />}
		</Section>
	);
}