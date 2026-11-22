import { LoadError, Section } from "@/components/runs/screenStates";
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
import { Radio } from "lucide-react";
import { useEffect, useId, useState, type ChangeEvent } from "react";
import { useTranslation } from "react-i18next";

const POLL_MS = 5000;

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
	return (
		<div className="flex min-w-0 flex-col gap-6" data-testid="from-traces">
			<PageHeader
				title={t("title")}
				subtitle={t("subtitle")}
				icon={Radio}
				breadcrumbs={[{ label: t("crumbAgents"), to: "/agents" }, { label: t("crumbNew"), to: "/agents/new" }, { label: t("title") }]}
			/>
			{query.isError ? (
				<LoadError title={t("title")} error={query.error} onRetry={() => void query.refetch()} testId="from-traces-error" />
			) : (
				<>
					{info && <EndpointSection info={info} />}
					{draft ? (
						<DraftSection
							draft={draft}
							info={info}
							fromFile={Boolean(fromFile)}
							yaml={fromFile?.agent_yaml ?? info?.agent_yaml ?? ""}
							summary={fromFile?.summary ?? info?.summary ?? ""}
							canWrite={canWrite}
							onClearFile={() => setFromFile(null)}
						/>
					) : (
						<WaitingSection canWrite={canWrite} onInferred={setFromFile} />
					)}
					{draft && canWrite && <ConnectSection draft={draft} />}
				</>
			)}
		</div>
	);
}

function CopyBlock({ label, text, testId }: { label: string; text: string; testId: string }) {
	const { t } = useTranslation("connectTraces");
	const [copied, setCopied] = useState(false);
	return (
		<div className="flex min-w-0 flex-col gap-1">
			<div className="flex items-center justify-between gap-2">
				<span className="text-muted-foreground text-xs font-medium">{label}</span>
				<button
					type="button"
					className="text-primary text-xs font-medium"
					onClick={() => {
						void navigator.clipboard?.writeText(text).then(() => setCopied(true));
					}}
					data-testid={`${testId}-copy`}
				>
					{copied ? t("endpoint.copied") : t("endpoint.copy")}
				</button>
			</div>
			<pre className="bg-muted overflow-x-auto rounded-md p-3 text-xs whitespace-pre" data-testid={testId}>
				{text}
			</pre>
		</div>
	);
}

function EndpointSection({ info }: { info: TraceConnectInfo }) {
	const { t } = useTranslation("connectTraces");
	const session = useSession();
	const [token, setToken] = useState<string | null>(null);
	const [setupId, setSetupId] = useState(info.setup[0]?.id ?? "otel");
	const [createToken, tokenState] = useCreateInboxTokenMutation();
	const setup: TraceSetup | undefined = info.setup.find((s) => s.id === setupId) ?? info.setup[0];
	const isAdmin = canUse(session, "admin");
	return (
		<Section title={t("endpoint.title")} description={t("endpoint.description")} testId="from-traces-endpoint">
			<div className="flex min-w-0 flex-col gap-4">
				<CopyBlock label={t("endpoint.url")} text={info.endpoint} testId="from-traces-url" />
				<p className="text-muted-foreground text-xs">{t("endpoint.auth", { hint: info.token_hint })}</p>
				{info.hosted && (
					<div className="flex flex-wrap items-center gap-3">
						{isAdmin ? (
							<Button
								size="sm"
								variant="outline"
								disabled={tokenState.isLoading || Boolean(token)}
								onClick={async () => {
									const res = await createToken({ label: "otel traces" });
									if ("data" in res && res.data) setToken(res.data.secret);
								}}
								data-testid="from-traces-make-token"
							>
								{t("endpoint.makeToken")}
							</Button>
						) : (
							<span className="text-muted-foreground text-xs">{t("endpoint.tokenNeedsAdmin")}</span>
						)}
						{token && (
							<span className="text-xs" data-testid="from-traces-token">
								{t("endpoint.tokenShown")} <code className="bg-muted rounded px-1">{token}</code>
							</span>
						)}
						{tokenState.isError && <span className="text-destructive text-xs">{getBenchErrorMessage(tokenState.error)}</span>}
					</div>
				)}
				<div className="flex flex-wrap gap-2" role="tablist">
					{info.setup.map((s) => (
						<button
							key={s.id}
							type="button"
							role="tab"
							aria-selected={s.id === setup?.id}
							className={
								"rounded-md border px-2 py-1 text-xs " + (s.id === setup?.id ? "border-primary text-foreground font-medium" : "text-muted-foreground")
							}
							onClick={() => setSetupId(s.id)}
							data-testid={`from-traces-setup-${s.id}`}
						>
							{s.label}
						</button>
					))}
				</div>
				{setup && (
					<div className="flex min-w-0 flex-col gap-3">
						{setup.install && <CopyBlock label={t("endpoint.install")} text={setup.install} testId="from-traces-install" />}
						<CopyBlock label={t("endpoint.env")} text={withToken(setup.env, token)} testId="from-traces-env" />
						{setup.code && <CopyBlock label={t("endpoint.code")} text={setup.code} testId="from-traces-code" />}
					</div>
				)}
			</div>
		</Section>
	);
}

