import { JobRunButton } from "@/components/jobs/jobRunButton";
import { ConfirmButton } from "@/components/platform/badges";
import { BlockSkeleton, LoadError, Section } from "@/components/runs/screenStates";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { useSession } from "@/lib/session/sessionContext";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { inboxApi } from "@/lib/store/apis/inboxApi";
import {
	liveinboxApi,
	useCreateInboxTokenMutation,
	useGetInboxTokensQuery,
	useGetInboxWebhookQuery,
	useRevokeInboxTokenMutation,
} from "@/lib/store/apis/liveinboxApi";
import { roleAtLeast } from "@/lib/store/apis/session";
import { useListSecretsQuery } from "@/lib/store/apis/secretsApi";
import { useAppDispatch } from "@/lib/store/hooks";
import type { InboxSourceParams, InboxTokenCreated, InboxTokenList, InboxWebhookInfo, PullCheck } from "@/lib/types/liveinbox";
import { PullSteps } from "@/components/inbox/pullSteps";
import { revealAnchor } from "@/components/inbox/featureHero";
import { Check, Copy } from "lucide-react";
import { useId, useState, type FormEvent } from "react";
import "./uf7aI18n";
import { useTranslation } from "react-i18next";

const SINCE_OPTIONS = ["", "24h", "7d", "30d"] as const;

/** A random 32-byte token as hex, made in the browser; Heimdall never sees it. Exported for tests. */
export function generateInboxToken(random: (a: Uint8Array) => Uint8Array = (a) => crypto.getRandomValues(a)): string {
	return Array.from(random(new Uint8Array(32)), (b) => b.toString(16).padStart(2, "0")).join("");
}

/** "Pull now" for the inbox: queues inbox.pull over every pull source of inbox/sources.yaml. Hosted and local alike; while a pull still needs a source or a secret it links to the setup below. */
export function InboxPullButton({ check }: { check?: PullCheck }) {
	const { t } = useTranslation("liveinbox");
	if (check && !check.ready) {
		return (
			<a
				href="#inbox-source-setup"
				onClick={(e) => {
					e.preventDefault();
					revealAnchor("inbox-source-setup");
				}}
				className="text-primary text-xs font-medium underline-offset-2 hover:underline"
				data-testid="inbox-pull-setup-link"
			>
				{t("inbox.pull.needsSetup")}
			</a>
		);
	}
	return <InboxPullControls />;
}

/** Builds inbox.source params from the form; null while a required field is empty. Exported for tests. */
export function inboxSourceParams(v: InboxSourceParams): InboxSourceParams | null {
	const p: InboxSourceParams = { name: v.name.trim(), kind: v.kind };
	const set = (k: keyof InboxSourceParams) => {
		const x = (v[k] ?? "").trim();
		if (x) (p as unknown as Record<string, string>)[k] = x;
	};
	if (!p.name) return null;
	if (v.kind === "bifrost") {
		set("url");
		set("token_env");
		if (!p.url) return null;
	} else if (v.kind === "langfuse") {
		set("host");
		set("public_key_env");
		set("secret_key_env");
		set("project");
		if (!p.host || !p.public_key_env || !p.secret_key_env) return null;
	} else {
		set("host");
		set("api_key_env");
		set("project");
		if (!p.host || !p.api_key_env || !p.project) return null;
	}
	return p;
}

/** The required fields of a source form that are still empty, in form order. Exported for tests. */
export function inboxSourceMissing(v: InboxSourceParams): (keyof InboxSourceParams)[] {
	return REQUIRED_FIELDS[v.kind].filter((k) => !((v[k] as string | undefined) ?? "").trim());
}

/** The fields inboxSourceParams needs per tool. */
const REQUIRED_FIELDS: Record<InboxSourceParams["kind"], (keyof InboxSourceParams)[]> = {
	bifrost: ["name", "url"],
	langfuse: ["name", "host", "public_key_env", "secret_key_env"],
	langsmith: ["name", "host", "api_key_env", "project"],
};

