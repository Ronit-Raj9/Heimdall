import { pageWidthClass } from "@/components/shell/pageWidth";
import "@/components/platform/fw23I18n";
import { PageHeader } from "@/components/shell";
import { useFormDraft } from "@/lib/hooks/useFormDraft";
import { useLeaveGuard } from "@/lib/hooks/useLeaveGuard";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { WhyDisabled, firstReason } from "@/components/ui/whyDisabled";
import { idProblem } from "@/lib/inputChecks";
import "@/lib/w4m0I18n";
import "@/app/runs/views/w4m4I18n";
import { Section } from "@/components/ui/section";
import { StatCard, StatGrid } from "@/components/ui/statCard";
import { apiErrorMessage } from "@/lib/store/apis/integrationsApi";
import { useDraftMcpBenchmarkMutation, useInspectMcpServerMutation } from "@/lib/store/apis/mcpTestApi";
import { canUse, useSession } from "@/lib/session/sessionContext";
import { useListSecretsQuery } from "@/lib/store/apis/secretsApi";
import "@/lib/types/mcptestI18n";
import {
	scoreBand,
	serverIdFrom,
	type McpDraftRequest,
	type McpDraftResponse,
	type McpInspectRequest,
	type McpReport,
	type McpToolQuality,
} from "@/lib/types/mcptest";
import { cn } from "@/lib/utils";
import { CopyId } from "@/components/ui/copyId";
import "@/components/runs/uf5bI18n";
import { skipToken } from "@reduxjs/toolkit/query/react";
import { keyMissingSearch } from "@/components/models/keyMissingNote";
import { Link } from "@tanstack/react-router";
import { ArrowUpRight, Check, Minus, X } from "lucide-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import "@/app/schedules/views/ufrjI18n";

export type AuthMode = "bearer" | "raw" | "header";

export interface McpForm {
	url: string;
	secret: string;
	mode: AuthMode;
	header: string;
	callReadOnly: boolean;
}

export const EMPTY_FORM: McpForm = { url: "", secret: "", mode: "bearer", header: "", callReadOnly: true };

/** The inspect request a form sends. No credential leaves out every auth field. */
export function requestFromForm(f: McpForm): McpInspectRequest {
	const req: McpInspectRequest = { url: f.url.trim(), call_read_only: f.callReadOnly };
	if (!f.secret) return req;
	req.secret = f.secret;
	if (f.mode === "bearer") req.auth_scheme = "Bearer";
	if (f.mode === "header" && f.header.trim()) req.auth_header = f.header.trim();
	return req;
}

/** True when something was typed that a check has not used yet. */
export function mcpFormDirty(f: McpForm): boolean {
	return f.url.trim() !== "" || f.secret !== "" || f.header.trim() !== "";
}

/**
 * What keeps the form from being checked: no address, or a credential sent in a custom header
 * with no header name. Null when the form can be sent.
 */
export function formProblem(f: McpForm): "url" | "header" | null {
	if (!f.url.trim()) return "url";
	if (f.secret && f.mode === "header" && !f.header.trim()) return "header";
	return null;
}

/** The most requests one check sends (mcptest.DefaultMaxCalls), said beside the button (Q-290). */
export const MCP_MAX_REQUESTS = 80;

/**
 * Whether the address shows its problem and holds the button (Q-290): a typed address that is not a
 * full http or https URL with a host does, as soon as it is typed; an empty one only after a press. Pure.
 */
export function urlState(raw: string, pressed: boolean): { bad: boolean; blocks: boolean } {
	const typed = raw.trim() !== "";
	const bad = !validUrl(raw) && (typed || pressed);
	return { bad, blocks: typed && !validUrl(raw) };
}

/** True when the address is a full http or https URL with a host. */
export function validUrl(raw: string): boolean {
	try {
		const u = new URL(raw.trim());
		return (u.protocol === "https:" || u.protocol === "http:") && Boolean(u.hostname);
	} catch {
		return false;
	}
}

