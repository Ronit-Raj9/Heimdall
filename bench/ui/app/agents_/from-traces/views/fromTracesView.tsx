import { type DestructiveConfirmOptions, useConfirm } from "@/components/platform/confirmDialog";
import { cn } from "@/lib/utils";
import { pageWidthClass } from "@/components/shell/pageWidth";
import { InlineError } from "@/components/runs/screenStates";
import { notify } from "@/lib/notify";
import { LoadError } from "@/components/runs/screenStates";
import { NewSecretInline, SecretNameField } from "@/app/agents_/new/views/connectForm";
import { PageHeader } from "@/components/shell";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { canUse, useSession } from "@/lib/session/sessionContext";
import { useCreateAgentFromTemplateMutation } from "@/lib/store/apis/agentBuilderApi";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import {
	connectRequest,
	useForgetConnectTracesMutation,
	useGetConnectTracesQuery,
	useInferFromTraceFileMutation,
	withToken,
} from "@/lib/store/apis/connectTracesApi";
import { useCreateInboxTokenMutation } from "@/lib/store/apis/liveinboxApi";
import type { TraceConnectInfo, TraceDraft, TraceInferResult, TraceSetup } from "@/lib/types/connectTraces";
import { Link, useNavigate } from "@tanstack/react-router";
import { Check, Copy, Radio, ShieldCheck, Upload } from "lucide-react";
import { useEffect, useId, useRef, useState, type ChangeEvent, type ReactNode } from "react";
import "@/components/improvements/uf2bI18n";
import { useTranslation } from "react-i18next";
import "@/app/agents/views/w4m1I18n";
import "@/app/agents/views/ufreI18n";

const POLL_MS = 5000;
/** After this long with no trace, the waiting step says what to check. */
export const STALE_MS = 2 * 60_000;
const TOKEN_VAR = "$HEIMDALL_INGEST_TOKEN";

/** Every setting line of an environment block as an export line (Q-35); blank lines and comments stay as they are. */
export function exportLines(env: string): string {
	return env
		.split("\n")
		.map((line) => (line.trim() === "" || line.trimStart().startsWith("#") || line.trimStart().startsWith("export ") ? line : `export ${line}`))
		.join("\n");
}

/**
 * The environment block as shown: with a token made on this page, the token filled in; without
 * one, an export line for HEIMDALL_INGEST_TOKEN first, so nothing is copied with an unset variable.
 * Every setting line is an export line, so the block works pasted into a shell as is.
 */
export function envBlock(env: string, token: string | null): { text: string; needsExport: boolean } {
	if (token) return { text: exportLines(withToken(env, token)), needsExport: false };
	if (!env.includes(TOKEN_VAR)) return { text: exportLines(env), needsExport: false };
	return { text: exportLines(`HEIMDALL_INGEST_TOKEN=<your hmi_ token>\n${env}`), needsExport: true };
}

/**
 * A request that checks the endpoint and the token without an agent (Q-277): it sends an empty
 * OTLP export, which keeps nothing; a 2xx answer means both work, 401 means the token is wrong.
 */
export function testRequest(endpoint: string): string {
	return `curl -i -X POST '${endpoint}' -H "Authorization: Bearer ${TOKEN_VAR}" -H 'Content-Type: application/json' -d '{"resourceSpans":[]}'`;
}

/** A setup chip label "Name (frameworks)" split into the short name and the frameworks it covers. */
export function setupChip(label: string): { name: string; covers: string } {
	const m = /^(.*?)\s*\((.+)\)\s*$/.exec(label);
	return m ? { name: m[1], covers: m[2] } : { name: label, covers: "" };
}

/** Which Connect field is wrong, first one first, or null when the request can be made. */
export function connectProblem(edits: { id: string; url: string; authSecret: string }): "name" | "url" | "secret" | null {
	if (!/^[a-z0-9][a-z0-9._-]{0,62}$/.test(edits.id.trim())) return "name";
	if (!/^https?:\/\/\S+$/i.test(edits.url.trim())) return "url";
	if (edits.authSecret.trim() && !/^[A-Za-z_][A-Za-z0-9_]*$/.test(edits.authSecret.trim())) return "secret";
	return null;
}