/** The same form with another tool: the name and the address typed so far are kept (M7-6). Exported for tests. */
export function switchSourceKind(v: InboxSourceParams, kind: InboxSourceParams["kind"]): InboxSourceParams {
	const next = { ...SOURCE_DEFAULTS[kind] };
	const typedName = v.name.trim() && v.name !== SOURCE_DEFAULTS[v.kind].name ? v.name : next.name;
	const address = (v.kind === "bifrost" ? v.url : v.host) ?? "";
	const typedAddress =
		address.trim() && address !== (v.kind === "bifrost" ? SOURCE_DEFAULTS.bifrost.url : SOURCE_DEFAULTS[v.kind].host) ? address : "";
	next.name = typedName;
	if (typedAddress) {
		if (kind === "bifrost") next.url = typedAddress;
		else next.host = typedAddress;
	}
	return next;
}

/** The placeholder of each secret-name field: a usual name, shown but not filled in, since nothing is stored under it yet (M7-7). */
const SECRET_PLACEHOLDERS: Partial<Record<keyof InboxSourceParams, string>> = {
	token_env: "BIFROST_ADMIN_TOKEN",
};

const SOURCE_DEFAULTS: Record<InboxSourceParams["kind"], InboxSourceParams> = {
	bifrost: { name: "gateway", kind: "bifrost", url: "", token_env: "" },
	langfuse: {
		name: "langfuse",
		kind: "langfuse",
		host: "https://cloud.langfuse.com",
		public_key_env: "LANGFUSE_PUBLIC_KEY",
		secret_key_env: "LANGFUSE_SECRET_KEY",
		project: "",
	},
	langsmith: {
		name: "langsmith",
		kind: "langsmith",
		host: "https://api.smith.langchain.com",
		api_key_env: "LANGSMITH_API_KEY",
		project: "",
	},
};

/** The sorted, unique names of the org's stored secrets, for the key field suggestions. Exported for tests. */
export function secretNameOptions(secrets: readonly { name: string }[] | undefined): string[] {
	return [...new Set((secrets ?? []).map((x) => x.name.trim()).filter(Boolean))].sort();
}