/** This site's own hosted MCP address (X-36): the origin plus /mcp, empty without a window. */
export function hostedMcpAddress(origin: string | undefined): string {
	return origin ? `${origin.replace(/\/+$/, "")}/mcp` : "";
}

/** One past check in this browser's history (M5-49). */
export interface McpCheck {
	url: string;
	at: string;
	reached: boolean;
	overall?: number;
}

/** Checks kept per endpoint. */
export const MCP_HISTORY_PER_URL = 10;
const HISTORY_KEY = "heimdall.mcpTest.history";

/** The history after a new check: newest first, at most MCP_HISTORY_PER_URL per endpoint. */
export function addCheck(list: readonly McpCheck[], c: McpCheck, perUrl = MCP_HISTORY_PER_URL): McpCheck[] {
	const out: McpCheck[] = [];
	const seen = new Map<string, number>();
	for (const x of [c, ...list]) {
		const n = seen.get(x.url) ?? 0;
		if (n >= perUrl) continue;
		seen.set(x.url, n + 1);
		out.push(x);
	}
	return out;
}

function readHistory(): McpCheck[] {
	try {
		const raw = window.localStorage.getItem(HISTORY_KEY);
		const v: unknown = raw ? JSON.parse(raw) : [];
		return Array.isArray(v) ? (v as McpCheck[]) : [];
	} catch {
		return [];
	}
}

function writeHistory(list: readonly McpCheck[]) {
	try {
		window.localStorage.setItem(HISTORY_KEY, JSON.stringify(list));
	} catch {
		// Storage blocked: the history lasts only for this page.
	}
}

/** The last checks of the endpoint in the form (all endpoints when it is empty), newest first. */
export function McpHistory({ checks, url }: { checks: readonly McpCheck[]; url: string }) {
	const { t } = useTranslation("uf5b");
	const shown = checks.filter((c) => !url.trim() || c.url === url.trim()).slice(0, MCP_HISTORY_PER_URL);
	if (shown.length === 0) return null;
	return (
		<Section title={t("mcp.historyTitle")} as="h3" testId="mcp-history">
			<ul className="flex flex-col gap-1 text-sm">
				{shown.map((c) => (
					<li key={c.url + c.at} className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1">
						<span className="min-w-0 truncate font-mono text-xs" title={c.url}>
							{c.url}
						</span>
						<span className="text-muted-foreground text-xs">{new Date(c.at).toLocaleString()}</span>
						{c.reached && c.overall !== undefined ? (
							<Score value={c.overall} />
						) : (
							<span className="text-chart-error-ink text-xs">{t("mcp.historyUnreached")}</span>
						)}
					</li>
				))}
			</ul>
		</Section>
	);
}

/** The draft request: the id trimmed and the server name normalised only when sending, so dashes can be typed. */
export function draftRequest(request: McpInspectRequest, id: string, serverId: string, tools: string[]): McpDraftRequest {
	return { ...request, id: id.trim(), server_id: serverIdFrom(serverId), tools };
}

/** A default server name from the report or the URL's host. */
export function defaultServerId(report: McpReport): string {
	const fromName = serverIdFrom(report.server?.name ?? "");
	if (fromName) return fromName;
	try {
		return serverIdFrom(new URL(report.url).hostname.split(".")[0] ?? "") || "server";
	} catch {
		return "server";
	}
}

const bandVariant = { good: "success", fair: "warning", poor: "destructive" } as const;

function Score({ value }: { value: number }) {
	return (
		<Badge variant={bandVariant[scoreBand(value)]} className="tabular-nums">
			{value}
		</Badge>
	);
}