/**
 * A numbered step of the page: a marker (filled once the step is done), the heading and the full
 * description, never cut. Screen readers hear "Step 1 of 3" instead of a typed "1.".
 */
function StepSection({
	n,
	total,
	done,
	title,
	description,
	testId,
	children,
}: {
	n: number;
	total: number;
	done: boolean;
	title: string;
	description?: ReactNode;
	testId: string;
	children: ReactNode;
}) {
	const { t } = useTranslation("uf2b");
	return (
		<section className="flex min-w-0 flex-col gap-3" data-testid={testId}>
			<div className="flex min-w-0 items-start gap-3">
				<span
					aria-hidden="true"
					data-testid={`${testId}-marker`}
					data-done={done || undefined}
					className={
						"mt-0.5 flex size-6 shrink-0 items-center justify-center rounded-full border text-xs font-medium " +
						(done ? "bg-primary text-primary-foreground border-primary" : "text-muted-foreground")
					}
				>
					{done ? <Check className="size-3.5" /> : n}
				</span>
				<div className="flex min-w-0 flex-col gap-0.5">
					<h2 className="text-foreground text-base font-semibold tracking-tight">
						<span className="sr-only">{t("traces.step", { n, total })}: </span>
						{title}
					</h2>
					{description && <div className="text-muted-foreground max-w-prose text-sm">{description}</div>}
				</div>
			</div>
			{children}
		</section>
	);
}

/**
 * Connect from traces (/agents/from-traces): the org's OTLP endpoint and setup lines, then the agent
 * the first traces show ("support-bot, langgraph, gpt-5.4-mini, tools seen: ..."), with what each
 * field came from, the questions still open, an agent.yaml to commit and Connect this agent.
 */
export default function FromTracesView() {
	const { t } = useTranslation("connectTraces");
	const session = useSession();
	const canWrite = canUse(session, "member");
	const [fromFile, setFromFile] = useState<TraceInferResult | null>(null);
	const query = useGetConnectTracesQuery(undefined, { pollingInterval: fromFile ? 0 : POLL_MS, skipPollingIfUnfocused: true });
	const info = query.data;
	const draft = fromFile?.draft ?? info?.draft;
	// Step 1 counts as done once a token was made here or traces arrived.
	const [tokenMade, setTokenMade] = useState(false);
	const total = draft && canWrite ? 3 : 2;
	return (
		<div className={cn(pageWidthClass("read"), "flex flex-col gap-6")} data-page-width="read" data-testid="from-traces">
			<PageHeader
				title={t("title")}
				icon={Radio}
			/>
			{query.isError ? (
				<LoadError title={t("title")} error={query.error} onRetry={() => void query.refetch()} testId="from-traces-error" />
			) : (
				<>
					{info && <EndpointSection info={info} total={total} done={tokenMade || Boolean(draft)} onToken={() => setTokenMade(true)} />}
					{draft ? (
						<DraftSection
							total={total}
							draft={draft}
							info={info}
							fromFile={Boolean(fromFile)}
							yaml={fromFile?.agent_yaml ?? info?.agent_yaml ?? ""}
							summary={fromFile?.summary ?? info?.summary ?? ""}
							canWrite={canWrite}
							onClearFile={() => setFromFile(null)}
						/>
					) : (
						<WaitingSection
							canWrite={canWrite}
							onInferred={setFromFile}
							total={total}
							endpoint={info?.endpoint}
							checkedAt={(query as { fulfilledTimeStamp?: number }).fulfilledTimeStamp}
						/>
					)}
					{draft && canWrite && <ConnectSection draft={draft} total={total} />}
				</>
			)}
		</div>
	);
}