/** The pull setup: what is missing (with links to Settings > Secrets) and a form that adds a source to inbox/sources.yaml. */
export function InboxPullSetup({ check, hosted }: { check?: PullCheck; hosted?: boolean }) {
	const { t } = useTranslation("liveinbox");
	const dispatch = useAppDispatch();
	const [v, setV] = useState<InboxSourceParams>(SOURCE_DEFAULTS.bifrost);
	const ids = useId();
	const { t: f } = useTranslation("uf7a");
	const params = inboxSourceParams(v);
	const missing = inboxSourceMissing(v);
	const labels: Partial<Record<keyof InboxSourceParams, string>> = {
		name: t("inbox.source.name"),
		url: t("inbox.source.url"),
		host: t("inbox.source.host"),
		project: v.kind === "langsmith" ? t("inbox.source.projectId") : t("inbox.source.project"),
		token_env: t("inbox.source.tokenEnv"),
		public_key_env: t("inbox.source.publicKeyEnv"),
		secret_key_env: t("inbox.source.secretKeyEnv"),
		api_key_env: t("inbox.source.apiKeyEnv"),
	};
	const required = new Set(REQUIRED_FIELDS[v.kind]);
	// On the hosted service the key fields suggest the org's stored secrets by name; typing stays free (M7-6).
	const secrets = useListSecretsQuery(undefined, { skip: !hosted });
	const secretNames = secretNameOptions(secrets.data?.secrets);
	// A secret name the pull check knows and reports as not stored (M7-7).
	const notStored = (name: string) => Boolean(name.trim()) && (check?.secrets ?? []).some((x) => x.name === name.trim() && !x.set);
	const field = (key: keyof InboxSourceParams, label: string, placeholder?: string) => (
		<label className="flex min-w-48 flex-1 flex-col gap-1 text-sm" htmlFor={`${ids}-${key}`}>
			<span className="text-foreground font-medium">
				{label}
				{required.has(key) && <span className="text-muted-foreground font-normal"> {f("source.required")}</span>}
			</span>
			<input
				id={`${ids}-${key}`}
				className="border-input bg-card dark:bg-input/20 h-8 rounded-md border px-2 text-sm shadow-xs outline-none"
				value={(v[key] as string | undefined) ?? ""}
				placeholder={placeholder ?? SECRET_PLACEHOLDERS[key]}
				required={required.has(key)}
				onChange={(e) => setV({ ...v, [key]: e.target.value })}
				list={key.endsWith("_env") && secretNames.length ? `${ids}-secrets` : undefined}
				data-testid={`inbox-source-${key}`}
			/>
			{key.endsWith("_env") && notStored((v[key] as string | undefined) ?? "") && (
				<span className="text-warning-ink text-sm" data-testid={`inbox-source-${key}-not-stored`}>
					{f("source.notStored")}
				</span>
			)}
		</label>
	);
	const secretHint = hosted ? t("inbox.source.secretHintHosted") : t("inbox.source.secretHint");
	const form = (
		<div className="flex flex-col gap-3">
			{secretNames.length > 0 && (
				<datalist id={`${ids}-secrets`} data-testid="inbox-source-secret-names">
					{secretNames.map((n) => (
						<option key={n} value={n} />
					))}
				</datalist>
			)}
			<label className="flex flex-col gap-1 text-sm" htmlFor={`${ids}-kind`}>
				<span className="text-foreground font-medium">{t("inbox.source.kind")}</span>
				<select
					id={`${ids}-kind`}
					className="border-input bg-card dark:bg-input/20 h-8 w-full max-w-56 rounded-md border px-2 text-sm shadow-xs outline-none"
					value={v.kind}
					onChange={(e) => setV(switchSourceKind(v, e.target.value as InboxSourceParams["kind"]))}
					data-testid="inbox-source-kind"
				>
					<option value="bifrost">Bifrost gateway</option>
					<option value="langfuse">Langfuse</option>
					<option value="langsmith">LangSmith</option>
				</select>
			</label>
			<div className="flex flex-wrap gap-3">
				{field("name", t("inbox.source.name"))}
				{v.kind === "bifrost" ? field("url", t("inbox.source.url"), "https://gateway.example.com") : field("host", t("inbox.source.host"))}
				{v.kind !== "bifrost" && field("project", v.kind === "langsmith" ? t("inbox.source.projectId") : t("inbox.source.project"))}
			</div>
			<div className="flex flex-wrap gap-3">
				{v.kind === "bifrost" && field("token_env", t("inbox.source.tokenEnv"))}
				{v.kind === "langfuse" && field("public_key_env", t("inbox.source.publicKeyEnv"))}
				{v.kind === "langfuse" && field("secret_key_env", t("inbox.source.secretKeyEnv"))}
				{v.kind === "langsmith" && field("api_key_env", t("inbox.source.apiKeyEnv"))}
			</div>
			<p className="text-muted-foreground text-sm">
				{secretHint}{" "}
				{hosted && (
					<a href="/settings?tab=secrets" className="text-primary font-medium underline underline-offset-2">
						{t("pull.steps.openSecrets")}
					</a>
				)}
			</p>
			<div className="flex flex-wrap items-center gap-3">
				{/* The submit always shows; while a required field is empty it is disabled and the line beside it names what is missing (M7-6). */}
				<JobRunButton
					kind="inbox.source"
					params={params ?? { name: v.name, kind: v.kind }}
					label={t("inbox.source.submit")}
					disabled={!params}
					onDone={() => dispatch(liveinboxApi.util.invalidateTags([{ type: "BenchPullReady", id: "INFO" }]))}
				/>
				{!params && (
					<span className="text-muted-foreground text-sm" data-testid="inbox-source-missing">
						{f("source.stillNeeded", {
							fields: missing.map((k) => labels[k] ?? k).join(", "),
						})}
					</span>
				)}
			</div>
		</div>
	);
	const ready = Boolean(check?.ready);
	return (
		<div id="inbox-source-setup" className="scroll-mt-4">
			<Section title={t("inbox.source.title")} description={t("inbox.source.description")} testId="inbox-source-setup">
				<div className="flex flex-col gap-3">
					<PullSteps check={check} testId="inbox-pull-steps" />
					{ready ? (
						<details>
							<summary className="text-primary cursor-pointer text-sm font-medium">{t("inbox.source.another")}</summary>
							<div className="mt-2">{form}</div>
						</details>
					) : (
						form
					)}
				</div>
			</Section>
		</div>
	);
}