function WaitingSection({ canWrite, onInferred }: { canWrite: boolean; onInferred: (r: TraceInferResult) => void }) {
	const { t } = useTranslation("connectTraces");
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
		<Section title={t("waiting.title")} description={t("waiting.description")} testId="from-traces-waiting">
			<div className="flex flex-col gap-2">
				<span className="text-muted-foreground flex items-center gap-2 text-xs">
					<span className="bg-primary size-2 animate-pulse rounded-full" aria-hidden />
					{t("waiting.description")}
				</span>
				{canWrite && (
					<label htmlFor={fileId} className="flex flex-col gap-1 text-xs">
						<span className="text-muted-foreground">{t("waiting.orFile")}</span>
						<input
							id={fileId}
							type="file"
							accept=".json,.pb,.bin,application/json,application/x-protobuf"
							disabled={inferState.isLoading}
							onChange={(e) => void onFile(e)}
							data-testid="from-traces-file"
						/>
					</label>
				)}
				{error && (
					<p className="text-destructive text-xs" data-testid="from-traces-file-error">
						{t("waiting.fileError", { error })}
					</p>
				)}
			</div>
		</Section>
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
	draft,
	info,
	fromFile,
	yaml,
	summary,
	canWrite,
	onClearFile,
}: {
	draft: TraceDraft;
	info?: TraceConnectInfo;
	fromFile: boolean;
	yaml: string;
	summary: string;
	canWrite: boolean;
	onClearFile: () => void;
}) {
	const { t } = useTranslation("connectTraces");
	const [forget, forgetState] = useForgetConnectTracesMutation();
	return (
		<Section
			title={t("draft.title")}
			description={fromFile ? t("draft.fromFile") : t("draft.received", { traces: draft.traces, spans: info?.spans_kept ?? draft.spans, exports: info?.exports ?? 0 })}
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
									<span className="font-mono">{tl.name}</span> <span className="text-muted-foreground">{t("draft.toolCalls", { calls: tl.calls })}</span>
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
							<Button size="sm" variant="ghost" disabled={forgetState.isLoading} onClick={() => void forget()} data-testid="from-traces-forget">
								{t("draft.forget")}
							</Button>
						)
					)}
				</div>
			</div>
		</Section>
	);
}

function ConnectSection({ draft }: { draft: TraceDraft }) {
	const { t } = useTranslation("connectTraces");
	const navigate = useNavigate();
	const [create, createState] = useCreateAgentFromTemplateMutation();
	const template = draft.template || "http-url";
	const proposedURL = draft.params?.url ?? draft.params?.card_url ?? "";
	const [id, setID] = useState(draft.id);
	const [url, setURL] = useState(proposedURL);
	const [authSecret, setAuthSecret] = useState("");
	const ids = useId();
	useEffect(() => {
		setID(draft.id);
		setURL(proposedURL);
	}, [draft.id, proposedURL]);
	if (template === "wrap-app") {
		return (
			<Section title={t("connect.title")} description={t("connect.wrapHint")} testId="from-traces-connect">
				<Link to="/agents/new" search={{ template: "wrap-app" } as never} className="text-primary text-xs font-medium underline underline-offset-2">
					{t("connect.openWrap")}
				</Link>
			</Section>
		);
	}
	const req = connectRequest(draft, { id, url, authSecret });
	return (
		<Section title={t("connect.title")} description={t("connect.description")} testId="from-traces-connect">
			<div className="flex max-w-xl flex-col gap-3">
				<label className="flex flex-col gap-1 text-xs" htmlFor={`${ids}-id`}>
					<span className="font-medium">{t("connect.name")}</span>
					<Input id={`${ids}-id`} value={id} onChange={(e) => setID(e.target.value)} data-testid="from-traces-id" />
				</label>
				<label className="flex flex-col gap-1 text-xs" htmlFor={`${ids}-url`}>
					<span className="font-medium">{template === "a2a-card" ? t("connect.urlCard") : t("connect.url")}</span>
					<Input id={`${ids}-url`} value={url} placeholder="https://agent.example.com/chat" onChange={(e) => setURL(e.target.value)} data-testid="from-traces-endpoint-url" />
				</label>
				{template !== "a2a-card" && (
					<label className="flex flex-col gap-1 text-xs" htmlFor={`${ids}-secret`}>
						<span className="font-medium">{t("connect.secret")}</span>
						<Input id={`${ids}-secret`} value={authSecret} placeholder="MY_AGENT_TOKEN" onChange={(e) => setAuthSecret(e.target.value)} data-testid="from-traces-secret" />
						<span className="text-muted-foreground">{t("connect.secretHint")}</span>
					</label>
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
					{!req && <span className="text-muted-foreground text-xs">{t("connect.incomplete")}</span>}
					{createState.isError && (
						<span className="text-destructive text-xs" data-testid="from-traces-connect-error">
							{t("connect.failed", { error: getBenchErrorMessage(createState.error) })}
						</span>
					)}
				</div>
			</div>
		</Section>
	);
}