function CopyBlock({ label, text, testId, single }: { label: string; text: string; testId: string; single?: boolean }) {
	const { t } = useTranslation("connectTraces");
	const [copied, setCopied] = useState(false);
	return (
		<div className="flex min-w-0 flex-col gap-1">
			<div className="flex items-center justify-between gap-2">
				<span className="text-muted-foreground text-sm font-medium">{label}</span>
				<Button
					type="button"
					size="sm"
					variant="ghost"
					className="min-h-8"
					onClick={() => {
						void navigator.clipboard?.writeText(text).then(() => setCopied(true));
					}}
					data-testid={`${testId}-copy`}
				>
					{copied ? <Check className="size-3.5" aria-hidden="true" /> : <Copy className="size-3.5" aria-hidden="true" />}
					{copied ? t("endpoint.copied") : t("endpoint.copy")}
				</Button>
			</div>
			{single ? (
				<pre className="bg-muted rounded-md p-3 text-xs break-all whitespace-pre-wrap" data-testid={testId}>
					{text}
				</pre>
			) : (
				// A right-edge fade says the line goes on; the block scrolls sideways on phones.
				<div className="relative min-w-0">
					<pre className="bg-muted overflow-x-auto rounded-md p-3 pr-8 text-xs whitespace-pre" data-testid={testId}>
						{text}
					</pre>
					<span
						aria-hidden="true"
						className="from-muted pointer-events-none absolute inset-y-0 right-0 w-8 rounded-r-md bg-gradient-to-l to-transparent"
					/>
				</div>
			)}
		</div>
	);
}

function EndpointSection({
	info,
	total,
	done,
	onToken,
}: {
	info: TraceConnectInfo;
	total: number;
	done: boolean;
	onToken: () => void;
}) {
	const { t } = useTranslation("connectTraces");
	const { t: tu } = useTranslation("uf2b");
	const { t: tm } = useTranslation("w4m1");
	const session = useSession();
	const [token, setToken] = useState<string | null>(null);
	const [setupId, setSetupId] = useState(info.setup[0]?.id ?? "otel");
	const [createToken, tokenState] = useCreateInboxTokenMutation();
	const setup: TraceSetup | undefined = info.setup.find((s) => s.id === setupId) ?? info.setup[0];
	const isAdmin = canUse(session, "admin");
	const env = setup ? envBlock(setup.env, token) : undefined;
	return (
		<StepSection
			n={1}
			total={total}
			done={done}
			title={t("endpoint.title")}
			description={
				<>
					<p className="text-foreground flex items-start gap-2 text-sm" data-testid="from-traces-privacy">
						<ShieldCheck className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
						{t("endpoint.privacy")}
					</p>
				</>
			}
			testId="from-traces-endpoint"
		>
			<div className="flex min-w-0 flex-col gap-4">
				<CopyBlock label={t("endpoint.url")} text={info.endpoint} testId="from-traces-url" single />
				<p className="text-sm">{t("endpoint.auth")}</p>
				{info.hosted && (
					<div className="flex flex-wrap items-center gap-3">
						{isAdmin ? (
							<Button
								size="sm"
								variant="outline"
								disabled={tokenState.isLoading || Boolean(token)}
								onClick={async () => {
									const res = await createToken({ label: "otel traces" });
									if ("data" in res && res.data) {
										setToken(res.data.secret);
										onToken();
									}
								}}
								data-testid="from-traces-make-token"
							>
								{t("endpoint.makeToken")}
							</Button>
						) : (
							<span className="text-muted-foreground text-sm">{t("endpoint.tokenNeedsAdmin")}</span>
						)}
						{token && (
							<span className="text-sm break-all" data-testid="from-traces-token">
								{t("endpoint.tokenShown")} <code className="bg-muted rounded px-1">{token}</code>
							</span>
						)}
						{token && (
							<a href="/failures" className="text-primary text-xs underline-offset-4 hover:underline" data-testid="from-traces-ingest-note">
								{tm("tokens.ingestPanel")}
							</a>
						)}
						{tokenState.isError && <span className="text-destructive text-xs">{getBenchErrorMessage(tokenState.error)}</span>}
					</div>
				)}
				<div className="flex flex-col gap-2">
					<span className="text-sm font-medium" id="from-traces-libraries">
						{tu("traces.libraries")}
					</span>
					<div className="flex flex-wrap gap-2" role="tablist" aria-labelledby="from-traces-libraries">
						{info.setup.map((s) => (
							<button
								key={s.id}
								type="button"
								role="tab"
								aria-selected={s.id === setup?.id}
								className={
									"min-h-8 rounded-md border px-3 py-1 text-sm " +
									(s.id === setup?.id ? "border-primary text-foreground font-medium" : "text-muted-foreground")
								}
								onClick={() => setSetupId(s.id)}
								data-testid={`from-traces-setup-${s.id}`}
							>
								{setupChip(s.label).name}
							</button>
						))}
					</div>
					{setup && setupChip(setup.label).covers && (
						<p className="text-muted-foreground text-sm" data-testid="from-traces-setup-covers">
							{tu("traces.worksWith", { list: setupChip(setup.label).covers })}
						</p>
					)}
				</div>
				{setup && (
					<div className="flex min-w-0 flex-col gap-3">
						{setup.install && <CopyBlock label={t("endpoint.install")} text={setup.install} testId="from-traces-install" />}
						{env?.needsExport && (
							<p className="text-sm" data-testid="from-traces-env-note">
								{tu("traces.envNoToken")} {t("endpoint.envHelp")}
							</p>
						)}
						<CopyBlock label={t("endpoint.env")} text={env?.text ?? ""} testId="from-traces-env" />
						{setup.code && <CopyBlock label={t("endpoint.code")} text={setup.code} testId="from-traces-code" />}
					</div>
				)}
			</div>
		</StepSection>
	);
}