function InboxPullControls() {
	const { t } = useTranslation("liveinbox");
	const dispatch = useAppDispatch();
	const [since, setSince] = useState<(typeof SINCE_OPTIONS)[number]>("");
	const id = useId();
	return (
		<span className="flex items-center gap-2" data-testid="inbox-pull">
			<label htmlFor={id} className="text-muted-foreground text-xs">
				{t("inbox.pull.since")}
			</label>
			<select
				id={id}
				className="border-input bg-card dark:bg-input/20 h-8 rounded-md border px-2 text-xs shadow-xs outline-none"
				value={since}
				onChange={(e) => setSince(e.target.value as (typeof SINCE_OPTIONS)[number])}
				data-testid="inbox-pull-since"
			>
				{SINCE_OPTIONS.map((s) => (
					<option key={s} value={s}>
						{s === "" ? t("inbox.pull.sinceCursor") : s}
					</option>
				))}
			</select>
			<JobRunButton
				kind="inbox.pull"
				params={since ? { since } : {}}
				label={t("inbox.pull.label")}
				onDone={() => dispatch(inboxApi.util.invalidateTags([{ type: "BenchInbox", id: "LIST" }]))}
			/>
		</span>
	);
}

/** A copy icon button with a label, so it reads as an action, not a status (M7-9). */
function CopyButton({ text, testId, label }: { text: string; testId: string; label?: string }) {
	const { t } = useTranslation("liveinbox");
	const [copied, setCopied] = useState(false);
	const name = label ?? t("inbox.webhook.copy");
	return (
		<Button
			size="sm"
			variant="outline"
			className="shrink-0"
			aria-label={copied ? t("inbox.webhook.copied") : name}
			data-testid={testId}
			onClick={() => {
				void navigator.clipboard?.writeText(text).then(() => setCopied(true));
			}}
		>
			{copied ? <Check className="size-3.5" aria-hidden="true" /> : <Copy className="size-3.5" aria-hidden="true" />}
			{copied ? t("inbox.webhook.copied") : t("inbox.webhook.copy")}
		</Button>
	);
}

/**
 * The env name of the inbox webhook token when the server names none. This is the inbox's own
 * secret, not the trace ingest token (HEIMDALL_INGEST_TOKEN, shown on the traces screen).
 */
export const INBOX_TOKEN_ENV = "HEIMDALL_INBOX_TOKEN";

/**
 * The server's next step for the inbox webhook in this screen's words: the token here is the inbox
 * token; "ingest token" names only the trace ingest token on the From traces screen (R7-2). Pure.
 */
export function inboxNextStep(next: string): string {
	return next.replace(/\bingest tokens?\b/gi, (m) => (m.toLowerCase().endsWith("s") ? "inbox tokens" : "inbox token"));
}

