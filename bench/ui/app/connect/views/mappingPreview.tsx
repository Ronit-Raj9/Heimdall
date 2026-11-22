import type { ConnectCandidate } from "@/lib/types/connect";
import { useTranslation } from "react-i18next";
import { isProven } from "./connect.utils";

function Field({ label, children, testId }: { label: string; children: React.ReactNode; testId?: string }) {
	return (
		<div className="grid gap-1 sm:grid-cols-[10rem_1fr] sm:gap-3" data-testid={testId}>
			<dt className="text-muted-foreground text-xs font-medium uppercase">{label}</dt>
			<dd className="min-w-0 text-sm break-words">{children}</dd>
		</div>
	);
}

/**
 * How Heimdall will call the agent: the URL, the request template with {{instruction}} where the
 * task goes, the answer path the check code came back at, the stream and the sign-in. Secret
 * values never appear here, only their names.
 */
export function MappingPreview({ candidate }: { candidate: ConnectCandidate }) {
	const { t } = useTranslation("connect");
	const m = candidate.mapping;
	const p = candidate.params ?? {};
	const url = p.url ?? p.card_url ?? candidate.url ?? "";
	const headers = Object.entries(m?.headers ?? {});
	const auth = candidate.auth?.type
		? candidate.auth.type
		: p.auth_secret
			? `${p.auth_header || "Authorization: Bearer"} ${p.auth_secret}`
			: p.api_key_secret
				? `Bearer ${p.api_key_secret}`
				: "";
	return (
		<dl className="flex flex-col gap-3" data-testid="mapping-preview">
			<Field label={t("mapping.method")}>{m?.method ?? "POST"}</Field>
			{url && (
				<Field label={t("mapping.url")} testId="mapping-url">
					<code className="text-xs">{url}</code>
				</Field>
			)}
			{!m && candidate.template === "http-url" && isProven(candidate) && (
				<p className="text-muted-foreground text-sm">{t("mapping.own")}</p>
			)}
			{m?.start && (
				<Field label={t("mapping.start")}>
					<code className="text-xs">
						{m.start.method ?? "POST"} {m.start.url}
					</code>
				</Field>
			)}
			{m?.body && (
				<Field label={t("mapping.body")} testId="mapping-body">
					<pre className="bg-muted overflow-x-auto rounded p-2 text-xs whitespace-pre-wrap">{m.body}</pre>
				</Field>
			)}
			{m?.answer_path && (
				<Field label={t("mapping.answerPath")} testId="mapping-answer-path">
					<code className="text-xs">{m.answer_path}</code>
				</Field>
			)}
			{m?.stream && (
				<Field label={t("mapping.stream")}>
					{m.stream}
					{m.stream_path ? (
						<>
							{" "}
							<span className="text-muted-foreground">{t("mapping.streamPath")}:</span> <code className="text-xs">{m.stream_path}</code>
						</>
					) : null}
				</Field>
			)}
			{m?.poll && (
				<Field label={t("mapping.poll")}>
					<code className="text-xs">{m.poll.url}</code>
				</Field>
			)}
			{headers.length > 0 && (
				<Field label={t("mapping.headers")}>
					<ul className="flex flex-col gap-0.5">
						{headers.map(([k, v]) => (
							<li key={k}>
								<code className="text-xs">
									{k}: {v}
								</code>
							</li>
						))}
					</ul>
				</Field>
			)}
			{auth && (
				<Field label={t("mapping.auth")} testId="mapping-auth">
					<code className="text-xs">{auth}</code>
				</Field>
			)}
		</dl>
	);
}