/** The server's one plain sentence about a file that is not a trace export, shown as is. */
export function TraceFileError({ error }: { error: string }) {
	return (
		<p className="text-destructive text-xs" data-testid="from-traces-file-error">
			{error}
		</p>
	);
}

function WaitingSection({
	canWrite,
	onInferred,
	total,
	endpoint,
	checkedAt,
}: {
	canWrite: boolean;
	onInferred: (r: TraceInferResult) => void;
	total: number;
	endpoint?: string;
	checkedAt?: number;
}) {
	const { t: tr } = useTranslation("ufre");
	const { t } = useTranslation("connectTraces");
	const { t: tu } = useTranslation("uf2b");
	const fileRef = useRef<HTMLInputElement>(null);
	const [stale, setStale] = useState(false);
	useEffect(() => {
		const timer = window.setTimeout(() => setStale(true), STALE_MS);
		return () => window.clearTimeout(timer);
	}, []);
	const checked = checkedAt ? new Date(checkedAt).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }) : null;
	const [infer, inferState] = useInferFromTraceFileMutation();
	const [error, setError] = useState<string | null>(null);
	const fileId = useId();
	const onFile = async (e: ChangeEvent<HTMLInputElement>) => {
		const file = e.target.files?.[0];
		if (!file) return;
		setError(null);
		const protobuf = /\.(pb|bin|proto)$/i.test(file.name);
		const body = protobuf ? await file.arrayBuffer() : await file.text();
		const res = await infer({ body, protobuf });
		if ("data" in res && res.data) onInferred(res.data);
		else if ("error" in res) setError(getBenchErrorMessage(res.error));
	};
	return (
		<StepSection n={2} total={total} done={false} title={t("waiting.title")} description={t("waiting.description")} testId="from-traces-waiting">
			<div className="flex flex-col gap-4">
				<span className="text-muted-foreground flex items-center gap-2 text-sm" aria-live="polite" data-testid="from-traces-waiting-status">
					<span className="bg-primary size-2 rounded-full motion-safe:animate-pulse" aria-hidden />
					{checked ? tu("traces.waitingChecked", { time: checked }) : tu("traces.waitingStatus")}
				</span>
				{stale && (
					<p className="text-chart-warning-ink text-sm" role="status" data-testid="from-traces-stale">
						{tu("traces.stale")}{" "}
						<Link to="/failures" className="text-primary underline underline-offset-2">
							{tu("traces.staleLink")}
						</Link>
					</p>
				)}
				{stale && endpoint && (
					<div className="flex min-w-0 flex-col gap-1" data-testid="from-traces-stale-test">
						<p className="text-muted-foreground text-sm">{tr("traces.testHelp")}</p>
						<CopyBlock label={tr("traces.testLabel")} text={testRequest(endpoint)} testId="from-traces-test-request" />
					</div>
				)}
				{canWrite && (
					<div className="flex flex-col gap-2 border-t pt-4">
						<h3 className="text-sm font-semibold">{tu("traces.fileTitle")}</h3>
						<p className="text-muted-foreground text-sm">{t("waiting.orFile")}</p>
						<input
							ref={fileRef}
							id={fileId}
							type="file"
							className="sr-only"
							tabIndex={-1}
							accept=".json,.pb,.bin,application/json,application/x-protobuf"
							disabled={inferState.isLoading}
							onChange={(e) => void onFile(e)}
							data-testid="from-traces-file"
						/>
						<Button
							type="button"
							variant="outline"
							className="w-fit"
							disabled={inferState.isLoading}
							onClick={() => fileRef.current?.click()}
							data-testid="from-traces-file-button"
						>
							<Upload className="size-4" aria-hidden="true" />
							{t("waiting.pickFile")}
						</Button>
					</div>
				)}
				{error && <TraceFileError error={error} />}
			</div>
		</StepSection>
	);
}