/** The webhook setup: ingest URL, token variable, next step, curl example and sources.yaml snippet. Exported for tests. */
export function InboxWebhookCard({ info }: { info: InboxWebhookInfo }) {
	const { t } = useTranslation("liveinbox");
	const [token, setToken] = useState<string | null>(null);
	const { t: f } = useTranslation("uf7a");
	const envName = info.token_env || INBOX_TOKEN_ENV;
	return (
		<Section title={t("inbox.webhook.title")} description={t("inbox.webhook.description")} testId="inbox-webhook">
			<div className="flex min-w-0 flex-col gap-3 text-sm">
				<div className="flex min-w-0 flex-wrap items-center gap-2">
					<span className="text-foreground font-medium">{t("inbox.webhook.url")}</span>
					<code className="bg-muted min-w-0 rounded px-2 py-1 font-mono text-xs break-all" data-testid="inbox-webhook-url">
						{info.url}
					</code>
					<CopyButton text={info.url} testId="inbox-webhook-copy-url" />
					<span
						className={info.enabled ? "text-success-ink font-medium" : "text-muted-foreground font-medium"}
						data-testid={info.enabled ? "inbox-webhook-on" : "inbox-webhook-off"}
					>
						{info.enabled ? t("inbox.webhook.on") : t("inbox.webhook.off")}
					</span>
				</div>
				{info.token_env && !info.hosted && (
					<p data-testid="inbox-webhook-token-env">
						<span className="text-foreground font-medium">{t("inbox.webhook.tokenEnv")}</span>{" "}
						<code className="font-mono">{info.token_env}</code>{" "}
						{info.token_set ? t("inbox.webhook.tokenSet") : t("inbox.webhook.tokenMissing")}
					</p>
				)}
				{info.problem && (
					<p className="text-danger-ink" role="note" data-testid="inbox-webhook-problem">
						{info.problem}
					</p>
				)}
				{/* How to send failures in, as three numbered steps with one token name (M7-8, M1-14); the server's own next step stays below. */}
				<ol className="list-decimal space-y-1 pl-5" data-testid="inbox-webhook-steps">
					<li>{f("ingest.step1")}</li>
					<li>
						{f("ingest.step2")} <code className="font-mono text-xs">{envName}</code>
					</li>
					<li>{f("ingest.step3")}</li>
				</ol>
				{info.next && (
					<p data-testid="inbox-webhook-next">
						<span className="text-foreground font-medium">{t("inbox.webhook.next")}:</span> {inboxNextStep(info.next)}
					</p>
				)}
				{info.sources_snippet && (
					<div className="flex flex-col gap-1">
						<span className="text-foreground font-medium">{t("inbox.webhook.snippet")}</span>
						<pre className="bg-muted overflow-x-auto rounded p-2 font-mono text-xs whitespace-pre" data-testid="inbox-webhook-snippet">
							{info.sources_snippet}
						</pre>
					</div>
				)}
				{info.ingest_tokens && <InboxTokens envName={envName} />}
				{!info.hosted && !info.enabled && (
					<div className="flex flex-col gap-2">
						<div>
							<Button size="sm" variant="outline" onClick={() => setToken(generateInboxToken())} data-testid="inbox-webhook-generate">
								{t("inbox.webhook.generate")}
							</Button>
						</div>
						{token && (
							<div className="flex flex-col gap-1" data-testid="inbox-webhook-token">
								<p className="text-muted-foreground">{t("inbox.webhook.generated")}</p>
								<div className="flex items-center gap-2">
									<code className="bg-muted rounded px-2 py-1 font-mono break-all">{`export ${envName}=${token}`}</code>
									<CopyButton text={`export ${envName}=${token}`} testId="inbox-webhook-copy-token" />
								</div>
							</div>
						)}
					</div>
				)}
				<div className="flex min-w-0 flex-col gap-1">
					<div className="flex items-center justify-between gap-2">
						<span className="text-foreground font-medium">{t("inbox.webhook.example")}</span>
						<CopyButton text={info.example} testId="inbox-webhook-copy-example" />
					</div>
					<pre
						className="bg-muted max-w-full overflow-x-auto rounded p-2 font-mono text-xs whitespace-pre"
						data-testid="inbox-webhook-example"
					>
						{info.example}
					</pre>
				</div>
			</div>
		</Section>
	);
}

