import { JobRunButton } from "@/components/jobs/jobRunButton";
import { LoadError, Section } from "@/components/runs/screenStates";
import { Button } from "@/components/ui/button";
import { inboxApi } from "@/lib/store/apis/inboxApi";
import { useGetInboxWebhookQuery } from "@/lib/store/apis/liveinboxApi";
import { useAppDispatch } from "@/lib/store/hooks";
import type { InboxWebhookInfo } from "@/lib/types/liveinbox";
import { useId, useState } from "react";
import { useTranslation } from "react-i18next";

const SINCE_OPTIONS = ["", "24h", "7d", "30d"] as const;

/** A random 32-byte token as hex, made in the browser; Heimdall never sees it. Exported for tests. */
export function generateInboxToken(random: (a: Uint8Array) => Uint8Array = (a) => crypto.getRandomValues(a)): string {
	return Array.from(random(new Uint8Array(32)), (b) => b.toString(16).padStart(2, "0")).join("");
}

/** "Pull now" for the inbox: queues inbox.pull over every pull source of inbox/sources.yaml. */
export function InboxPullButton({ hosted }: { hosted?: boolean }) {
	const { t } = useTranslation("liveinbox");
	if (hosted) {
		return (
			<p className="text-muted-foreground max-w-prose text-xs" data-testid="inbox-hosted-pull">
				{t("inbox.pull.hosted")}
			</p>
		);
	}
	return <InboxPullControls />;
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