/** "agent.yaml" as a download, made in the browser. */
function downloadYAML(text: string) {
	const url = URL.createObjectURL(new Blob([text], { type: "application/yaml" }));
	const a = document.createElement("a");
	a.href = url;
	a.download = "agent.yaml";
	a.click();
	URL.revokeObjectURL(url);
}

export function DraftSection({
	total = 2,
	draft,
	info,
	fromFile,
	yaml,
	summary,
	canWrite,
	onClearFile,
}: {
	total?: number;
	draft: TraceDraft;
	info?: TraceConnectInfo;
	fromFile: boolean;
	yaml: string;
	summary: string;
	canWrite: boolean;
	onClearFile: () => void;
}) {
	const { t } = useTranslation("connectTraces");
	const { t: tu } = useTranslation("uf2b");
	const confirm = useConfirm();
	const [forget, forgetState] = useForgetConnectTracesMutation();
	// A failed Forget shows why next to the button (M1-44).
	const [forgetError, setForgetError] = useState<unknown>(null);
	const onForget = async () => {
		const opts: DestructiveConfirmOptions = {
			title: tu("traces.forgetConfirmTitle"),
			confirmLabel: tu("traces.forgetConfirmLabel"),
			destructive: true,
		};
		const ok = await confirm(tu("traces.forgetConfirm"), opts);
		if (!ok) return;
		setForgetError(null);
		try {
			await forget().unwrap();
			notify({ text: tu("traces.forgotten") });
		} catch (e) {
			setForgetError(e);
		}
	};
	return (
		<StepSection
			n={2}
			total={total}
			done
			title={t("draft.title")}
			description={
				fromFile
					? t("draft.fromFile")
					: t("draft.received", { traces: draft.traces, spans: info?.spans_kept ?? draft.spans, exports: info?.exports ?? 0 })
			}
			testId="from-traces-draft"
		>
			<div className="flex min-w-0 flex-col gap-4">
				<p className="text-foreground text-sm font-medium" data-testid="from-traces-summary">
					{summary}
				</p>
				<p className="text-muted-foreground text-xs" data-testid="from-traces-confidence">
					{t("draft.confidence", { level: t(`draft.confidenceLevel.${draft.confidence}`) })}
				</p>
				{(draft.evidence?.length ?? 0) > 0 && (
					<div className="min-w-0 overflow-x-auto">
						<table className="w-full text-left text-xs" data-testid="from-traces-evidence">
							<caption className="text-muted-foreground mb-1 text-left font-medium">{t("draft.seen")}</caption>
							<thead>
								<tr className="text-muted-foreground">
									<th className="py-1 pr-3 font-medium">{t("draft.field")}</th>
									<th className="py-1 pr-3 font-medium">{t("draft.value")}</th>
									<th className="py-1 font-medium">{t("draft.from")}</th>
								</tr>
							</thead>
							<tbody>
								{draft.evidence!.map((e) => (
									<tr key={e.field} className="border-t">
										<td className="py-1 pr-3">{e.field}</td>
										<td className="py-1 pr-3 font-mono">{e.value}</td>
										<td className="text-muted-foreground py-1">{e.from}</td>
									</tr>
								))}
							</tbody>
						</table>
					</div>
				)}
				{(draft.tools?.length ?? 0) > 0 && (
					<div className="flex flex-col gap-1">
						<span className="text-muted-foreground text-xs font-medium">{t("draft.tools")}</span>
						<ul className="flex flex-wrap gap-2" data-testid="from-traces-tools">
							{draft.tools!.map((tl) => (
								<li key={tl.name} className="rounded-md border px-2 py-1 text-xs">
									<span className="font-mono">{tl.name}</span>{" "}
									<span className="text-muted-foreground">{t("draft.toolCalls", { calls: tl.calls })}</span>
									{tl.errors ? <span className="text-destructive"> {t("draft.toolErrors", { errors: tl.errors })}</span> : null}
								</li>
							))}
						</ul>
					</div>
				)}
				{(draft.tools_declared?.length ?? 0) > 0 && (
					<p className="text-xs">
						<span className="text-muted-foreground font-medium">{t("draft.declared")}: </span>
						{draft.tools_declared!.join(", ")}
					</p>
				)}
				{(draft.subagents?.length ?? 0) > 0 && (
					<p className="text-xs">
						<span className="text-muted-foreground font-medium">{t("draft.subagents")}: </span>
						{draft.subagents!.join(", ")}
					</p>
				)}
				{(draft.packs?.length ?? 0) > 0 && (
					<p className="text-xs" data-testid="from-traces-packs">
						<span className="text-muted-foreground font-medium">{t("draft.hints")}: </span>
						{draft.packs!.join(", ")}
					</p>
				)}
				{(draft.questions?.length ?? 0) > 0 && (
					<div className="flex flex-col gap-1">
						<span className="text-muted-foreground text-xs font-medium">{t("draft.questions")}</span>
						<ul className="list-disc pl-5 text-xs" data-testid="from-traces-questions">
							{draft.questions!.map((q) => (
								<li key={q}>{q}</li>
							))}
						</ul>
					</div>
				)}
				{yaml && (
					<details>
						<summary className="text-primary cursor-pointer text-xs font-medium">.heimdall/agent.yaml</summary>
						<pre className="bg-muted mt-2 overflow-x-auto rounded-md p-3 text-xs" data-testid="from-traces-yaml">
							{yaml}
						</pre>
						<p className="text-muted-foreground mt-1 text-xs">{t("draft.yamlHint")}</p>
					</details>
				)}
				<div className="flex flex-wrap gap-2">
					{yaml && (
						<Button size="sm" variant="outline" onClick={() => downloadYAML(yaml)} data-testid="from-traces-download">
							{t("draft.download")}
						</Button>
					)}
					{fromFile ? (
						<Button size="sm" variant="ghost" onClick={onClearFile} data-testid="from-traces-clear-file">
							{t("draft.forget")}
						</Button>
					) : (
						canWrite && (
							<Button
								size="sm"
								variant="ghost"
								disabled={forgetState.isLoading}
								onClick={() => void onForget()}
								data-testid="from-traces-forget"
							>
								{t("draft.forget")}
							</Button>
						)
					)}
				</div>
				<InlineError error={forgetError} onRetry={() => void onForget()} testId="from-traces-forget-error" />
			</div>
		</StepSection>
	);
}