function shortDate(iso?: string): string {
	if (!iso) return "";
	const d = new Date(iso);
	return Number.isNaN(d.getTime()) ? iso : d.toLocaleString();
}

export interface InboxTokensViewProps {
	list: InboxTokenList;
	envName: string;
	isAdmin: boolean;
	/** The token just made: shown once, until the page is left. */
	created: InboxTokenCreated | null;
	creating?: boolean;
	revokingId?: string | null;
	error?: string | null;
	onCreate(label: string): void;
	onRevoke(id: string): void;
}

/** The hosted per-org inbox tokens: generate (admins, server side, shown once), list and revoke. Exported for tests. */
export function InboxTokensView({
	list,
	envName,
	isAdmin,
	created,
	creating,
	revokingId,
	error,
	onCreate,
	onRevoke,
}: InboxTokensViewProps) {
	const { t } = useTranslation("liveinbox");
	const { t: f } = useTranslation("uf7a");
	const [label, setLabel] = useState("");
	const labelId = useId();
	const full = list.tokens.length >= list.max;
	const submit = (e: FormEvent) => {
		e.preventDefault();
		onCreate(label.trim());
	};
	// The label is cleared only once the token was made, so a refused request keeps what was typed (M7-6).
	const [clearedFor, setClearedFor] = useState<string | null>(null);
	if (created && created.token.id !== clearedFor) {
		setClearedFor(created.token.id);
		setLabel("");
	}
	return (
		<div className="flex flex-col gap-3" data-testid="inbox-tokens">
			<span className="text-foreground font-medium">{t("inbox.tokens.title")}</span>
			<p className="text-muted-foreground" data-testid="inbox-tokens-howto">
				{f("ingest.howTo", { env: envName })}
			</p>
			{isAdmin ? (
				<form className="flex flex-wrap items-end gap-2" onSubmit={submit}>
					<div className="flex flex-col gap-1">
						<label htmlFor={labelId} className="text-muted-foreground">
							{t("inbox.tokens.label")}
						</label>
						<Input
							id={labelId}
							value={label}
							maxLength={80}
							placeholder={t("inbox.tokens.labelPlaceholder")}
							onChange={(e) => setLabel(e.target.value)}
							data-testid="inbox-tokens-label"
						/>
					</div>
					<Button
						type="submit"
						size="sm"
						variant={list.tokens.length === 0 ? "default" : "outline"}
						disabled={full || creating}
						data-testid="inbox-webhook-generate"
					>
						{t("inbox.webhook.generate")}
					</Button>
					{full && <span className="text-muted-foreground">{t("inbox.tokens.full", { max: list.max })}</span>}
				</form>
			) : (
				<p className="text-muted-foreground" data-testid="inbox-tokens-viewer">
					{t("inbox.tokens.viewer")}
				</p>
			)}
			{error && (
				<p className="text-danger-ink" role="alert" data-testid="inbox-tokens-error">
					{error}
				</p>
			)}
			{created && (
				<div className="flex flex-col gap-1 rounded border p-2" data-testid="inbox-webhook-token">
					<p className="text-muted-foreground">{t("inbox.tokens.createdOnce")}</p>
					<div className="flex items-center gap-2">
						<code className="bg-muted rounded px-2 py-1 font-mono break-all">{`export ${envName}=${created.secret}`}</code>
						<CopyButton text={`export ${envName}=${created.secret}`} testId="inbox-webhook-copy-token" />
					</div>
				</div>
			)}
			{list.tokens.length === 0 ? (
				<p className="text-muted-foreground" data-testid="inbox-tokens-empty">
					{isAdmin ? t("inbox.tokens.emptyAdmin") : t("inbox.tokens.empty")}
				</p>
			) : (
				<table className="w-full text-left" data-testid="inbox-tokens-table">
					<thead className="text-muted-foreground">
						<tr>
							<th className="py-1 pr-2 font-normal">{t("inbox.tokens.colLabel")}</th>
							<th className="py-1 pr-2 font-normal">{t("inbox.tokens.colToken")}</th>
							<th className="py-1 pr-2 font-normal">{t("inbox.tokens.colCreated")}</th>
							<th className="py-1 pr-2 font-normal">{t("inbox.tokens.colUsed")}</th>
							<th className="py-1 font-normal" />
						</tr>
					</thead>
					<tbody>
						{list.tokens.map((tok) => (
							<tr key={tok.id} className="border-t" data-testid={`inbox-token-${tok.id}`}>
								<td className="py-1 pr-2">{tok.label || "-"}</td>
								<td className="py-1 pr-2 font-mono">{`hmi_...${tok.last4}`}</td>
								<td className="py-1 pr-2">{shortDate(tok.created_at)}</td>
								<td className="py-1 pr-2">{tok.last_used_at ? shortDate(tok.last_used_at) : t("inbox.tokens.never")}</td>
								<td className="py-1 text-right">
									{isAdmin && (
										<ConfirmButton
											label={t("inbox.tokens.revoke")}
											question={t("inbox.tokens.revokeQuestion")}
											confirmLabel={t("inbox.tokens.revoke")}
											cancelLabel={t("inbox.tokens.cancel")}
											variant="destructive"
											isLoading={revokingId === tok.id}
											onConfirm={() => onRevoke(tok.id)}
											testId={`inbox-token-revoke-${tok.id}`}
										/>
									)}
								</td>
							</tr>
						))}
					</tbody>
				</table>
			)}
		</div>
	);
}

