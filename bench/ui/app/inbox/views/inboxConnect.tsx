import { JobRunButton } from "@/components/jobs/jobRunButton";
import { LoadError, Section } from "@/components/runs/screenStates";
import { Button } from "@/components/ui/button";
import { inboxApi } from "@/lib/store/apis/inboxApi";
import { liveinboxApi, useGetInboxWebhookQuery } from "@/lib/store/apis/liveinboxApi";
import { useAppDispatch } from "@/lib/store/hooks";
import type { InboxSourceParams, InboxWebhookInfo, PullCheck } from "@/lib/types/liveinbox";
import { PullSteps } from "@/components/inbox/pullSteps";
import { useId, useState } from "react";
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
			<a href="#inbox-source-setup" className="text-primary text-xs font-medium underline underline-offset-2" data-testid="inbox-pull-setup-link">
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

const SOURCE_DEFAULTS: Record<InboxSourceParams["kind"], InboxSourceParams> = {
	bifrost: { name: "gateway", kind: "bifrost", url: "", token_env: "BIFROST_ADMIN_TOKEN" },
	langfuse: { name: "langfuse", kind: "langfuse", host: "https://cloud.langfuse.com", public_key_env: "LANGFUSE_PUBLIC_KEY", secret_key_env: "LANGFUSE_SECRET_KEY", project: "" },
	langsmith: { name: "langsmith", kind: "langsmith", host: "https://api.smith.langchain.com", api_key_env: "LANGSMITH_API_KEY", project: "" },
};

/** The pull setup: what is missing (with links to Settings > Secrets) and a form that adds a source to inbox/sources.yaml. */
export function InboxPullSetup({ check, hosted }: { check?: PullCheck; hosted?: boolean }) {
	const { t } = useTranslation("liveinbox");
	const dispatch = useAppDispatch();
	const [v, setV] = useState<InboxSourceParams>(SOURCE_DEFAULTS.bifrost);
	const ids = useId();
	const params = inboxSourceParams(v);
	const field = (key: keyof InboxSourceParams, label: string, placeholder?: string) => (
		<label className="flex min-w-48 flex-1 flex-col gap-1 text-xs" htmlFor={`${ids}-${key}`}>
			<span className="text-foreground font-medium">{label}</span>
			<input
				id={`${ids}-${key}`}
				className="border-input bg-card dark:bg-input/20 h-8 rounded-md border px-2 text-xs shadow-xs outline-none"
				value={(v[key] as string | undefined) ?? ""}
				placeholder={placeholder}
				onChange={(e) => setV({ ...v, [key]: e.target.value })}
				data-testid={`inbox-source-${key}`}
			/>
		</label>
	);
	const secretHint = hosted ? t("inbox.source.secretHintHosted") : t("inbox.source.secretHint");
	const form = (
		<div className="flex flex-col gap-3 rounded-lg border p-4">
			<label className="flex flex-col gap-1 text-xs" htmlFor={`${ids}-kind`}>
				<span className="text-foreground font-medium">{t("inbox.source.kind")}</span>
				<select
					id={`${ids}-kind`}
					className="border-input bg-card dark:bg-input/20 h-8 w-56 rounded-md border px-2 text-xs shadow-xs outline-none"
					value={v.kind}
					onChange={(e) => setV(SOURCE_DEFAULTS[e.target.value as InboxSourceParams["kind"]])}
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
			<p className="text-muted-foreground text-xs">
				{secretHint}{" "}
				{hosted && (
					<a href="/settings?tab=secrets" className="text-primary font-medium underline underline-offset-2">
						{t("pull.steps.openSecrets")}
					</a>
				)}
			</p>
			<div className="flex flex-wrap items-center gap-3">
				{params ? (
					<JobRunButton
						kind="inbox.source"
						params={params}
						label={t("inbox.source.submit")}
						onDone={() => dispatch(liveinboxApi.util.invalidateTags([{ type: "BenchPullReady", id: "INFO" }]))}
					/>
				) : (
					<span className="text-muted-foreground text-xs">{t("inbox.source.incomplete")}</span>
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
							<summary className="text-primary cursor-pointer text-xs font-medium">{t("inbox.source.another")}</summary>
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

function CopyButton({ text, testId }: { text: string; testId: string }) {
	const { t } = useTranslation("liveinbox");
	const [copied, setCopied] = useState(false);
	return (
		<Button
			size="sm"
			variant="ghost"
			data-testid={testId}
			onClick={() => {
				void navigator.clipboard?.writeText(text).then(() => setCopied(true));
			}}
		>
			{copied ? t("inbox.webhook.copied") : t("inbox.webhook.copy")}
		</Button>
	);
}

/** The webhook setup: ingest URL, token variable, next step, curl example and sources.yaml snippet. Exported for tests. */
export function InboxWebhookCard({ info }: { info: InboxWebhookInfo }) {
	const { t } = useTranslation("liveinbox");
	const [token, setToken] = useState<string | null>(null);
	const envName = info.token_env || "HEIMDALL_INBOX_TOKEN";
	return (
		<Section title={t("inbox.webhook.title")} description={t("inbox.webhook.description")} testId="inbox-webhook">
			<div className="flex flex-col gap-3 rounded-lg border p-4 text-xs">
				<div className="flex flex-wrap items-center gap-2">
					<span className="text-foreground font-medium">{t("inbox.webhook.url")}</span>
					<code className="bg-muted rounded px-2 py-1 font-mono break-all" data-testid="inbox-webhook-url">
						{info.url}
					</code>
					<CopyButton text={info.url} testId="inbox-webhook-copy-url" />
					<span
						className={info.enabled ? "text-chart-success-ink font-medium" : "text-muted-foreground font-medium"}
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
					<p className="text-chart-error-ink" role="note" data-testid="inbox-webhook-problem">
						{info.problem}
					</p>
				)}
				{info.next && (
					<p data-testid="inbox-webhook-next">
						<span className="text-foreground font-medium">{t("inbox.webhook.next")}:</span> {info.next}
					</p>
				)}
				{info.sources_snippet && (
					<div className="flex flex-col gap-1">
						<span className="text-foreground font-medium">{t("inbox.webhook.snippet")}</span>
						<pre className="bg-muted overflow-x-auto rounded p-2 font-mono" data-testid="inbox-webhook-snippet">
							{info.sources_snippet}
						</pre>
					</div>
				)}
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
				<div className="flex flex-col gap-1">
					<span className="text-foreground font-medium">{t("inbox.webhook.example")}</span>
					<pre className="bg-muted overflow-x-auto rounded p-2 font-mono" data-testid="inbox-webhook-example">
						{info.example}
					</pre>
				</div>
			</div>
		</Section>
	);
}

/** Loads the webhook setup and shows the card. */
export function InboxWebhook() {
	const { t } = useTranslation("liveinbox");
	const q = useGetInboxWebhookQuery();
	if (q.isLoading) return null;
	if (q.error || !q.data)
		return <LoadError title={t("inbox.webhook.loadError")} error={q.error} onRetry={() => void q.refetch()} testId="inbox-webhook-error" />;
	return <InboxWebhookCard info={q.data} />;
}