function ToolRow({ q, desc }: { q: McpToolQuality; desc: string }) {
	const { t } = useTranslation("mcptest");
	return (
		<li className="border-border/70 flex flex-col gap-2 rounded-lg border p-3" data-testid={`mcp-tool-${q.tool}`}>
			<div className="flex flex-wrap items-center gap-2">
				<span className="font-mono text-sm font-semibold">{q.tool}</span>
				{q.read_only && <Badge variant="neutral">{t("result.readOnly")}</Badge>}
				{q.destructive && <Badge variant="warning">{t("result.destructive")}</Badge>}
				<span className="ml-auto flex gap-1.5 text-xs">
					<Badge variant={bandVariant[scoreBand(q.description_score)]}>{t("result.descScore", { n: q.description_score })}</Badge>
					<Badge variant={bandVariant[scoreBand(q.schema_score)]}>{t("result.schemaScore", { n: q.schema_score })}</Badge>
				</span>
			</div>
			{desc && <p className="text-muted-foreground line-clamp-2 text-xs">{desc}</p>}
			{q.issues.length === 0 ? (
				<p className="text-muted-foreground text-xs">{t("result.noIssues")}</p>
			) : (
				<ul className="flex flex-col gap-1 text-xs">
					{q.issues.map((is) => (
						<li key={is.code + (is.field ?? "")}>
							<span className={cn(is.severity === "warn" ? "text-chart-warning-ink" : "text-muted-foreground")}>{is.message}</span>{" "}
							<span className="text-muted-foreground">
								{t("result.fix")} {is.fix}
							</span>
						</li>
					))}
				</ul>
			)}
		</li>
	);
}

/** Everything one inspection measured. */
export function McpReportView({ report }: { report: McpReport }) {
	const { t } = useTranslation("mcptest");
	if (!report.reached) {
		return (
			<p className="text-destructive text-sm" data-testid="mcp-unreached">
				{t("result.unreached", { error: report.error ?? "" })}
			</p>
		);
	}
	const s = report.scores;
	const desc = new Map(report.tools.map((x) => [x.name, x.description]));
	return (
		<div className="flex flex-col gap-6" data-testid="mcp-report">
			<p className="text-muted-foreground text-sm">
				{t("result.server", {
					name: report.server.name || report.url,
					version: report.server.version,
					protocol: report.server.protocol_version,
				})}{" "}
				· {t("result.requests", { n: report.requests })}
			</p>
			<StatGrid>
				<StatCard label={t("result.overall")} value={s.overall} testId="mcp-score-overall" />
				<StatCard
					label={t("result.reliability")}
					value={s.reliability}
					hint={t("result.reliabilityHint", { ok: report.reliability.succeeded, n: report.reliability.attempts })}
				/>
				<StatCard label={t("result.errorRecovery")} value={s.error_recovery} hint={t("result.errorRecoveryHint")} />
				<StatCard
					label={t("result.latency")}
					value={s.latency}
					hint={t("result.latencyHint", { p50: report.latency.p50_ms, p95: report.latency.p95_ms })}
				/>
				<StatCard label={t("result.schema")} value={s.schema} />
				<StatCard label={t("result.description")} value={s.description} />
			</StatGrid>
			{!report.reliability.stable_listing && <p className="text-chart-warning-ink text-sm">{t("result.unstable")}</p>}

			<Section title={t("result.recoveryHeading")} as="h3">
				<ul className="flex flex-col gap-1.5 text-sm" data-testid="mcp-recovery">
					{report.error_recovery.map((c) => (
						<li key={c.id} className="flex items-start gap-2">
							{c.skipped ? (
								<Minus className="text-muted-foreground mt-0.5 size-4 shrink-0" aria-label={t("result.skipped")} />
							) : c.passed ? (
								<Check className="text-chart-success-ink mt-0.5 size-4 shrink-0" aria-label={t("result.passed")} />
							) : (
								<X className="text-chart-error-ink mt-0.5 size-4 shrink-0" aria-label={t("result.notPassed")} />
							)}
							<span>
								<span className="font-medium">{c.title}</span> <span className="text-muted-foreground">{c.detail}</span>
							</span>
						</li>
					))}
				</ul>
			</Section>

			<Section title={t("result.toolsHeading", { n: report.tools.length })} as="h3">
				<ul className="flex flex-col gap-2">
					{report.quality.tools.map((q) => (
						<ToolRow key={q.tool} q={q} desc={desc.get(q.tool) ?? ""} />
					))}
				</ul>
				{report.quality.server_issues.length > 0 && (
					<div className="flex flex-col gap-1 text-sm">
						<span className="font-medium">{t("result.serverIssues")}</span>
						{report.quality.server_issues.map((is) => (
							<span key={is.code + is.message} className="text-muted-foreground">
								{is.message} {t("result.fix")} {is.fix}
							</span>
						))}
					</div>
				)}
			</Section>

			{report.calls.length > 0 && (
				<Section title={t("result.callsHeading")} as="h3">
					<ul className="flex flex-col gap-1 text-sm">
						{report.calls.map((c, i) => (
							<li key={c.tool + i} className="flex flex-wrap items-center gap-2">
								<span className="font-mono">{c.tool}</span>
								<Badge variant={c.ok ? "success" : "destructive"}>{c.ok ? t("result.callOk") : t("result.callFailed")}</Badge>
								<span className="text-muted-foreground tabular-nums">{c.latency_ms} ms</span>
								{c.placeholder && <span className="text-muted-foreground text-xs">{t("result.placeholder")}</span>}
								{!c.ok && <span className="text-muted-foreground text-xs">{c.detail}</span>}
							</li>
						))}
					</ul>
				</Section>
			)}

			<Section title={t("result.scanHeading")} as="h3">
				{report.scan.length === 0 ? (
					<p className="text-muted-foreground text-sm">{t("result.scanNone")}</p>
				) : (
					<ul className="flex flex-col gap-1 text-sm" data-testid="mcp-scan">
						{report.scan.map((f, i) => (
							<li key={f.rule + i}>
								<Badge variant={f.severity === "block" ? "destructive" : "warning"}>{f.severity}</Badge>{" "}
								<span className="font-mono text-xs">{f.file}</span> {f.detail}
							</li>
						))}
					</ul>
				)}
				{/* The scan reads the listing; whether an agent follows poisoned descriptions is the gate mcp_poisoning_followed (M7-110). */}
				<Link to="/docs" search={{ doc: "tool-safety" } as never} className="text-primary text-sm underline-offset-4 hover:underline" data-testid="mcp-poisoning-link">
					{t("result.poisoningLink")}
				</Link>
			</Section>

			{report.reliability.failures.length > 0 && (
				<Section title={t("result.failures")} as="h3">
					<ul className="text-muted-foreground flex flex-col gap-1 text-xs">
						{report.reliability.failures.map((f, i) => (
							<li key={i}>{f}</li>
						))}
					</ul>
				</Section>
			)}
		</div>
	);
}