/** Loads the org's ingest tokens and wires generate and revoke. */
function InboxTokens({ envName }: { envName: string }) {
	const { t } = useTranslation("liveinbox");
	const session = useSession();
	const q = useGetInboxTokensQuery();
	const [create, createState] = useCreateInboxTokenMutation();
	const [revoke] = useRevokeInboxTokenMutation();
	const [created, setCreated] = useState<InboxTokenCreated | null>(null);
	const [revokingId, setRevokingId] = useState<string | null>(null);
	const [error, setError] = useState<string | null>(null);
	if (q.isLoading) return <BlockSkeleton rows={1} testId="inbox-tokens-loading" />;
	if (q.error || !q.data)
		return (
			<LoadError title={t("inbox.tokens.loadError")} error={q.error} onRetry={() => void q.refetch()} testId="inbox-tokens-load-error" />
		);
	return (
		<InboxTokensView
			list={q.data}
			envName={envName}
			isAdmin={roleAtLeast(session.role, "admin")}
			created={created}
			creating={createState.isLoading}
			revokingId={revokingId}
			error={error}
			onCreate={(label) => {
				setError(null);
				create(label ? { label } : {})
					.unwrap()
					.then(setCreated)
					.catch((e: unknown) => setError(getBenchErrorMessage(e)));
			}}
			onRevoke={(id) => {
				setError(null);
				setRevokingId(id);
				revoke(id)
					.unwrap()
					.then(() => {
						if (created?.token.id === id) setCreated(null);
					})
					.catch((e: unknown) => setError(getBenchErrorMessage(e)))
					.finally(() => setRevokingId(null));
			}}
		/>
	);
}

/** Loads the webhook setup and shows the card. */
export function InboxWebhook() {
	const { t } = useTranslation("liveinbox");
	const q = useGetInboxWebhookQuery();
	if (q.isLoading) return <BlockSkeleton rows={1} testId="inbox-webhook-loading" />;
	if (q.error || !q.data)
		return <LoadError title={t("inbox.webhook.loadError")} error={q.error} onRetry={() => void q.refetch()} testId="inbox-webhook-error" />;
	return <InboxWebhookCard info={q.data} />;
}