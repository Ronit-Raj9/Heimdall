import { useConfirm } from "@/components/platform/confirmDialog";
import { JobRunButton } from "@/components/jobs/jobRunButton";
import { EmptyState } from "@/components/shell/emptyState";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
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
import type { GithubStatus } from "@/lib/types/github";
import type {
	IntegrationExport,
	IntegrationExportFormat,
	IntegrationsExportParams,
	Webhook,
	WebhookDelivery,
	WebhookEventType,
} from "@/lib/types/integrations";
import { useState } from "react";
import { useTranslation } from "react-i18next";

declare module "@/lib/i18n/resources" {
	interface LaneResources {
		integrations: typeof import("@/locales/en/integrations.json");
	}
}

const FORMATS: IntegrationExportFormat[] = ["otlp", "eee", "csv", "jsonl", "harbor"];
const EVENTS: WebhookEventType[] = ["experiment.finished", "job.failed"];
const selectClass = "border-input bg-background h-9 w-full rounded-md border px-2 text-sm";

/** Integrations (web parity L19): CI workflow (install PR via Settings > GitHub), exports with download, experiment-finished webhooks. */
export default function IntegrationsView() {
	const { t } = useTranslation("integrations");
	return (
		<div className="flex flex-col gap-4 p-6" data-testid="integrations-view">
			<div>
				<h1 className="text-xl font-semibold">{t("title")}</h1>
				<p className="text-muted-foreground text-sm">{t("subtitle")}</p>
			</div>
			<Tabs defaultValue="webhooks">
				<TabsList>
					<TabsTrigger value="webhooks" data-testid="integrations-tab-webhooks">
						{t("tabs.webhooks")}
					</TabsTrigger>
					<TabsTrigger value="exports" data-testid="integrations-tab-exports">
						{t("tabs.exports")}
					</TabsTrigger>
					<TabsTrigger value="ci" data-testid="integrations-tab-ci">
						{t("tabs.ci")}
					</TabsTrigger>
				</TabsList>
				<TabsContent value="webhooks">
					<WebhooksPanel />
				</TabsContent>
				<TabsContent value="exports">
					<ExportsPanel />
				</TabsContent>
				<TabsContent value="ci">
					<CIPanel />
				</TabsContent>
			</Tabs>
		</div>
	);
}