function ConnectSection({ draft, total }: { draft: TraceDraft; total: number }) {
	const { t } = useTranslation("connectTraces");
	const { t: tu } = useTranslation("uf2b");
	const navigate = useNavigate();
	const [create, createState] = useCreateAgentFromTemplateMutation();
	const template = draft.template || "http-url";
	const proposedURL = draft.params?.url ?? draft.params?.card_url ?? "";
	const [id, setID] = useState(draft.id);
	const [url, setURL] = useState(proposedURL);
	const [authSecret, setAuthSecret] = useState("");
	const ids = useId();
	// The poll may bring a draft before or after the person edits: fill id and URL once, never over typing.
	const filled = useRef(Boolean(draft.id || proposedURL));
	useEffect(() => {
		if (filled.current) return;
		if (!draft.id && !proposedURL) return;
		filled.current = true;
		setID(draft.id);
		setURL(proposedURL);
	}, [draft.id, proposedURL]);
	if (template === "wrap-app") {
		return (
			<StepSection n={3} total={total} done={false} title={t("connect.title")} description={t("connect.wrapHint")} testId="from-traces-connect">
				<Link
					to="/agents/new"
					search={{ template: "wrap-app" } as never}
					className="text-primary text-sm font-medium underline underline-offset-2"
				>
					{t("connect.openWrap")}
				</Link>
			</StepSection>
		);
	}
	const req = connectRequest(draft, { id, url, authSecret });
	const problem = req ? null : connectProblem({ id, url, authSecret });
	return (
		<StepSection n={3} total={total} done={false} title={t("connect.title")} testId="from-traces-connect">
			<div className="flex max-w-xl flex-col gap-3">
				<label className="flex flex-col gap-1 text-sm" htmlFor={`${ids}-id`}>
					<span className="font-medium">{t("connect.name")}</span>
					<Input id={`${ids}-id`} value={id} onChange={(e) => setID(e.target.value)} data-testid="from-traces-id" />
				</label>
				<label className="flex flex-col gap-1 text-sm" htmlFor={`${ids}-url`}>
					<span className="font-medium">{template === "a2a-card" ? t("connect.urlCard") : t("connect.url")}</span>
					<Input
						id={`${ids}-url`}
						value={url}
						placeholder="https://agent.example.com/chat"
						onChange={(e) => setURL(e.target.value)}
						data-testid="from-traces-endpoint-url"
					/>
				</label>
				{template !== "a2a-card" && (
					<>
					<label className="flex flex-col gap-1 text-sm" htmlFor={`${ids}-secret`}>
						<span className="font-medium">{t("connect.secret")}</span>
						<SecretNameField
							id={`${ids}-secret`}
							value={authSecret}
							placeholder="MY_AGENT_TOKEN"
							onChange={setAuthSecret}
							testId="from-traces-secret"
						/>
						<span className="text-muted-foreground">{t("connect.secretHint")}</span>
					</label>
					<NewSecretInline param="from-traces" onSaved={setAuthSecret} />
					</>
				)}
				<div className="flex flex-wrap items-center gap-3">
					<Button
						disabled={!req || createState.isLoading}
						onClick={async () => {
							if (!req) return;
							const res = await create(req);
							if ("data" in res && res.data) void navigate({ to: "/agents/$agentId", params: { agentId: res.data.id } });
						}}
						data-testid="from-traces-submit"
					>
						{t("connect.submit")}
					</Button>
					{!req && (
						<span className="text-muted-foreground text-sm" data-testid="from-traces-incomplete">
							{problem === "name"
								? tu("traces.missingName")
								: problem === "url"
									? tu("traces.missingUrl")
									: problem === "secret"
										? tu("traces.missingSecret")
										: t("connect.incomplete")}
						</span>
					)}
					{createState.isError && (
						<span className="text-destructive text-xs" data-testid="from-traces-connect-error">
							{t("connect.failed", { error: getBenchErrorMessage(createState.error) })}
						</span>
					)}
				</div>
			</div>
		</StepSection>
	);
}