function DraftForm({ report, request }: { report: McpReport; request: McpInspectRequest }) {
	const { t } = useTranslation("mcptest");
	const server0 = defaultServerId(report);
	const [serverId, setServerId] = useState(server0);
	const [id, setId] = useState(`${server0}-mcp`);
	const [tools, setTools] = useState<string[]>(report.tools.map((x) => x.name));
	const { t: tw0 } = useTranslation("w4m0");
	const { t: tw } = useTranslation("w4m4");
	const [draft, st] = useDraftMcpBenchmarkMutation();
	const [done, setDone] = useState<McpDraftResponse | null>(null);
	const idWhy = idProblem(id.trim(), "benchmark id");
	const createWhy = firstReason(
		idWhy,
		!serverIdFrom(serverId) && tw0("inputs.idEmpty", { what: "server name" }),
		tools.length === 0 && tw("mcp.pickTool"),
	);
	const toggle = (name: string) => setTools((cur) => (cur.includes(name) ? cur.filter((x) => x !== name) : [...cur, name]));
	const create = async () => {
		try {
			setDone(await draft(draftRequest(request, id, serverId, tools)).unwrap());
		} catch {
			// The error is shown from the mutation state.
		}
	};
	return (
		<Section title={t("draft.heading")} description={t("draft.hint")} testId="mcp-draft">
			<div className="grid gap-3 sm:grid-cols-2">
				<label className="flex flex-col gap-1.5 text-sm">
					<span className="font-medium">{t("draft.id")}</span>
					<Input value={id} onChange={(e) => setId(e.target.value)} aria-invalid={idWhy !== null} data-testid="mcp-draft-id" />
					{idWhy && (
						<span className="text-destructive text-xs" data-testid="mcp-draft-id-problem">
							{idWhy}
						</span>
					)}
				</label>
				<label className="flex flex-col gap-1.5 text-sm">
					<span className="font-medium">{t("draft.serverName")}</span>
					<Input
						value={serverId}
						onChange={(e) => setServerId(e.target.value)}
						onBlur={() => setServerId(serverIdFrom(serverId))}
						data-testid="mcp-draft-server"
					/>
				</label>
			</div>
			<fieldset className="flex flex-col gap-1.5 text-sm">
				<legend className="mb-1 font-medium">{t("draft.tools")}</legend>
				{report.tools.map((x) => (
					<label key={x.name} className="flex items-center gap-2">
						<input type="checkbox" checked={tools.includes(x.name)} onChange={() => toggle(x.name)} />
						<span className="font-mono">{x.name}</span>
					</label>
				))}
			</fieldset>
			<div className="flex items-center gap-3">
				<Button
					onClick={() => void create()}
					disabled={st.isLoading || createWhy !== null}
					aria-describedby={createWhy ? "mcp-draft-why" : undefined}
					data-testid="mcp-draft-create"
				>
					{st.isLoading ? t("draft.creating") : t("draft.create")}
				</Button>
				<WhyDisabled reason={st.isLoading ? null : createWhy} testId="mcp-draft-why" />
				{st.error != null && <span className="text-destructive text-sm">{apiErrorMessage(st.error, t("draft.failed"))}</span>}
			</div>
			{done && (
				<div className="flex flex-col gap-2 text-sm" data-testid="mcp-draft-done">
					<p className="font-medium">{t("draft.created", { id: done.suite, n: done.tasks.length })}</p>
					{done.no_reference.length > 0 && (
						<p className="text-chart-warning-ink">
							{t("draft.noReference", { n: done.no_reference.length, ids: done.no_reference.join(", ") })}
						</p>
					)}
					<p className="text-muted-foreground">{t("draft.next")}</p>
					<div className="flex flex-wrap gap-2">
						<Button asChild variant="outline" size="sm">
							<Link to="/suites/$suiteId" params={{ suiteId: done.suite } as never}>
								{t("draft.open")}
							</Link>
						</Button>
						<DraftEvaluateLink done={done} />
					</div>
				</div>
			)}
		</Section>
	);
}