function Section({ title, intro, children }: { title: string; intro?: string; children: React.ReactNode }) {
	return (
		<section className="border-border flex flex-col gap-3 rounded-lg border p-4">
			<div>
				<h2 className="font-medium">{title}</h2>
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
	const [events, setEvents] = useState<WebhookEventType[]>(["experiment.finished"]);
	const [secret, setSecret] = useState<string | null>(null);
	const [message, setMessage] = useState<string | null>(null);
	const [tests, setTests] = useState<Record<string, WebhookDelivery>>({});

	const fail = (err: unknown) =>
		setMessage((err as { status?: number })?.status === 403 ? t("errors.adminOnly") : apiErrorMessage(err, t("errors.generic")));

	const add = async () => {
		setMessage(null);
		const res = await createHook({ url: url.trim(), events, description: desc.trim() || undefined });
		if ("error" in res) return fail(res.error);
		setSecret(res.data.secret);
		setUrl("");
		setDesc("");
	};

	return (
		<Section title={t("webhooks.heading")} intro={t("webhooks.intro")}>
			{data?.hosted && <p className="text-muted-foreground text-xs">{t("webhooks.hostedNote")}</p>}
			<form
				className="grid gap-2 sm:grid-cols-[2fr_1fr_auto]"
				onSubmit={(e) => {
					e.preventDefault();
					void add();
				}}
			>
				<Input
					aria-label={t("webhooks.url")}
					placeholder={t("webhooks.urlPlaceholder")}
					value={url}
					onChange={(e) => setUrl(e.target.value)}
					data-testid="webhook-url"
				/>
				<Input aria-label={t("webhooks.description")} placeholder={t("webhooks.description")} value={desc} onChange={(e) => setDesc(e.target.value)} />
				<Button type="submit" disabled={!url.trim() || events.length === 0 || create.isLoading} data-testid="webhook-add">
					{create.isLoading ? t("webhooks.adding") : t("webhooks.add")}
				</Button>
				<fieldset className="flex flex-wrap gap-4 text-sm sm:col-span-3">
					<legend className="sr-only">{t("webhooks.events")}</legend>
					{EVENTS.map((ev) => (
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
			{secret && (
				<div className="border-chart-warning/60 bg-chart-warning/10 flex flex-wrap items-center gap-2 rounded-md border p-3 text-sm" data-testid="webhook-secret">
					<span>{t("webhooks.secretOnce")}</span>
					<code className="bg-background rounded px-1.5 py-0.5 font-mono text-xs select-all">{secret}</code>
					<Button size="sm" variant="outline" onClick={() => setSecret(null)}>
						{t("webhooks.dismiss")}
					</Button>
				</div>
			)}
			{message && <ErrorNote message={message} />}
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
		<ul className="divide-border flex flex-col divide-y" data-testid="webhook-list">
			{hooks.map((h) => {
				const last = h.last_delivery;
				const test = tests[h.id];
				return (
					<li key={h.id} className="flex flex-col gap-1 py-2" data-testid={`webhook-${h.id}`}>
						<div className="flex flex-wrap items-center gap-2">
							<span className="font-mono text-sm break-all">{h.url}</span>
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
							{h.events.map((e) => t(`webhooks.eventNames.${e}`)).join(", ")} · whsec_...{h.secret_last4}
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
							<p className={test.ok ? "text-chart-success-ink text-xs" : "text-chart-error-ink text-xs"} data-testid={`webhook-test-result-${h.id}`}>
								{test.ok ? t("webhooks.testOk", { code: test.status_code }) : t("webhooks.testFail", { error: test.error ?? `HTTP ${test.status_code}` })}
							</p>
						)}
					</li>
				);
			})}
		</ul>
	);
}

// ----------------------------------------------------------------------------------------------
// Exports

/** The job params of an export form, or null while it is incomplete. */
export function exportParams(format: IntegrationExportFormat, expId: string, suite: string, payloads: boolean, allowSynthetic: boolean): IntegrationsExportParams | null {
	if (format === "harbor") return suite ? { format, suite } : null;
	if (!expId) return null;
	const p: IntegrationsExportParams = { format, exp_id: expId };
	if (format === "otlp" && payloads) p.payloads = true;
	if (format === "eee" && allowSynthetic) p.allow_synthetic = true;
	return p;
}

function ExportsPanel() {
	const { t } = useTranslation("integrations");
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

	return (
		<div className="flex flex-col gap-4">
			<Section title={t("exports.heading")} intro={t("exports.intro")}>
				<div className="grid gap-3 sm:grid-cols-2">
					<label className="flex flex-col gap-1 text-sm">
						{t("exports.format")}
						<select className={selectClass} value={format} onChange={(e) => setFormat(e.target.value as IntegrationExportFormat)} data-testid="export-format">
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
						<label className="flex flex-col gap-1 text-sm">
							{t("exports.experiment")}
							<select className={selectClass} value={expId} onChange={(e) => setExpId(e.target.value)} data-testid="export-experiment">
								<option value="">{t("exports.pickExperiment")}</option>
								{experiments.map((x) => (
									<option key={x.exp_id} value={x.exp_id}>
										{x.name ? `${x.name} (${x.exp_id})` : x.exp_id} · {x.state}
									</option>
								))}
							</select>
						</label>
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
					<EmptyState compact message={t("exports.noExperiments")} action={{ label: t("exports.experiment"), to: "/experiments" }} testId="exports-no-experiments" />
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
		<table className="w-full text-sm" data-testid="export-table">
			<thead className="text-muted-foreground text-left text-xs">
				<tr>
					<th className="py-1">{t("exports.name")}</th>
					<th>{t("exports.format")}</th>
					<th>{t("exports.size")}</th>
					<th>{t("exports.created")}</th>
					<th />
				</tr>
			</thead>
			<tbody>
				{exports.map((x) => (
					<tr key={x.name} className="border-border border-t">
						<td className="py-1.5 font-mono text-xs break-all">{x.name}</td>
						<td>
							<Badge variant="outline">{x.format}</Badge>
						</td>
						<td>{formatBytes(x.size)}</td>
						<td>{new Date(x.created_at).toLocaleString()}</td>
						<td className="flex justify-end gap-2 py-1">
							<Button asChild size="sm" variant="outline">
								<a href={exportDownloadUrl(x.name)} download={x.name} data-testid={`export-download-${x.name}`}>
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
		return <p className="text-muted-foreground text-sm" data-testid="ci-manual">{t("ci.manual")}</p>;
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
	const preview = useGetCIWorkflowQuery({ mode, agent: mode === "live" ? agent || undefined : undefined }, { skip: mode === "live" && !agent });

	return (
		<Section title={t("ci.heading")} intro={t("ci.intro")}>
			{!status.isLoading && <CIInstallLink step={ciNextStep(status.data, status.isError)} />}
			<div className="grid gap-3 sm:grid-cols-3">
				<label className="flex flex-col gap-1 text-sm">
					{t("ci.mode")}
					<select className={selectClass} value={mode} onChange={(e) => setMode(e.target.value as "recorded" | "live")} data-testid="ci-mode">
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
					<pre className="bg-muted max-h-96 overflow-auto rounded p-2 text-xs" data-testid="ci-workflow">
						{preview.data.workflow}
					</pre>
				</div>
			)}
			{preview.error && <ErrorNote message={apiErrorMessage(preview.error, t("errors.load"))} />}
		</Section>
	);
}
