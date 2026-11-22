import "@/components/platform/fw23I18n";
import { PageHeader } from "@/components/shell";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
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
	type McpDraftResponse,
	type McpInspectRequest,
	type McpReport,
	type McpToolQuality,
} from "@/lib/types/mcptest";
import { cn } from "@/lib/utils";
import { skipToken } from "@reduxjs/toolkit/query/react";
import { Link } from "@tanstack/react-router";
import { Check, Minus, Plug, X } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";

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
	const [draft, st] = useDraftMcpBenchmarkMutation();
	const [done, setDone] = useState<McpDraftResponse | null>(null);
	const toggle = (name: string) => setTools((cur) => (cur.includes(name) ? cur.filter((x) => x !== name) : [...cur, name]));
	const create = async () => {
		try {
			setDone(await draft({ ...request, id: id.trim(), server_id: serverId, tools }).unwrap());
		} catch {
			// The error is shown from the mutation state.
		}
	};
	return (
		<Section title={t("draft.heading")} description={t("draft.hint")} testId="mcp-draft">
			<div className="grid gap-3 sm:grid-cols-2">
				<label className="flex flex-col gap-1.5 text-sm">
					<span className="font-medium">{t("draft.id")}</span>
					<Input value={id} onChange={(e) => setId(e.target.value)} data-testid="mcp-draft-id" />
				</label>
				<label className="flex flex-col gap-1.5 text-sm">
					<span className="font-medium">{t("draft.serverName")}</span>
					<Input value={serverId} onChange={(e) => setServerId(serverIdFrom(e.target.value))} data-testid="mcp-draft-server" />
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
					disabled={st.isLoading || tools.length === 0 || !id.trim() || !serverId}
					data-testid="mcp-draft-create"
				>
					{st.isLoading ? t("draft.creating") : t("draft.create")}
				</Button>
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
						<Button asChild size="sm">
							<Link
								to="/evaluate"
								search={{ suite: done.suite, agent: done.reference_agents[0] } as never}
								data-testid="mcp-draft-evaluate"
							>
								{t("draft.evaluate")}
							</Link>
						</Button>
					</div>
				</div>
			)}
		</Section>
	);
}

/** /mcp-test: inspect the org's MCP server, then draft a benchmark of it. */
export function McpTestView() {
	const { t } = useTranslation("mcptest");
	const { t: tf } = useTranslation("fw23");
	const [form, setForm] = useState<McpForm>(EMPTY_FORM);
	const session = useSession();
	// Secret names are for members and above (J9.secrets); a viewer types a name instead.
	const listSecrets = canUse(session, "member");
	const secrets = useListSecretsQuery(listSecrets ? undefined : skipToken);
	const [inspect, st] = useInspectMcpServerMutation();
	const [report, setReport] = useState<McpReport | null>(null);
	const [sent, setSent] = useState<McpInspectRequest | null>(null);
	const set = (p: Partial<McpForm>) => setForm((f) => ({ ...f, ...p }));
	const run = async () => {
		const req = requestFromForm(form);
		try {
			setReport(await inspect(req).unwrap());
			setSent(req);
		} catch {
			setReport(null);
		}
	};
	return (
		<section className="mx-auto flex w-full max-w-[1100px] min-w-0 flex-col gap-6" data-testid="mcp-test-screen">
			<PageHeader title={t("title")} subtitle={t("subtitle")} icon={Plug} />
			<Section title={t("form.heading")}>
				<label className="flex flex-col gap-1.5 text-sm">
					<span className="font-medium">{t("form.url")}</span>
					<Input
						value={form.url}
						onChange={(e) => set({ url: e.target.value })}
						placeholder="https://mcp.example.com/mcp"
						data-testid="mcp-url"
					/>
					<span className="text-muted-foreground text-xs">{t("form.urlHint")}</span>
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
						<span className="text-muted-foreground text-xs">{listSecrets ? t("form.secretHint") : tf("mcp.secretNameHint")}</span>
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
				<div className="flex items-center gap-3">
					<Button onClick={() => void run()} disabled={st.isLoading || !form.url.trim()} data-testid="mcp-inspect">
						{st.isLoading ? t("form.inspecting") : t("form.inspect")}
					</Button>
					{st.error != null && <span className="text-destructive text-sm">{apiErrorMessage(st.error, t("form.failed"))}</span>}
				</div>
			</Section>
			{report && <McpReportView report={report} />}
			{report?.reached && report.tools.length > 0 && sent && <DraftForm key={report.checked_at} report={report} request={sent} />}
		</section>
	);
}

export default McpTestView;