/**
 * The Evaluate button under a drafted benchmark (N113): only reference agents the organization has a model key for.
 * Two keyed agents compare head to head (mode=opponent), one runs alone, none explains which key to add.
 */
export function DraftEvaluateLink({ done }: { done: McpDraftResponse }) {
	const { t } = useTranslation("mcptest");
	const keyed = done.reference_agents_keyed ?? [];
	if (keyed.length === 0) {
		return (
			<p className="text-chart-warning-ink flex flex-wrap items-center gap-2 text-sm" data-testid="mcp-draft-no-key">
				{t("draft.noKeyedAgent")}
				{/* No single provider is named, so this opens Settings, Model keys, and comes back here (M8-12). */}
				<Link to="/settings" search={keyMissingSearch("", "/mcp-test") as never} className="text-primary underline" data-testid="mcp-draft-models-link">
					{t("draft.modelsKeys")}
				</Link>
			</p>
		);
	}
	const [a, b] = keyed;
	const search = b ? { suite: done.suite, agent: a, mode: "opponent", opponent: b } : { suite: done.suite, agent: a };
	return (
		<Button asChild size="sm">
			<Link to="/evaluate" search={search as never} data-testid="mcp-draft-evaluate">
				{b ? t("draft.evaluateWithTwo", { a, b }) : t("draft.evaluateWith", { agent: a })}
			</Link>
		</Button>
	);
}

/** /mcp-test: inspect the org's MCP server, then draft a benchmark of it. */
export function McpTestView() {
	const { t } = useTranslation("mcptest");
	const { t: tf } = useTranslation("fw23");
	const { t: tw } = useTranslation("w4m4");
	// The address and options are kept as a draft; the secret stays in memory only (X-82).
	const [form, setForm] = useFormDraft<McpForm>("mcpTest", EMPTY_FORM);
	const session = useSession();
	// Secret names are for members and above (J9.secrets); a viewer types a name instead.
	const listSecrets = canUse(session, "member");
	const secrets = useListSecretsQuery(listSecrets ? undefined : skipToken);
	const [inspect, st] = useInspectMcpServerMutation();
	const [report, setReport] = useState<McpReport | null>(null);
	const [sent, setSent] = useState<McpInspectRequest | null>(null);
	const [history, setHistory] = useState<McpCheck[]>([]);
	useEffect(() => setHistory(readHistory()), []);
	const remember = (r: McpReport) =>
		setHistory((h) => {
			const next = addCheck(h, { url: r.url || form.url.trim(), at: r.checked_at || new Date().toISOString(), reached: r.reached, overall: r.scores?.overall });
			writeHistory(next);
			return next;
		});
	const set = (p: Partial<McpForm>) => setForm(p);
	const problem = formProblem(form);
	const { t: t5 } = useTranslation("uf5b");
	const { t: tr } = useTranslation("ufrj");
	const [pressed, setPressed] = useState(false);
	// A typed address that is not a URL is said at once and holds the button (Q-290); an empty one is
	// said on a press, so the button stays usable to learn what to type (M5-50).
	const url = urlState(form.url, pressed);
	const urlBad = url.bad;
	const inspectWhy = url.blocks ? t5("mcp.badUrl") : problem === "header" ? tw("mcp.headerNeeded") : null;
	const address = hostedMcpAddress(typeof window !== "undefined" ? window.location.origin : undefined);
	useLeaveGuard(mcpFormDirty(form) && report === null);
	const run = async () => {
		setPressed(true);
		if (!validUrl(form.url) || problem !== null) return;
		const req = requestFromForm(form);
		try {
			const r = await inspect(req).unwrap();
			setReport(r);
			setSent(req);
			remember(r);
		} catch {
			setReport(null);
		}
	};
	return (
		<section className={cn(pageWidthClass("read"), "flex flex-col gap-6")} data-testid="mcp-test-screen">
			<PageHeader
				title={t("title")}
				subtitle={t("subtitle")}
				actions={
					<Button asChild variant="outline" size="sm">
						<Link to={"/gateway" as never} data-testid="mcp-gateway-link">
							{t5("mcp.gatewayLink")}
							<ArrowUpRight aria-hidden="true" />
						</Link>
					</Button>
				}
			/>
			{address && (
				<p className="text-muted-foreground flex min-w-0 flex-wrap items-center gap-2 text-sm" data-testid="mcp-hosted-address">
					{t5("mcp.yourAddress")}
					<CopyId value={address} className="text-foreground text-sm" testId="mcp-hosted-address-copy" />
				</p>
			)}
			<Section title={t("form.heading")}>
				<label className="flex flex-col gap-1.5 text-sm">
					<span className="font-medium">{t("form.url")}</span>
					<Input
						value={form.url}
						onChange={(e) => set({ url: e.target.value })}
						placeholder="https://mcp.example.com/mcp"
						className="max-w-xl"
						type="url"
						aria-invalid={urlBad || undefined}
						aria-describedby={urlBad ? "mcp-url-problem" : undefined}
						data-testid="mcp-url"
					/>
					{urlBad ? (
						<span id="mcp-url-problem" className="text-destructive text-xs" role="alert" data-testid="mcp-url-problem">
							{t5("mcp.badUrl")}
						</span>
					) : (
						<span className="text-muted-foreground text-xs">{t("form.urlHint")}</span>
					)}
				</label>
				<div className="grid gap-3 sm:grid-cols-3">
					<label className="flex flex-col gap-1.5 text-sm">
						<span className="font-medium">{listSecrets ? t("form.secret") : tf("mcp.secretName")}</span>
						{listSecrets ? (
							<select
								className="border-input bg-background h-9 rounded-lg border px-2 text-sm"
								value={form.secret}
								onChange={(e) => set({ secret: e.target.value })}
								data-testid="mcp-secret"
							>
								<option value="">{t("form.secretNone")}</option>
								{(secrets.data?.secrets ?? []).map((s) => (
									<option key={s.id} value={s.name}>
										{s.name}
									</option>
								))}
							</select>
						) : (
							<Input
								value={form.secret}
								onChange={(e) => set({ secret: e.target.value.trim() })}
								placeholder={tf("mcp.secretNamePlaceholder")}
								aria-label={tf("mcp.secretName")}
								autoComplete="off"
								data-testid="mcp-secret-name"
							/>
						)}
						<span className="text-muted-foreground text-xs">
							{listSecrets ? t("form.secretHint") : tf("mcp.secretNameHint")}{" "}
							<Link to={"/settings" as never} search={{ tab: "secrets" } as never} className="text-primary underline-offset-4 hover:underline">
								{t5("mcp.manageSecrets")}
							</Link>
						</span>
					</label>
					{form.secret && (
						<label className="flex flex-col gap-1.5 text-sm">
							<span className="font-medium">{t("form.scheme")}</span>
							<select
								className="border-input bg-background h-9 rounded-lg border px-2 text-sm"
								value={form.mode}
								onChange={(e) => set({ mode: e.target.value as AuthMode })}
							>
								<option value="bearer">{t("form.schemeBearer")}</option>
								<option value="raw">{t("form.schemeRaw")}</option>
								<option value="header">{t("form.schemeHeader")}</option>
							</select>
						</label>
					)}
					{form.secret && form.mode === "header" && (
						<label className="flex flex-col gap-1.5 text-sm">
							<span className="font-medium">{t("form.header")}</span>
							<Input value={form.header} onChange={(e) => set({ header: e.target.value })} placeholder="X-Api-Key" />
						</label>
					)}
				</div>
				<label className="flex items-start gap-2 text-sm">
					<input
						type="checkbox"
						className="mt-1"
						checked={form.callReadOnly}
						onChange={(e) => set({ callReadOnly: e.target.checked })}
						data-testid="mcp-call-read-only"
					/>
					<span>
						{t("form.callReadOnly")}
						<span className="text-muted-foreground block text-xs">{t("form.callReadOnlyHint")}</span>
					</span>
				</label>
				<p className="text-muted-foreground max-w-prose text-xs" data-testid="mcp-inspect-note">
					{tr("mcp.inspectNote", { count: MCP_MAX_REQUESTS })}
					<span className="block">{t5("mcp.youGet")}</span>
				</p>
				<div className="flex flex-wrap items-center gap-3">
					<Button
						onClick={() => void run()}
						disabled={st.isLoading || url.blocks}
						aria-describedby={inspectWhy ? "mcp-inspect-why" : undefined}
						data-testid="mcp-inspect"
					>
						{st.isLoading ? t("form.inspecting") : t("form.inspect")}
					</Button>
					<WhyDisabled reason={st.isLoading ? null : inspectWhy} testId="mcp-inspect-why" />
					{st.error != null && <span className="text-destructive text-sm">{apiErrorMessage(st.error, t("form.failed"))}</span>}
				</div>
			</Section>
			<McpHistory checks={history} url={form.url} />
			{report && <McpReportView report={report} />}
			{report?.reached && report.tools.length > 0 && sent && <DraftForm key={report.checked_at} report={report} request={sent} />}
		</section>
	);
}

export default McpTestView;